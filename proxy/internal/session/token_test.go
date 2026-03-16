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

// TestToken_ZeroTTL tests token behavior with zero TTL (immediate expiration).
func TestToken_ZeroTTL(t *testing.T) {
	token, err := NewToken(0)
	if err != nil {
		t.Fatalf("NewToken with zero TTL failed: %v", err)
	}

	// Token should be immediately expired or very close to it
	// Allow a small buffer for execution time
	time.Sleep(time.Millisecond)
	if !token.IsExpired() {
		t.Error("token with zero TTL should be expired")
	}

	err = token.Validate(token.Value())
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired for zero TTL token, got %v", err)
	}
}

// TestToken_NegativeTTL tests token behavior with negative TTL.
func TestToken_NegativeTTL(t *testing.T) {
	token, err := NewToken(-time.Hour)
	if err != nil {
		t.Fatalf("NewToken with negative TTL failed: %v", err)
	}

	// Token should be expired
	if !token.IsExpired() {
		t.Error("token with negative TTL should be expired")
	}

	if token.TTLRemaining() >= 0 {
		t.Error("TTL remaining should be negative for expired token")
	}
}

// TestToken_LargeTTL tests token behavior with very large TTL.
func TestToken_LargeTTL(t *testing.T) {
	ttl := 365 * 24 * time.Hour // 1 year
	token, err := NewToken(ttl)
	if err != nil {
		t.Fatalf("NewToken with large TTL failed: %v", err)
	}

	if token.IsExpired() {
		t.Error("token with large TTL should not be expired")
	}

	remaining := token.TTLRemaining()
	// Should be close to 1 year (allow 1 second tolerance)
	if remaining < ttl-time.Second || remaining > ttl {
		t.Errorf("unexpected TTL remaining: %v", remaining)
	}
}

// TestToken_EmptyValue tests validation against empty string.
func TestToken_EmptyValue(t *testing.T) {
	token, _ := NewToken(60 * time.Second)

	err := token.Validate("")
	if !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("expected ErrTokenMismatch for empty value, got %v", err)
	}

	err = token.ValidateWithoutConsume("")
	if !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("expected ErrTokenMismatch for empty value in ValidateWithoutConsume, got %v", err)
	}
}

// TestToken_ExpireDuringValidation tests race between expiration and validation.
func TestToken_ExpireDuringValidation(t *testing.T) {
	// Create token that expires very soon
	token, _ := NewToken(5 * time.Millisecond)
	value := token.Value()

	// Wait until token might be expired
	time.Sleep(3 * time.Millisecond)

	// Try many validations - some should fail with expired, none should panic
	for i := 0; i < 100; i++ {
		err := token.Validate(value)
		if err == nil || errors.Is(err, ErrTokenExpired) || errors.Is(err, ErrTokenAlreadyUsed) {
			// These are all acceptable outcomes
			continue
		}
		t.Errorf("unexpected error: %v", err)
	}
}

// TestToken_ConcurrentReads tests that Value() and Expiry() are safe for concurrent access.
func TestToken_ConcurrentReads(t *testing.T) {
	token, _ := NewToken(60 * time.Second)

	var wg sync.WaitGroup
	const numGoroutines = 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = token.Value()
			_ = token.Expiry()
			_ = token.IsExpired()
			_ = token.IsConsumed()
			_ = token.TTLRemaining()
		}()
	}

	wg.Wait()
}

// TestToken_ValidateOrderMatters tests that validation checks happen in correct order.
func TestToken_ValidateOrderMatters(t *testing.T) {
	// Test 1: Wrong value should be rejected even if token is consumed
	token1, _ := NewToken(60 * time.Second)
	token1.Validate(token1.Value()) // Consume it

	err := token1.Validate("wrong-value")
	// Should be ErrTokenMismatch, not ErrTokenAlreadyUsed
	if !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("expected ErrTokenMismatch for wrong value on consumed token, got %v", err)
	}

	// Test 2: Correct value on consumed token should be ErrTokenAlreadyUsed
	token2, _ := NewToken(60 * time.Second)
	value := token2.Value()
	token2.Validate(value) // Consume it

	err = token2.Validate(value)
	if !errors.Is(err, ErrTokenAlreadyUsed) {
		t.Errorf("expected ErrTokenAlreadyUsed, got %v", err)
	}
}

// TestToken_Base64URLSafe tests that generated tokens are URL-safe.
func TestToken_Base64URLSafe(t *testing.T) {
	// Generate many tokens and ensure they're all URL-safe
	for i := 0; i < 100; i++ {
		token, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken failed: %v", err)
		}

		// URL-safe base64 should only contain A-Za-z0-9-_
		for _, r := range token {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_') {
				t.Errorf("token contains non-URL-safe character: %c in %s", r, token)
			}
		}
	}
}

// TestToken_ConcurrentValidateWithoutConsume tests thread-safety of ValidateWithoutConsume.
func TestToken_ConcurrentValidateWithoutConsume(t *testing.T) {
	token, _ := NewToken(60 * time.Second)
	value := token.Value()

	var wg sync.WaitGroup
	const numGoroutines = 100
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := token.ValidateWithoutConsume(value)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// All should succeed since ValidateWithoutConsume doesn't consume
	if successCount != numGoroutines {
		t.Errorf("expected all %d validations to succeed, got %d", numGoroutines, successCount)
	}

	// Token should still not be consumed
	if token.IsConsumed() {
		t.Error("token should not be consumed after ValidateWithoutConsume calls")
	}
}
