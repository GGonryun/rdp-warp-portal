package session

import (
	"errors"
	"sync"
	"testing"
)

func TestNewPortPool(t *testing.T) {
	pool := NewPortPool(33400, 33500, 11000)
	if pool == nil {
		t.Fatal("NewPortPool returned nil")
	}

	if pool.Total() != 101 {
		t.Errorf("expected total 101, got %d", pool.Total())
	}

	if pool.Available() != 101 {
		t.Errorf("expected available 101, got %d", pool.Available())
	}

	if pool.InUse() != 0 {
		t.Errorf("expected in use 0, got %d", pool.InUse())
	}
}

func TestAllocate_Success(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	ext, int_, err := pool.Allocate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext != 33400 {
		t.Errorf("expected external port 33400, got %d", ext)
	}

	if int_ != 44400 {
		t.Errorf("expected internal port 44400, got %d", int_)
	}

	if pool.Available() != 10 {
		t.Errorf("expected available 10, got %d", pool.Available())
	}

	if pool.InUse() != 1 {
		t.Errorf("expected in use 1, got %d", pool.InUse())
	}
}

func TestAllocate_Sequential(t *testing.T) {
	pool := NewPortPool(33400, 33402, 11000)

	// Allocate all three ports
	ext1, _, _ := pool.Allocate()
	ext2, _, _ := pool.Allocate()
	ext3, _, _ := pool.Allocate()

	if ext1 != 33400 || ext2 != 33401 || ext3 != 33402 {
		t.Errorf("expected sequential ports 33400, 33401, 33402; got %d, %d, %d", ext1, ext2, ext3)
	}
}

func TestAllocate_Exhausted(t *testing.T) {
	pool := NewPortPool(33400, 33401, 11000)

	// Allocate both ports
	pool.Allocate()
	pool.Allocate()

	// Third allocation should fail
	_, _, err := pool.Allocate()
	if !errors.Is(err, ErrNoPortsAvailable) {
		t.Errorf("expected ErrNoPortsAvailable, got %v", err)
	}
}

func TestRelease_Success(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	ext, _, _ := pool.Allocate()

	err := pool.Release(ext)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pool.Available() != 11 {
		t.Errorf("expected available 11, got %d", pool.Available())
	}

	if pool.InUse() != 0 {
		t.Errorf("expected in use 0, got %d", pool.InUse())
	}
}

func TestRelease_NotInUse(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	err := pool.Release(33400)
	if !errors.Is(err, ErrPortNotInUse) {
		t.Errorf("expected ErrPortNotInUse, got %v", err)
	}
}

func TestRelease_OutOfRange(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	tests := []int{33399, 33411, 0, -1, 65536}
	for _, port := range tests {
		err := pool.Release(port)
		if !errors.Is(err, ErrPortOutOfRange) {
			t.Errorf("port %d: expected ErrPortOutOfRange, got %v", port, err)
		}
	}
}

func TestAllocateAfterRelease(t *testing.T) {
	pool := NewPortPool(33400, 33401, 11000)

	// Allocate both ports
	ext1, _, _ := pool.Allocate()
	ext2, _, _ := pool.Allocate()

	// Release the first
	pool.Release(ext1)

	// Allocate again - should get the first port back
	ext3, _, _ := pool.Allocate()
	if ext3 != ext1 {
		t.Errorf("expected to get released port %d back, got %d", ext1, ext3)
	}

	// Pool should be full again
	if pool.Available() != 0 {
		t.Errorf("expected available 0, got %d", pool.Available())
	}

	_ = ext2 // suppress unused warning
}

func TestIsInUse(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	if pool.IsInUse(33400) {
		t.Error("port should not be in use initially")
	}

	pool.Allocate()

	if !pool.IsInUse(33400) {
		t.Error("port should be in use after allocation")
	}

	pool.Release(33400)

	if pool.IsInUse(33400) {
		t.Error("port should not be in use after release")
	}
}

func TestInternalPort(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	tests := []struct {
		external int
		expected int
	}{
		{33400, 44400},
		{33405, 44405},
		{33410, 44410},
	}

	for _, tt := range tests {
		got := pool.InternalPort(tt.external)
		if got != tt.expected {
			t.Errorf("InternalPort(%d): got %d, want %d", tt.external, got, tt.expected)
		}
	}
}

func TestRange(t *testing.T) {
	pool := NewPortPool(33400, 33500, 11000)

	start, end := pool.Range()
	if start != 33400 {
		t.Errorf("expected start 33400, got %d", start)
	}
	if end != 33500 {
		t.Errorf("expected end 33500, got %d", end)
	}
}

func TestConcurrentAccess(t *testing.T) {
	pool := NewPortPool(33400, 33499, 11000) // 100 ports

	const numGoroutines = 50
	const numOperations = 100

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*numOperations)
	allocatedPorts := make(chan int, numGoroutines*numOperations)

	// Start multiple goroutines allocating and releasing
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				ext, _, err := pool.Allocate()
				if err == nil {
					allocatedPorts <- ext
				} else if !errors.Is(err, ErrNoPortsAvailable) {
					errChan <- err
				}
			}
		}()
	}

	// Start goroutines that release ports
	go func() {
		for port := range allocatedPorts {
			pool.Release(port)
		}
	}()

	wg.Wait()
	close(allocatedPorts)
	close(errChan)

	// Check for unexpected errors
	for err := range errChan {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConcurrentAllocateAndRelease(t *testing.T) {
	pool := NewPortPool(33400, 33409, 11000) // 10 ports

	const iterations = 1000
	var wg sync.WaitGroup

	// Each goroutine allocates and then releases
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ext, _, err := pool.Allocate()
			if err == nil {
				// Simulate some work
				pool.Release(ext)
			}
		}()
	}

	wg.Wait()

	// All ports should be available again
	// Note: Due to timing, some might still be in use during concurrent operations,
	// but after all goroutines complete, we should have all ports back
	if pool.InUse() != 0 {
		t.Errorf("expected 0 ports in use after all releases, got %d", pool.InUse())
	}
}

func TestSinglePort(t *testing.T) {
	pool := NewPortPool(33400, 33400, 11000) // Single port pool

	if pool.Total() != 1 {
		t.Errorf("expected total 1, got %d", pool.Total())
	}

	ext, int_, err := pool.Allocate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext != 33400 || int_ != 44400 {
		t.Errorf("expected ports 33400/44400, got %d/%d", ext, int_)
	}

	// Second allocation should fail
	_, _, err = pool.Allocate()
	if !errors.Is(err, ErrNoPortsAvailable) {
		t.Errorf("expected ErrNoPortsAvailable, got %v", err)
	}

	// Release and allocate again
	pool.Release(33400)
	ext, _, err = pool.Allocate()
	if err != nil {
		t.Fatalf("unexpected error after release: %v", err)
	}
	if ext != 33400 {
		t.Errorf("expected port 33400, got %d", ext)
	}
}
