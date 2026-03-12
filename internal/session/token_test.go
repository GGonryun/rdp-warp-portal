package session

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestGenerateToken(t *testing.T) {
	token1, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	// Base64url encoding of 32 bytes = 43 characters (no padding with RawURLEncoding)
	if len(token1) != 43 {
		t.Errorf("expected token length 43, got %d", len(token1))
	}

	// Generate another token - should be different
	token2, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	if token1 == token2 {
		t.Error("expected different tokens, got identical")
	}
}

func TestNewToken(t *testing.T) {
	ttl := 60 * time.Second
	token, err := NewToken(ttl)
	if err != nil {
		t.Fatalf("NewToken failed: %v", err)
	}

	if token.Value() == "" {
		t.Error("token value is empty")
	}

	if len(token.Value()) != 43 {
		t.Errorf("expected token length 43, got %d", len(token.Value()))
	}

	// Expiry should be approximately TTL from now
	expectedExpiry := time.Now().Add(ttl)
	if token.Expiry().Sub(expectedExpiry) > time.Second {
		t.Errorf("expiry time mismatch: got %v, expected ~%v", token.Expiry(), expectedExpiry)
	}

	if token.IsExpired() {
		t.Error("new token should not be expired")
	}

	if token.IsConsumed() {
		t.Error("new token should not be consumed")
	}
}

func TestToken_Validate_Success(t *testing.T) {
	token, _ := NewToken(60 * time.Second)

	err := token.Validate(token.Value())
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	// Token should now be consumed
	if !token.IsConsumed() {
		t.Error("token should be consumed after validation")
	}
}

func TestToken_Validate_Mismatch(t *testing.T) {
	token, _ := NewToken(60 * time.Second)

	err := token.Validate("wrong-token-value")
	if !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("expected ErrTokenMismatch, got %v", err)
	}

	// Token should not be consumed
	if token.IsConsumed() {
		t.Error("token should not be consumed after failed validation")
	}
}

func TestToken_Validate_AlreadyUsed(t *testing.T) {
	token, _ := NewToken(60 * time.Second)
	value := token.Value()

	// First validation should succeed
	err := token.Validate(value)
	if err != nil {
		t.Fatalf("first validation failed: %v", err)
	}

	// Second validation should fail
	err = token.Validate(value)
	if !errors.Is(err, ErrTokenAlreadyUsed) {
		t.Errorf("expected ErrTokenAlreadyUsed, got %v", err)
	}
}

func TestToken_Validate_Expired(t *testing.T) {
	// Create token with very short TTL
	token, _ := NewToken(1 * time.Millisecond)
	value := token.Value()

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	err := token.Validate(value)
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestToken_IsExpired(t *testing.T) {
	// Create token with very short TTL
	token, _ := NewToken(10 * time.Millisecond)

	if token.IsExpired() {
		t.Error("token should not be expired immediately")
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	if !token.IsExpired() {
		t.Error("token should be expired after TTL")
	}
}

func TestToken_ValidateWithoutConsume(t *testing.T) {
	token, _ := NewToken(60 * time.Second)
	value := token.Value()

	// Validate without consuming
	err := token.ValidateWithoutConsume(value)
	if err != nil {
		t.Fatalf("ValidateWithoutConsume failed: %v", err)
	}

	// Token should NOT be consumed
	if token.IsConsumed() {
		t.Error("token should not be consumed after ValidateWithoutConsume")
	}

	// Can still do the real validation
	err = token.Validate(value)
	if err != nil {
		t.Fatalf("Validate after ValidateWithoutConsume failed: %v", err)
	}

	// Now it should be consumed
	if !token.IsConsumed() {
		t.Error("token should be consumed after Validate")
	}
}

func TestToken_ValidateWithoutConsume_Errors(t *testing.T) {
	t.Run("mismatch", func(t *testing.T) {
		token, _ := NewToken(60 * time.Second)
		err := token.ValidateWithoutConsume("wrong")
		if !errors.Is(err, ErrTokenMismatch) {
			t.Errorf("expected ErrTokenMismatch, got %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		token, _ := NewToken(1 * time.Millisecond)
		time.Sleep(10 * time.Millisecond)
		err := token.ValidateWithoutConsume(token.Value())
		if !errors.Is(err, ErrTokenExpired) {
			t.Errorf("expected ErrTokenExpired, got %v", err)
		}
	})

	t.Run("already_used", func(t *testing.T) {
		token, _ := NewToken(60 * time.Second)
		token.Validate(token.Value()) // Consume it
		err := token.ValidateWithoutConsume(token.Value())
		if !errors.Is(err, ErrTokenAlreadyUsed) {
			t.Errorf("expected ErrTokenAlreadyUsed, got %v", err)
		}
	})
}

func TestToken_TTLRemaining(t *testing.T) {
	ttl := 60 * time.Second
	token, _ := NewToken(ttl)

	remaining := token.TTLRemaining()
	if remaining < 59*time.Second || remaining > 60*time.Second {
		t.Errorf("expected TTL remaining ~60s, got %v", remaining)
	}
}

func TestToken_TTLRemaining_Negative(t *testing.T) {
	token, _ := NewToken(1 * time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	remaining := token.TTLRemaining()
	if remaining >= 0 {
		t.Errorf("expected negative TTL remaining, got %v", remaining)
	}
}

func TestToken_ConcurrentValidation(t *testing.T) {
	token, _ := NewToken(60 * time.Second)
	value := token.Value()

	const numGoroutines = 100
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := token.Validate(value)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Only one goroutine should succeed
	if successCount != 1 {
		t.Errorf("expected exactly 1 successful validation, got %d", successCount)
	}
}

func TestToken_ConcurrentValidation_DifferentTokens(t *testing.T) {
	const numTokens = 100
	var wg sync.WaitGroup
	errorChan := make(chan error, numTokens)

	for i := 0; i < numTokens; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := NewToken(60 * time.Second)
			if err != nil {
				errorChan <- err
				return
			}
			err = token.Validate(token.Value())
			if err != nil {
				errorChan <- err
			}
		}()
	}

	wg.Wait()
	close(errorChan)

	for err := range errorChan {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGenerateToken_Uniqueness(t *testing.T) {
	const numTokens = 1000
	tokens := make(map[string]bool)

	for i := 0; i < numTokens; i++ {
		token, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken failed: %v", err)
		}

		if tokens[token] {
			t.Errorf("duplicate token generated: %s", token)
		}
		tokens[token] = true
	}
}
