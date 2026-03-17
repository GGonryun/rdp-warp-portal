package session

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/p0-security/rdp-broker/internal/credential"
)

// ProxyConfig holds the configuration needed to generate a freerdp-proxy3 INI file.
type ProxyConfig struct {
	// Server settings (proxy listens on this)
	ServerHost string
	ServerPort int

	// Target settings (proxy connects to this)
	TargetHost     string
	TargetPort     int
	TargetUser     string
	TargetPassword string
	TargetDomain   string

	// Certificate paths
	CertificateFile string
	PrivateKeyFile  string
}

// proxyConfigTemplate is the freerdp-proxy3 INI configuration template.
const proxyConfigTemplate = `[Server]
Host = {{.ServerHost}}
Port = {{.ServerPort}}

[Target]
FixedTarget = true
Host = {{.TargetHost}}
Port = {{.TargetPort}}
User = {{.TargetUser}}
Password = {{.TargetPassword}}
Domain = {{.TargetDomain}}

[Channels]
GFX = true
DisplayControl = true
Clipboard = true
AudioInput = true
AudioOutput = true
DeviceRedirection = true
VideoRedirection = true
CameraRedirection = true
RemoteApp = false
PassthroughIsBlacklist = true
Passthrough =
Intercept =

[Input]
Keyboard = true
Mouse = true
Multitouch = true

[Security]
ServerTlsSecurity = true
ServerNlaSecurity = false
ServerRdpSecurity = true
ClientTlsSecurity = true
ClientNlaSecurity = true
ClientRdpSecurity = true
ClientAllowFallbackToTls = true

[Certificates]
CertificateFile = {{.CertificateFile}}
PrivateKeyFile = {{.PrivateKeyFile}}

[Clipboard]
TextOnly = false
MaxTextLength = 0
`

// ConfigWriter generates freerdp-proxy3 configuration files.
type ConfigWriter struct {
	template    *template.Template
	certDir     string
	sessionDir  string
}

// NewConfigWriter creates a new config writer.
func NewConfigWriter(certDir, sessionDir string) (*ConfigWriter, error) {
	tmpl, err := template.New("proxy-config").Parse(proxyConfigTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config template: %w", err)
	}

	return &ConfigWriter{
		template:   tmpl,
		certDir:    certDir,
		sessionDir: sessionDir,
	}, nil
}

// WriteConfig generates and writes a freerdp-proxy3 INI file for a session.
// Returns the path to the written config file.
//
// The config file is written to {sessionDir}/{sessionID}/proxy.ini with mode 0600.
func (w *ConfigWriter) WriteConfig(sessionID string, internalPort int, creds *credential.TargetCredentials) (string, error) {
	// Create session directory
	sessionPath := filepath.Join(w.sessionDir, sessionID)
	if err := os.MkdirAll(sessionPath, 0700); err != nil {
		return "", fmt.Errorf("failed to create session directory: %w", err)
	}

	// Build config
	config := ProxyConfig{
		ServerHost:      "127.0.0.1",
		ServerPort:      internalPort,
		TargetHost:      creds.IP,
		TargetPort:      creds.Port,
		TargetUser:      creds.Username,
		TargetPassword:  creds.Password,
		TargetDomain:    creds.Domain,
		CertificateFile: filepath.Join(w.certDir, "server.crt"),
		PrivateKeyFile:  filepath.Join(w.certDir, "server.key"),
	}

	// Write config file
	configPath := filepath.Join(sessionPath, "proxy.ini")
	f, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	if err := w.template.Execute(f, config); err != nil {
		return "", fmt.Errorf("failed to write config: %w", err)
	}

	return configPath, nil
}

// DeleteConfig removes the config file for a session.
func (w *ConfigWriter) DeleteConfig(sessionID string) error {
	configPath := filepath.Join(w.sessionDir, sessionID, "proxy.ini")
	return os.Remove(configPath)
}

// CleanupSession removes the entire session directory.
func (w *ConfigWriter) CleanupSession(sessionID string) error {
	sessionPath := filepath.Join(w.sessionDir, sessionID)
	return os.RemoveAll(sessionPath)
}

// GenerateConfigBytes generates the INI content without writing to disk.
// This is useful for testing.
func (w *ConfigWriter) GenerateConfigBytes(internalPort int, creds *credential.TargetCredentials) ([]byte, error) {
	config := ProxyConfig{
		ServerHost:      "127.0.0.1",
		ServerPort:      internalPort,
		TargetHost:      creds.IP,
		TargetPort:      creds.Port,
		TargetUser:      creds.Username,
		TargetPassword:  creds.Password,
		TargetDomain:    creds.Domain,
		CertificateFile: filepath.Join(w.certDir, "server.crt"),
		PrivateKeyFile:  filepath.Join(w.certDir, "server.key"),
	}

	var buf []byte
	builder := &stringBuilder{buf: &buf}
	if err := w.template.Execute(builder, config); err != nil {
		return nil, err
	}

	return buf, nil
}

// stringBuilder implements io.Writer for collecting template output.
type stringBuilder struct {
	buf *[]byte
}

func (s *stringBuilder) Write(p []byte) (n int, err error) {
	*s.buf = append(*s.buf, p...)
	return len(p), nil
}
