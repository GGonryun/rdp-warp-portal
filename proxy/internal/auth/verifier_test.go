package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var errKeyNotFound = errors.New("key not found")

// memKeyStore stores multiple keys per email for testing.
type memKeyStore struct {
	keys map[string][]crypto.PublicKey
}

func (m *memKeyStore) GetPublicKeys(_ context.Context, email string) ([]crypto.PublicKey, error) {
	pks, ok := m.keys[email]
	if !ok || len(pks) == 0 {
		return nil, errKeyNotFound
	}
	return pks, nil
}

func mintJWT(t *testing.T, method jwt.SigningMethod, privKey interface{}, sub string, exp time.Time) string {
	t.Helper()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(method, claims)
	signed, err := token.SignedString(privKey)
	if err != nil {
		t.Fatalf("failed to sign JWT: %v", err)
	}
	return signed
}

func TestVerify_Ed25519_Valid(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{
		"alice@example.com": {pub},
	}}
	v := NewVerifier(store)

	tokenStr := mintJWT(t, jwt.SigningMethodEdDSA, priv, "alice@example.com", time.Now().Add(time.Minute))
	claims, err := v.Verify(context.Background(), tokenStr)
	if err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
	sub, _ := claims.GetSubject()
	if sub != "alice@example.com" {
		t.Fatalf("expected sub alice@example.com, got %s", sub)
	}
}

func TestVerify_RSA_Valid(t *testing.T) {
	privKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{
		"bob@example.com": {&privKey.PublicKey},
	}}
	v := NewVerifier(store)

	tokenStr := mintJWT(t, jwt.SigningMethodRS256, privKey, "bob@example.com", time.Now().Add(time.Minute))
	claims, err := v.Verify(context.Background(), tokenStr)
	if err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
	sub, _ := claims.GetSubject()
	if sub != "bob@example.com" {
		t.Fatalf("expected sub bob@example.com, got %s", sub)
	}
}

func TestVerify_ECDSA_Valid(t *testing.T) {
	privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{
		"carol@example.com": {&privKey.PublicKey},
	}}
	v := NewVerifier(store)

	tokenStr := mintJWT(t, jwt.SigningMethodES256, privKey, "carol@example.com", time.Now().Add(time.Minute))
	claims, err := v.Verify(context.Background(), tokenStr)
	if err != nil {
		t.Fatalf("expected valid, got error: %v", err)
	}
	sub, _ := claims.GetSubject()
	if sub != "carol@example.com" {
		t.Fatalf("expected sub carol@example.com, got %s", sub)
	}
}

func TestVerify_MultipleKeys_SecondMatches(t *testing.T) {
	pub1, _, _ := ed25519.GenerateKey(rand.Reader)
	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{
		"alice@example.com": {pub1, pub2},
	}}
	v := NewVerifier(store)

	tokenStr := mintJWT(t, jwt.SigningMethodEdDSA, priv2, "alice@example.com", time.Now().Add(time.Minute))
	claims, err := v.Verify(context.Background(), tokenStr)
	if err != nil {
		t.Fatalf("expected valid with second key, got error: %v", err)
	}
	sub, _ := claims.GetSubject()
	if sub != "alice@example.com" {
		t.Fatalf("expected sub alice@example.com, got %s", sub)
	}
}

func TestVerify_Expired(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{
		"alice@example.com": {pub},
	}}
	v := NewVerifier(store)

	tokenStr := mintJWT(t, jwt.SigningMethodEdDSA, priv, "alice@example.com", time.Now().Add(-time.Minute))
	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerify_WrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{
		"alice@example.com": {otherPub},
	}}
	v := NewVerifier(store)

	tokenStr := mintJWT(t, jwt.SigningMethodEdDSA, priv, "alice@example.com", time.Now().Add(time.Minute))
	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestVerify_UnknownIdentity(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{}}
	v := NewVerifier(store)

	tokenStr := mintJWT(t, jwt.SigningMethodEdDSA, priv, "unknown@example.com", time.Now().Add(time.Minute))
	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for unknown identity")
	}
}

func TestVerify_TamperedSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{
		"alice@example.com": {pub},
	}}
	v := NewVerifier(store)

	tokenStr := mintJWT(t, jwt.SigningMethodEdDSA, priv, "alice@example.com", time.Now().Add(time.Minute))
	tampered := tokenStr[:len(tokenStr)-4] + "XXXX"
	_, err := v.Verify(context.Background(), tampered)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestVerify_EmptySubject(t *testing.T) {
	store := &memKeyStore{keys: map[string][]crypto.PublicKey{}}
	v := NewVerifier(store)

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	tokenStr := mintJWT(t, jwt.SigningMethodEdDSA, priv, "", time.Now().Add(time.Minute))
	_, err := v.Verify(context.Background(), tokenStr)
	if err == nil {
		t.Fatal("expected error for empty subject")
	}
}
