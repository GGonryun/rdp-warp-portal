package certs

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestNewGenerator(t *testing.T) {
	gen := NewGenerator("/tmp/test-certs")
	if gen == nil {
		t.Fatal("NewGenerator returned nil")
	}
	if gen.certDir != "/tmp/test-certs" {
		t.Errorf("expected certDir '/tmp/test-certs', got %q", gen.certDir)
	}
}

func TestGenerate(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Check certificate file exists
	certPath := filepath.Join(tmpDir, CertFileName)
	if !fileExists(certPath) {
		t.Error("certificate file was not created")
	}

	// Check key file exists
	keyPath := filepath.Join(tmpDir, KeyFileName)
	if !fileExists(keyPath) {
		t.Error("key file was not created")
	}

	// Check certificate file permissions
	certInfo, _ := os.Stat(certPath)
	if certInfo.Mode().Perm() != 0644 {
		t.Errorf("expected cert permissions 0644, got %o", certInfo.Mode().Perm())
	}

	// Check key file permissions
	keyInfo, _ := os.Stat(keyPath)
	if keyInfo.Mode().Perm() != 0600 {
		t.Errorf("expected key permissions 0600, got %o", keyInfo.Mode().Perm())
	}
}

func TestGenerate_ValidCertificate(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Read and parse certificate
	certPEM, err := os.ReadFile(filepath.Join(tmpDir, CertFileName))
	if err != nil {
		t.Fatalf("failed to read certificate: %v", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode PEM block")
	}

	if block.Type != "CERTIFICATE" {
		t.Errorf("expected block type 'CERTIFICATE', got %q", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	// Verify certificate properties
	if cert.Subject.CommonName != "RDP Broker Proxy" {
		t.Errorf("expected CN 'RDP Broker Proxy', got %q", cert.Subject.CommonName)
	}

	if len(cert.Subject.Organization) == 0 || cert.Subject.Organization[0] != "RDP Broker" {
		t.Error("expected Organization 'RDP Broker'")
	}

	if len(cert.DNSNames) == 0 || cert.DNSNames[0] != "localhost" {
		t.Error("expected DNSNames to include 'localhost'")
	}

	if cert.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Error("expected KeyUsageKeyEncipherment")
	}

	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("expected KeyUsageDigitalSignature")
	}
}

func TestGenerate_ValidPrivateKey(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Read and parse private key
	keyPEM, err := os.ReadFile(filepath.Join(tmpDir, KeyFileName))
	if err != nil {
		t.Fatalf("failed to read private key: %v", err)
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("failed to decode PEM block")
	}

	if block.Type != "PRIVATE KEY" {
		t.Errorf("expected block type 'PRIVATE KEY', got %q", block.Type)
	}

	_, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}
}

func TestEnsureCertificates_GeneratesIfMissing(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	certPath, keyPath, err := gen.EnsureCertificates()
	if err != nil {
		t.Fatalf("EnsureCertificates failed: %v", err)
	}

	expectedCertPath := filepath.Join(tmpDir, CertFileName)
	expectedKeyPath := filepath.Join(tmpDir, KeyFileName)

	if certPath != expectedCertPath {
		t.Errorf("expected cert path %q, got %q", expectedCertPath, certPath)
	}

	if keyPath != expectedKeyPath {
		t.Errorf("expected key path %q, got %q", expectedKeyPath, keyPath)
	}

	if !fileExists(certPath) {
		t.Error("certificate file was not created")
	}

	if !fileExists(keyPath) {
		t.Error("key file was not created")
	}
}

func TestEnsureCertificates_UsesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	// Create initial certificates
	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Get original contents
	origCert, _ := os.ReadFile(filepath.Join(tmpDir, CertFileName))
	origKey, _ := os.ReadFile(filepath.Join(tmpDir, KeyFileName))

	// Call EnsureCertificates - should use existing
	_, _, err := gen.EnsureCertificates()
	if err != nil {
		t.Fatalf("EnsureCertificates failed: %v", err)
	}

	// Verify files weren't regenerated
	newCert, _ := os.ReadFile(filepath.Join(tmpDir, CertFileName))
	newKey, _ := os.ReadFile(filepath.Join(tmpDir, KeyFileName))

	if string(origCert) != string(newCert) {
		t.Error("certificate was regenerated when it should have been reused")
	}

	if string(origKey) != string(newKey) {
		t.Error("private key was regenerated when it should have been reused")
	}
}

func TestCertPath(t *testing.T) {
	gen := NewGenerator("/etc/certs")
	if gen.CertPath() != "/etc/certs/server.crt" {
		t.Errorf("expected '/etc/certs/server.crt', got %q", gen.CertPath())
	}
}

func TestKeyPath(t *testing.T) {
	gen := NewGenerator("/etc/certs")
	if gen.KeyPath() != "/etc/certs/server.key" {
		t.Errorf("expected '/etc/certs/server.key', got %q", gen.KeyPath())
	}
}

func TestGenerate_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "certs")
	gen := NewGenerator(nestedDir)

	err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if !fileExists(filepath.Join(nestedDir, CertFileName)) {
		t.Error("certificate was not created in nested directory")
	}
}
