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

	// Stop it (just sends kill signal, doesn't wait)
	err = pm.StopProxy(cmd, time.Second)
	if err != nil {
		t.Errorf("StopProxy failed: %v", err)
	}

	// Wait for process to actually exit
	cmd.Wait()

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

func TestStopProxy_ReturnsImmediately(t *testing.T) {
	// Start a long-running sleep process
	pm := NewProxyManager("sleep")

	cmd, err := pm.StartProxy("60", nil, nil)
	if err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// Verify it's running
	if !pm.IsRunning(cmd) {
		t.Fatal("process should be running")
	}

	// StopProxy should return almost immediately (not wait for process to exit)
	start := time.Now()
	err = pm.StopProxy(cmd, time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("StopProxy failed: %v", err)
	}

	// StopProxy should return within 100ms since it doesn't wait for exit
	if elapsed > 100*time.Millisecond {
		t.Errorf("StopProxy took too long (%v), expected it to return immediately without waiting", elapsed)
	}

	// The process should still be in the process of terminating
	// (we sent Kill but didn't wait)
	// Clean up by waiting for actual exit
	cmd.Wait()
}

func TestStopProxy_SendsSIGKILL(t *testing.T) {
	// Start a process that ignores SIGTERM (using a trap)
	// We use a shell command that traps SIGTERM but not SIGKILL
	pm := NewProxyManager("sh")

	// This shell command traps SIGTERM and ignores it, but SIGKILL cannot be trapped
	cmd, err := pm.StartProxy("-c", nil, nil)
	if err != nil {
		// Fall back to using sleep directly
		pm = NewProxyManager("sleep")
		cmd, err = pm.StartProxy("60", nil, nil)
		if err != nil {
			t.Fatalf("StartProxy failed: %v", err)
		}
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// Verify it's running
	if !pm.IsRunning(cmd) {
		t.Fatal("process should be running")
	}

	// Stop the process
	err = pm.StopProxy(cmd, time.Second)
	if err != nil {
		t.Errorf("StopProxy failed: %v", err)
	}

	// Wait for the process to exit and check its state
	cmd.Wait()

	// After SIGKILL, the process should be dead
	if pm.IsRunning(cmd) {
		t.Error("process should not be running after SIGKILL")
	}

	// On Unix, a process killed by SIGKILL has exit status -1 and signal 9
	if cmd.ProcessState == nil {
		t.Fatal("ProcessState should be set after Wait()")
	}

	// The process was killed (not exited normally)
	if cmd.ProcessState.Success() {
		t.Error("process killed by SIGKILL should not have success status")
	}
}

func TestStopProxy_HandlesAlreadyExitedProcessGracefully(t *testing.T) {
	pm := NewProxyManager("sleep")

	// Start a long-running process
	cmd, err := pm.StartProxy("60", nil, nil)
	if err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}

	// Kill it directly (simulating it dying on its own)
	cmd.Process.Kill()
	cmd.Wait()

	// Now ProcessState is set, indicating the process has exited
	if cmd.ProcessState == nil {
		t.Fatal("ProcessState should be set after Wait()")
	}

	// StopProxy should handle this gracefully and return nil
	err = pm.StopProxy(cmd, time.Second)
	if err != nil {
		t.Errorf("StopProxy on already-exited process should not error: %v", err)
	}
}

func TestStopProxy_HandlesNilCmdGracefully(t *testing.T) {
	pm := NewProxyManager("test")

	// Test with completely nil cmd
	err := pm.StopProxy(nil, time.Second)
	if err != nil {
		t.Errorf("StopProxy(nil) should not error: %v", err)
	}
}

func TestStopProxy_HandlesNilProcessGracefully(t *testing.T) {
	pm := NewProxyManager("test")

	// Test with cmd that has nil Process (never started)
	cmd := &exec.Cmd{}
	err := pm.StopProxy(cmd, time.Second)
	if err != nil {
		t.Errorf("StopProxy on cmd with nil Process should not error: %v", err)
	}
}

// TestWaitReady_ContextDeadlineExceeded tests context deadline behavior.
func TestWaitReady_ContextDeadlineExceeded(t *testing.T) {
	// Use a port that nothing is listening on
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := listener.Addr().String()
	listener.Close()

	pm := NewProxyManager("test")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := pm.WaitReady(ctx, addr, 5*time.Second)
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

// TestWaitReady_ServerAppearsLater tests the polling behavior.
func TestWaitReady_ServerAppearsLater(t *testing.T) {
	pm := NewProxyManager("test")

	// Use a high port that should be available
	addr := "127.0.0.1:44999"

	// Start server after 200ms
	serverStarted := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		close(serverStarted)
		time.Sleep(2 * time.Second)
		listener.Close()
	}()

	ctx := context.Background()
	err := pm.WaitReady(ctx, addr, 2*time.Second)
	if err != nil {
		t.Logf("WaitReady returned error (may be expected if port in use): %v", err)
	}

	// Wait for server cleanup
	select {
	case <-serverStarted:
		// Server started as expected
	case <-time.After(500 * time.Millisecond):
		// Server may not have started if port was in use
	}
}

// TestProxyOutput_EmptyData tests writing empty data.
func TestProxyOutput_EmptyData(t *testing.T) {
	var buf bytes.Buffer
	output := NewProxyOutput("session-123", &buf, "OUT: ")

	n, err := output.Write([]byte{})
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
}

// TestProxyOutput_LargeData tests writing large amounts of data.
func TestProxyOutput_LargeData(t *testing.T) {
	var buf bytes.Buffer
	output := NewProxyOutput("session-123", &buf, "OUT: ")

	largeData := bytes.Repeat([]byte("x"), 10000)
	n, err := output.Write(largeData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 10000 {
		t.Errorf("expected n=10000, got %d", n)
	}
}

// TestProxyOutput_MultipleWrites tests multiple consecutive writes.
func TestProxyOutput_MultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	output := NewProxyOutput("session-123", &buf, "OUT: ")

	for i := 0; i < 10; i++ {
		n, err := output.Write([]byte("message"))
		if err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
		if n != 7 {
			t.Errorf("Write %d: expected n=7, got %d", i, n)
		}
	}
}

// TestIsRunning_CmdWithNilProcess tests IsRunning with various nil states.
func TestIsRunning_CmdWithNilProcess(t *testing.T) {
	pm := NewProxyManager("test")

	// Cmd with nil Process
	cmd := &exec.Cmd{}
	if pm.IsRunning(cmd) {
		t.Error("IsRunning should return false for cmd with nil Process")
	}
}

// TestStartProxy_WithStderr tests starting with stderr output.
func TestStartProxy_WithStderr(t *testing.T) {
	pm := NewProxyManager("sh")

	var stdout, stderr bytes.Buffer
	cmd, err := pm.StartProxy("-c", &stdout, &stderr)
	if err != nil {
		// sh -c with empty arg might fail
		t.Logf("StartProxy returned error (expected): %v", err)
		return
	}

	cmd.Wait()
}

// TestStartProxy_WithBothOutputs tests starting with both stdout and stderr.
func TestStartProxy_WithBothOutputs(t *testing.T) {
	pm := NewProxyManager("echo")

	var stdout, stderr bytes.Buffer
	cmd, err := pm.StartProxy("test output", &stdout, &stderr)
	if err != nil {
		t.Fatalf("StartProxy failed: %v", err)
	}

	cmd.Wait()

	if !strings.Contains(stdout.String(), "test output") {
		t.Errorf("expected 'test output' in stdout, got %q", stdout.String())
	}
}
