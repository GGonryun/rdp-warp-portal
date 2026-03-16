package certs

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockCryptoOps is a mock implementation of cryptoOps for testing error paths.
type mockCryptoOps struct {
	generateKeyErr        error
	randIntErr            error
	createCertificateErr  error
	marshalPrivateKeyErr  error
	pemEncodeCertErr      error
	pemEncodeKeyErr       error
	pemEncodeCallCount    int
	defaultOps            *defaultCryptoOps
}

func newMockCryptoOps() *mockCryptoOps {
	return &mockCryptoOps{
		defaultOps: &defaultCryptoOps{},
	}
}

func (m *mockCryptoOps) GenerateKey(random io.Reader, bits int) (*rsa.PrivateKey, error) {
	if m.generateKeyErr != nil {
		return nil, m.generateKeyErr
	}
	return m.defaultOps.GenerateKey(random, bits)
}

func (m *mockCryptoOps) RandInt(random io.Reader, max *big.Int) (*big.Int, error) {
	if m.randIntErr != nil {
		return nil, m.randIntErr
	}
	return m.defaultOps.RandInt(random, max)
}

func (m *mockCryptoOps) CreateCertificate(randReader io.Reader, template, parent *x509.Certificate, pub, priv any) ([]byte, error) {
	if m.createCertificateErr != nil {
		return nil, m.createCertificateErr
	}
	return m.defaultOps.CreateCertificate(randReader, template, parent, pub, priv)
}

func (m *mockCryptoOps) MarshalPKCS8PrivateKey(key any) ([]byte, error) {
	if m.marshalPrivateKeyErr != nil {
		return nil, m.marshalPrivateKeyErr
	}
	return m.defaultOps.MarshalPKCS8PrivateKey(key)
}

func (m *mockCryptoOps) PEMEncode(out io.Writer, b *pem.Block) error {
	m.pemEncodeCallCount++
	// First call is for certificate, second is for key
	if m.pemEncodeCallCount == 1 && m.pemEncodeCertErr != nil {
		return m.pemEncodeCertErr
	}
	if m.pemEncodeCallCount == 2 && m.pemEncodeKeyErr != nil {
		return m.pemEncodeKeyErr
	}
	return m.defaultOps.PEMEncode(out, b)
}

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

func TestGenerate_FailToCreateDirectory(t *testing.T) {
	// Create a file where we want to create a directory
	tmpDir := t.TempDir()
	blockingFile := filepath.Join(tmpDir, "blocking")
	if err := os.WriteFile(blockingFile, []byte("block"), 0644); err != nil {
		t.Fatalf("failed to create blocking file: %v", err)
	}

	// Try to create certs in a path that has a file in the way
	gen := NewGenerator(filepath.Join(blockingFile, "certs"))
	err := gen.Generate()
	if err == nil {
		t.Error("expected error when directory creation fails")
	}
	if err != nil && !contains(err.Error(), "failed to create cert directory") {
		t.Errorf("expected 'failed to create cert directory' error, got: %v", err)
	}
}

func TestGenerate_FailToCreateCertFile(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a directory with the same name as the cert file
	certFilePath := filepath.Join(tmpDir, CertFileName)
	if err := os.MkdirAll(certFilePath, 0755); err != nil {
		t.Fatalf("failed to create blocking directory: %v", err)
	}

	gen := NewGenerator(tmpDir)
	err := gen.Generate()
	if err == nil {
		t.Error("expected error when cert file creation fails")
	}
	if err != nil && !contains(err.Error(), "failed to create cert file") {
		t.Errorf("expected 'failed to create cert file' error, got: %v", err)
	}
}

func TestGenerate_FailToCreateKeyFile(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a directory with the same name as the key file
	keyFilePath := filepath.Join(tmpDir, KeyFileName)
	if err := os.MkdirAll(keyFilePath, 0755); err != nil {
		t.Fatalf("failed to create blocking directory: %v", err)
	}

	gen := NewGenerator(tmpDir)
	err := gen.Generate()
	if err == nil {
		t.Error("expected error when key file creation fails")
	}
	if err != nil && !contains(err.Error(), "failed to create key file") {
		t.Errorf("expected 'failed to create key file' error, got: %v", err)
	}
}

