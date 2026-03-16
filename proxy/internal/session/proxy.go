package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"syscall"
	"time"
)

// Proxy errors.
var (
	ErrProxyNotReady    = errors.New("proxy did not become ready in time")
	ErrProxyStartFailed = errors.New("proxy failed to start")
)

// ProxyManager handles freerdp-proxy3 process management.
type ProxyManager struct {
	binaryPath string
}

// NewProxyManager creates a new proxy manager.
func NewProxyManager(binaryPath string) *ProxyManager {
	return &ProxyManager{
		binaryPath: binaryPath,
	}
}

// StartProxy starts a freerdp-proxy3 process with the given config file.
// The process runs in the background. Use cmd.Wait() to wait for it to exit.
//
// Returns the exec.Cmd for the running process.
func (p *ProxyManager) StartProxy(configPath string, stdout, stderr io.Writer) (*exec.Cmd, error) {
	cmd := exec.Command(p.binaryPath, configPath)

	// Redirect output to provided writers
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}

	// Set process group so we can kill the entire group if needed
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProxyStartFailed, err)
	}

	return cmd, nil
}

// WaitReady polls the proxy's listening port until it accepts connections.
// Returns nil when the proxy is ready, or an error if the timeout is exceeded.
func (p *ProxyManager) WaitReady(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pollInterval := 50 * time.Millisecond

	for time.Now().Before(deadline) {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to connect
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}

		// Wait before next attempt
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return ErrProxyNotReady
}

// StopProxy kills a proxy process immediately.
// Does not wait for the process to exit (monitor goroutine handles that).
func (p *ProxyManager) StopProxy(cmd *exec.Cmd, timeout time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Check if already exited
	if cmd.ProcessState != nil {
		return nil
	}

	// Kill the process immediately
	if err := cmd.Process.Kill(); err != nil {
		// Process might already be dead
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}

	return nil
}

// IsRunning checks if a proxy process is still running.
func (p *ProxyManager) IsRunning(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}

	// If ProcessState is set, the process has exited
	if cmd.ProcessState != nil {
		return false
	}

	// Check if process is still alive by sending signal 0
	err := cmd.Process.Signal(syscall.Signal(0))
	return err == nil
}

// ProxyOutput is a simple io.Writer that prefixes output with a session identifier.
type ProxyOutput struct {
	sessionID string
	writer    io.Writer
	prefix    string
}

// NewProxyOutput creates a new ProxyOutput writer.
func NewProxyOutput(sessionID string, writer io.Writer, prefix string) *ProxyOutput {
	return &ProxyOutput{
		sessionID: sessionID,
		writer:    writer,
		prefix:    prefix,
	}
}

func (p *ProxyOutput) Write(data []byte) (n int, err error) {
	// In a production system, you might want to parse log lines
	// and emit structured log entries. For now, we just prefix.
	if p.writer == nil {
		return len(data), nil
	}

	// Write with prefix for each line
	// This is a simplified version - production code might buffer lines
	_, err = fmt.Fprintf(p.writer, "[%s] %s%s", p.sessionID, p.prefix, string(data))
	return len(data), err
}
