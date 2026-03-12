package session

import (
	"bytes"
	"context"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestNewProxyManager(t *testing.T) {
	pm := NewProxyManager("freerdp-proxy3")
	if pm == nil {
		t.Fatal("NewProxyManager returned nil")
	}
	if pm.binaryPath != "freerdp-proxy3" {
		t.Errorf("expected binaryPath 'freerdp-proxy3', got %q", pm.binaryPath)
	}
}

func TestWaitReady_Success(t *testing.T) {
	// Start a mock TCP server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}
	defer listener.Close()

	pm := NewProxyManager("test")
	ctx := context.Background()

	err = pm.WaitReady(ctx, listener.Addr().String(), time.Second)
	if err != nil {
		t.Errorf("WaitReady failed: %v", err)
	}
}

func TestWaitReady_Timeout(t *testing.T) {
	// Use a port that nothing is listening on
	// Find an available port first, then close the listener
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := listener.Addr().String()
	listener.Close()

	pm := NewProxyManager("test")
	ctx := context.Background()

	err := pm.WaitReady(ctx, addr, 200*time.Millisecond)
	if err != ErrProxyNotReady {
		t.Errorf("expected ErrProxyNotReady, got %v", err)
	}
}

func TestWaitReady_ContextCanceled(t *testing.T) {
	// Use a port that nothing is listening on
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := listener.Addr().String()
	listener.Close()

	pm := NewProxyManager("test")
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	err := pm.WaitReady(ctx, addr, 5*time.Second)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestWaitReady_DelayedStart(t *testing.T) {
	// Create a listener that we'll start accepting after a delay
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}

	// Close it immediately to simulate "not ready" state
	addr := listener.Addr().String()
	listener.Close()

	// Start the server after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		l, _ := net.Listen("tcp", addr)
		if l != nil {
			defer l.Close()
			time.Sleep(time.Second) // Keep it running
		}
	}()

	pm := NewProxyManager("test")
	ctx := context.Background()

	err = pm.WaitReady(ctx, addr, 2*time.Second)
	if err != nil {
		t.Errorf("WaitReady failed for delayed start: %v", err)
	}
}

func TestStartProxy_InvalidBinary(t *testing.T) {
	pm := NewProxyManager("/nonexistent/binary")

	_, err := pm.StartProxy("/tmp/config.ini", nil, nil)
	if err == nil {
		t.Error("expected error for nonexistent binary")
	}
}

func TestStartProxy_WithEcho(t *testing.T) {
	// Use 'echo' as a test binary - it exists on most systems
	pm := NewProxyManager("echo")

	var stdout bytes.Buffer
	cmd, err := pm.StartProxy("hello", &stdout, nil)
	if err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}

	// Wait for completion
	err = cmd.Wait()
	if err != nil {
		t.Errorf("cmd.Wait failed: %v", err)
	}

	if !strings.Contains(stdout.String(), "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", stdout.String())
	}
}

func TestStopProxy_NilCmd(t *testing.T) {
	pm := NewProxyManager("test")

	err := pm.StopProxy(nil, time.Second)
	if err != nil {
		t.Errorf("StopProxy(nil) should not error: %v", err)
	}
}

func TestStopProxy_GracefulShutdown(t *testing.T) {
	// Start a sleep process that we can terminate
	pm := NewProxyManager("sleep")

	cmd, err := pm.StartProxy("10", nil, nil)
	if err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}

	// Verify it's running
	if !pm.IsRunning(cmd) {
		t.Fatal("process should be running")
	}

	// Stop it
	err = pm.StopProxy(cmd, time.Second)
	if err != nil {
		t.Errorf("StopProxy failed: %v", err)
	}

	// Verify it's stopped
	if pm.IsRunning(cmd) {
		t.Error("process should not be running after stop")
	}
}

func TestStopProxy_AlreadyExited(t *testing.T) {
	// Start a process that exits immediately
	pm := NewProxyManager("true") // 'true' exits immediately with code 0

	cmd, err := pm.StartProxy("", nil, nil)
	if err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}

	// Wait for it to exit naturally
	cmd.Wait()

	// Now stop should be a no-op
	err = pm.StopProxy(cmd, time.Second)
	if err != nil {
		t.Errorf("StopProxy on exited process should not error: %v", err)
	}
}

func TestIsRunning(t *testing.T) {
	pm := NewProxyManager("sleep")

	t.Run("nil cmd", func(t *testing.T) {
		if pm.IsRunning(nil) {
			t.Error("IsRunning(nil) should return false")
		}
	})

	t.Run("running process", func(t *testing.T) {
		cmd, err := pm.StartProxy("10", nil, nil)
		if err != nil {
			t.Fatalf("StartProxy failed: %v", err)
		}
		defer pm.StopProxy(cmd, time.Second)

		if !pm.IsRunning(cmd) {
			t.Error("IsRunning should return true for running process")
		}
	})

	t.Run("exited process", func(t *testing.T) {
		cmd := exec.Command("true")
		cmd.Start()
		cmd.Wait()

		if pm.IsRunning(cmd) {
			t.Error("IsRunning should return false for exited process")
		}
	})
}

func TestProxyOutput(t *testing.T) {
	var buf bytes.Buffer
	output := NewProxyOutput("session-123", &buf, "OUT: ")

	n, err := output.Write([]byte("test message"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 12 { // len("test message")
		t.Errorf("expected n=12, got %d", n)
	}

	result := buf.String()
	if !strings.Contains(result, "[session-123]") {
		t.Errorf("expected session ID in output, got %q", result)
	}
	if !strings.Contains(result, "OUT: ") {
		t.Errorf("expected prefix in output, got %q", result)
	}
	if !strings.Contains(result, "test message") {
		t.Errorf("expected message in output, got %q", result)
	}
}

func TestProxyOutput_NilWriter(t *testing.T) {
	output := NewProxyOutput("session-123", nil, "")

	n, err := output.Write([]byte("test"))
	if err != nil {
		t.Errorf("Write to nil writer should not error: %v", err)
	}
	if n != 4 {
		t.Errorf("expected n=4, got %d", n)
	}
}