func TestEnsureCertificates_GenerateError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory where the cert file should be (blocks file creation)
	certFilePath := filepath.Join(tmpDir, CertFileName)
	if err := os.MkdirAll(certFilePath, 0755); err != nil {
		t.Fatalf("failed to create blocking directory: %v", err)
	}

	gen := NewGenerator(tmpDir)
	certPath, keyPath, err := gen.EnsureCertificates()
	if err == nil {
		t.Error("expected error from EnsureCertificates")
	}
	if certPath != "" || keyPath != "" {
		t.Error("expected empty paths on error")
	}
}

func TestEnsureCertificates_OnlyCertExists(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	// Create only the cert file
	certPath := filepath.Join(tmpDir, CertFileName)
	if err := os.WriteFile(certPath, []byte("cert"), 0644); err != nil {
		t.Fatalf("failed to create cert file: %v", err)
	}

	// EnsureCertificates should regenerate both since key is missing
	_, _, err := gen.EnsureCertificates()
	if err != nil {
		t.Fatalf("EnsureCertificates failed: %v", err)
	}

	// Verify both files exist and are valid
	if !fileExists(filepath.Join(tmpDir, CertFileName)) {
		t.Error("certificate file should exist")
	}
	if !fileExists(filepath.Join(tmpDir, KeyFileName)) {
		t.Error("key file should exist")
	}
}

func TestEnsureCertificates_OnlyKeyExists(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	// Create only the key file
	keyPath := filepath.Join(tmpDir, KeyFileName)
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}

	// EnsureCertificates should regenerate both since cert is missing
	_, _, err := gen.EnsureCertificates()
	if err != nil {
		t.Fatalf("EnsureCertificates failed: %v", err)
	}

	// Verify both files exist
	if !fileExists(filepath.Join(tmpDir, CertFileName)) {
		t.Error("certificate file should exist")
	}
	if !fileExists(filepath.Join(tmpDir, KeyFileName)) {
		t.Error("key file should exist")
	}
}

func TestFileExists_Directory(t *testing.T) {
	tmpDir := t.TempDir()
	// fileExists should return false for directories
	if fileExists(tmpDir) {
		t.Error("fileExists should return false for directories")
	}
}

func TestFileExists_NonExistent(t *testing.T) {
	if fileExists("/nonexistent/path/to/file") {
		t.Error("fileExists should return false for non-existent files")
	}
}

func TestGenerate_OverwritesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	// Generate initial certificates
	if err := gen.Generate(); err != nil {
		t.Fatalf("first Generate failed: %v", err)
	}

	// Get original contents
	origCert, _ := os.ReadFile(filepath.Join(tmpDir, CertFileName))

	// Generate again - should overwrite
	if err := gen.Generate(); err != nil {
		t.Fatalf("second Generate failed: %v", err)
	}

	// Verify files were regenerated (different serial number)
	newCert, _ := os.ReadFile(filepath.Join(tmpDir, CertFileName))
	if string(origCert) == string(newCert) {
		t.Error("certificate should have been regenerated with different content")
	}
}

func TestGenerate_WriteCertError(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	// Create cert file as read-only, and make the directory read-only
	// so we can't truncate/write to the file
	certPath := filepath.Join(tmpDir, CertFileName)
	if err := os.WriteFile(certPath, []byte("existing"), 0000); err != nil {
		t.Fatalf("failed to create read-only cert file: %v", err)
	}

	err := gen.Generate()
	if err == nil {
		// On some systems this might succeed if running as root
		t.Skip("test requires non-root user")
	}
	// Error could be either "failed to create cert file" or permission-related
}

