// Package certs provides TLS certificate generation for the RDP broker.
package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultKeySize is the RSA key size for generated certificates.
	DefaultKeySize = 2048
	// DefaultValidityDays is the validity period for generated certificates.
	DefaultValidityDays = 365
	// CertFileName is the name of the certificate file.
	CertFileName = "server.crt"
	// KeyFileName is the name of the private key file.
	KeyFileName = "server.key"
)

// cryptoOps defines the interface for cryptographic operations.
// This allows for dependency injection in tests.
type cryptoOps interface {
	GenerateKey(random io.Reader, bits int) (*rsa.PrivateKey, error)
	RandInt(random io.Reader, max *big.Int) (*big.Int, error)
	CreateCertificate(rand io.Reader, template, parent *x509.Certificate, pub, priv any) ([]byte, error)
	MarshalPKCS8PrivateKey(key any) ([]byte, error)
	PEMEncode(out io.Writer, b *pem.Block) error
}

// defaultCryptoOps implements cryptoOps using the standard crypto library.
type defaultCryptoOps struct{}

func (d *defaultCryptoOps) GenerateKey(random io.Reader, bits int) (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(random, bits)
}

func (d *defaultCryptoOps) RandInt(random io.Reader, max *big.Int) (*big.Int, error) {
	return rand.Int(random, max)
}

func (d *defaultCryptoOps) CreateCertificate(randReader io.Reader, template, parent *x509.Certificate, pub, priv any) ([]byte, error) {
	return x509.CreateCertificate(randReader, template, parent, pub, priv)
}

func (d *defaultCryptoOps) MarshalPKCS8PrivateKey(key any) ([]byte, error) {
	return x509.MarshalPKCS8PrivateKey(key)
}

func (d *defaultCryptoOps) PEMEncode(out io.Writer, b *pem.Block) error {
	return pem.Encode(out, b)
}

// Generator handles TLS certificate generation.
type Generator struct {
	certDir string
	crypto  cryptoOps
}

// NewGenerator creates a new certificate generator.
func NewGenerator(certDir string) *Generator {
	return &Generator{
		certDir: certDir,
		crypto:  &defaultCryptoOps{},
	}
}

// EnsureCertificates checks if certificates exist and generates them if not.
// Returns the paths to the certificate and key files.
func (g *Generator) EnsureCertificates() (certPath, keyPath string, err error) {
	certPath = filepath.Join(g.certDir, CertFileName)
	keyPath = filepath.Join(g.certDir, KeyFileName)

	// Check if both files exist
	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}

	// Generate new certificates
	if err := g.Generate(); err != nil {
		return "", "", err
	}

	return certPath, keyPath, nil
}

// Generate creates a new self-signed certificate and private key.
func (g *Generator) Generate() error {
	// Ensure directory exists
	if err := os.MkdirAll(g.certDir, 0700); err != nil {
		return fmt.Errorf("failed to create cert directory: %w", err)
	}

	// Generate private key
	privateKey, err := g.crypto.GenerateKey(rand.Reader, DefaultKeySize)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	// Generate serial number
	serialNumber, err := g.crypto.RandInt(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Create certificate template
	notBefore := time.Now()
	notAfter := notBefore.Add(time.Duration(DefaultValidityDays) * 24 * time.Hour)

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"RDP Broker"},
			CommonName:   "RDP Broker Proxy",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	// Create certificate
	derBytes, err := g.crypto.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	// Write certificate
	certPath := filepath.Join(g.certDir, CertFileName)
	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create cert file: %w", err)
	}
	defer certFile.Close()

	if err := g.crypto.PEMEncode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Write private key
	keyPath := filepath.Join(g.certDir, KeyFileName)
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create key file: %w", err)
	}
	defer keyFile.Close()

	privBytes, err := g.crypto.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := g.crypto.PEMEncode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	return nil
}

// CertPath returns the path to the certificate file.
func (g *Generator) CertPath() string {
	return filepath.Join(g.certDir, CertFileName)
}

// KeyPath returns the path to the private key file.
func (g *Generator) KeyPath() string {
	return filepath.Join(g.certDir, KeyFileName)
}

// fileExists checks if a file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