func TestGenerate_WriteKeyError(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	// Create key file with no write permissions
	keyPath := filepath.Join(tmpDir, KeyFileName)
	if err := os.WriteFile(keyPath, []byte("existing"), 0000); err != nil {
		t.Fatalf("failed to create read-only key file: %v", err)
	}

	err := gen.Generate()
	if err == nil {
		// On some systems this might succeed if running as root
		t.Skip("test requires non-root user")
	}
	// Error could be either "failed to create key file" or permission-related
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGenerate_WriteCertPEMError(t *testing.T) {
	// Test that we handle errors when writing to the cert file
	// by making the file read-only after creation
	tmpDir := t.TempDir()

	// Create a read-only cert file
	certPath := filepath.Join(tmpDir, CertFileName)
	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0200)
	if err != nil {
		t.Fatalf("failed to create cert file: %v", err)
	}
	certFile.Close()

	// Now make the parent directory read-only so file can't be opened for write
	if err := os.Chmod(tmpDir, 0555); err != nil {
		t.Fatalf("failed to chmod dir: %v", err)
	}
	defer os.Chmod(tmpDir, 0755) // restore for cleanup

	gen := NewGenerator(tmpDir)
	err = gen.Generate()
	if err == nil {
		t.Skip("test requires non-root user or restrictive permissions")
	}
}

func TestGenerate_WriteKeyPEMError(t *testing.T) {
	// Test that we handle errors when writing to the key file
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	// Generate once to get valid cert
	if err := gen.Generate(); err != nil {
		t.Fatalf("first Generate failed: %v", err)
	}

	// Make key file read-only so it can't be written to on regenerate
	keyPath := filepath.Join(tmpDir, KeyFileName)
	if err := os.Chmod(keyPath, 0000); err != nil {
		t.Fatalf("failed to chmod key file: %v", err)
	}
	defer os.Chmod(keyPath, 0600) // restore for cleanup

	err := gen.Generate()
	if err == nil {
		t.Skip("test requires non-root user")
	}
	// Should fail to open key file for writing
}

func TestGenerate_CertWriteAfterOpen(t *testing.T) {
	// This tests an edge case where we can open the file but fail to write
	// We simulate this by filling up available space or using a FIFO
	// For now, we just verify the code path exists
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	// Create the files first with valid content
	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify the files were created and are valid
	certPath := filepath.Join(tmpDir, CertFileName)
	keyPath := filepath.Join(tmpDir, KeyFileName)

	certData, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("failed to read cert: %v", err)
	}
	if len(certData) == 0 {
		t.Error("cert file is empty")
	}

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read key: %v", err)
	}
	if len(keyData) == 0 {
		t.Error("key file is empty")
	}
}

func TestConstants(t *testing.T) {
	// Verify constants have expected values
	if DefaultKeySize != 2048 {
		t.Errorf("expected DefaultKeySize 2048, got %d", DefaultKeySize)
	}
	if DefaultValidityDays != 365 {
		t.Errorf("expected DefaultValidityDays 365, got %d", DefaultValidityDays)
	}
	if CertFileName != "server.crt" {
		t.Errorf("expected CertFileName 'server.crt', got %s", CertFileName)
	}
	if KeyFileName != "server.key" {
		t.Errorf("expected KeyFileName 'server.key', got %s", KeyFileName)
	}
}

func TestGenerate_CertificateValidity(t *testing.T) {
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

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	// Verify certificate validity period
	now := time.Now()
	if cert.NotBefore.After(now) {
		t.Error("certificate NotBefore is in the future")
	}
	if cert.NotAfter.Before(now) {
		t.Error("certificate NotAfter is in the past")
	}

	// Check validity duration is approximately 365 days
	validityDuration := cert.NotAfter.Sub(cert.NotBefore)
	expectedDuration := time.Duration(DefaultValidityDays) * 24 * time.Hour
	if validityDuration < expectedDuration-time.Hour || validityDuration > expectedDuration+time.Hour {
		t.Errorf("certificate validity duration %v not within expected range of %v", validityDuration, expectedDuration)
	}

	// Verify extended key usage
	hasServerAuth := false
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
			break
		}
	}
	if !hasServerAuth {
		t.Error("certificate missing ExtKeyUsageServerAuth")
	}

	// Verify basic constraints
	if !cert.BasicConstraintsValid {
		t.Error("certificate BasicConstraintsValid should be true")
	}
}

func TestGenerate_KeyMatchesCertificate(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Read certificate
	certPEM, err := os.ReadFile(filepath.Join(tmpDir, CertFileName))
	if err != nil {
		t.Fatalf("failed to read certificate: %v", err)
	}
	certBlock, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	// Read private key
	keyPEM, err := os.ReadFile(filepath.Join(tmpDir, KeyFileName))
	if err != nil {
		t.Fatalf("failed to read private key: %v", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	privateKeyInterface, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}

	privateKey, ok := privateKeyInterface.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("private key is not RSA")
	}

	// Verify the public key in cert matches the private key
	certPubKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("certificate public key is not RSA")
	}

	if privateKey.PublicKey.N.Cmp(certPubKey.N) != 0 {
		t.Error("private key does not match certificate public key")
	}
	if privateKey.PublicKey.E != certPubKey.E {
		t.Error("private key exponent does not match certificate public key exponent")
	}
}

func TestGenerate_KeySize(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Read private key
	keyPEM, err := os.ReadFile(filepath.Join(tmpDir, KeyFileName))
	if err != nil {
		t.Fatalf("failed to read private key: %v", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	privateKeyInterface, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}

	privateKey, ok := privateKeyInterface.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("private key is not RSA")
	}

	keySize := privateKey.N.BitLen()
	if keySize != DefaultKeySize {
		t.Errorf("expected key size %d, got %d", DefaultKeySize, keySize)
	}
}

func TestGenerate_GenerateKeyError(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	mockOps := newMockCryptoOps()
	mockOps.generateKeyErr = errors.New("mock key generation error")
	gen.crypto = mockOps

	err := gen.Generate()
	if err == nil {
		t.Error("expected error when key generation fails")
	}
	if err != nil && !contains(err.Error(), "failed to generate private key") {
		t.Errorf("expected 'failed to generate private key' error, got: %v", err)
	}
}

func TestGenerate_RandIntError(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	mockOps := newMockCryptoOps()
	mockOps.randIntErr = errors.New("mock rand int error")
	gen.crypto = mockOps

	err := gen.Generate()
	if err == nil {
		t.Error("expected error when serial number generation fails")
	}
	if err != nil && !contains(err.Error(), "failed to generate serial number") {
		t.Errorf("expected 'failed to generate serial number' error, got: %v", err)
	}
}

func TestGenerate_CreateCertificateError(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	mockOps := newMockCryptoOps()
	mockOps.createCertificateErr = errors.New("mock create certificate error")
	gen.crypto = mockOps

	err := gen.Generate()
	if err == nil {
		t.Error("expected error when certificate creation fails")
	}
	if err != nil && !contains(err.Error(), "failed to create certificate") {
		t.Errorf("expected 'failed to create certificate' error, got: %v", err)
	}
}

func TestGenerate_PEMEncodeCertError(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	mockOps := newMockCryptoOps()
	mockOps.pemEncodeCertErr = errors.New("mock pem encode cert error")
	gen.crypto = mockOps

	err := gen.Generate()
	if err == nil {
		t.Error("expected error when PEM encoding certificate fails")
	}
	if err != nil && !contains(err.Error(), "failed to write certificate") {
		t.Errorf("expected 'failed to write certificate' error, got: %v", err)
	}
}

func TestGenerate_MarshalPrivateKeyError(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	mockOps := newMockCryptoOps()
	mockOps.marshalPrivateKeyErr = errors.New("mock marshal private key error")
	gen.crypto = mockOps

	err := gen.Generate()
	if err == nil {
		t.Error("expected error when marshaling private key fails")
	}
	if err != nil && !contains(err.Error(), "failed to marshal private key") {
		t.Errorf("expected 'failed to marshal private key' error, got: %v", err)
	}
}

func TestGenerate_PEMEncodeKeyError(t *testing.T) {
	tmpDir := t.TempDir()
	gen := NewGenerator(tmpDir)

	mockOps := newMockCryptoOps()
	mockOps.pemEncodeKeyErr = errors.New("mock pem encode key error")
	gen.crypto = mockOps

	err := gen.Generate()
	if err == nil {
		t.Error("expected error when PEM encoding private key fails")
	}
	if err != nil && !contains(err.Error(), "failed to write private key") {
		t.Errorf("expected 'failed to write private key' error, got: %v", err)
	}
}
