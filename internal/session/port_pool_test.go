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

// TestPortPool_LargeOffset tests port pool with large internal offset.
func TestPortPool_LargeOffset(t *testing.T) {
	pool := NewPortPool(33400, 33410, 30000) // Large offset

	ext, int_, err := pool.Allocate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext != 33400 {
		t.Errorf("expected external port 33400, got %d", ext)
	}

	if int_ != 63400 {
		t.Errorf("expected internal port 63400, got %d", int_)
	}
}

// TestPortPool_NegativeOffset tests port pool with negative internal offset.
func TestPortPool_NegativeOffset(t *testing.T) {
	pool := NewPortPool(33400, 33410, -10000)

	ext, int_, err := pool.Allocate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext != 33400 {
		t.Errorf("expected external port 33400, got %d", ext)
	}

	if int_ != 23400 {
		t.Errorf("expected internal port 23400, got %d", int_)
	}
}

// TestPortPool_ZeroOffset tests port pool with zero offset.
func TestPortPool_ZeroOffset(t *testing.T) {
	pool := NewPortPool(33400, 33410, 0)

	ext, int_, err := pool.Allocate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext != int_ {
		t.Errorf("with zero offset, external and internal should be equal: %d != %d", ext, int_)
	}
}

// TestPortPool_AllocateAllAndRelease tests allocating all ports then releasing.
func TestPortPool_AllocateAllAndRelease(t *testing.T) {
	pool := NewPortPool(33400, 33404, 11000) // 5 ports

	allocated := make([]int, 0, 5)

	// Allocate all ports
	for i := 0; i < 5; i++ {
		ext, _, err := pool.Allocate()
		if err != nil {
			t.Fatalf("allocation %d failed: %v", i, err)
		}
		allocated = append(allocated, ext)
	}

	if pool.Available() != 0 {
		t.Errorf("expected 0 available, got %d", pool.Available())
	}

	// Next allocation should fail
	_, _, err := pool.Allocate()
	if !errors.Is(err, ErrNoPortsAvailable) {
		t.Errorf("expected ErrNoPortsAvailable, got %v", err)
	}

	// Release all in reverse order
	for i := len(allocated) - 1; i >= 0; i-- {
		err := pool.Release(allocated[i])
		if err != nil {
			t.Errorf("release failed: %v", err)
		}
	}

	if pool.Available() != 5 {
		t.Errorf("expected 5 available after release, got %d", pool.Available())
	}
}

// TestPortPool_DoubleRelease tests that releasing the same port twice returns an error.
func TestPortPool_DoubleRelease(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	ext, _, _ := pool.Allocate()

	// First release should succeed
	err := pool.Release(ext)
	if err != nil {
		t.Fatalf("first release failed: %v", err)
	}

	// Second release should fail
	err = pool.Release(ext)
	if !errors.Is(err, ErrPortNotInUse) {
		t.Errorf("expected ErrPortNotInUse on double release, got %v", err)
	}
}

// TestPortPool_ReleaseMultipleTimes tests releasing multiple ports.
func TestPortPool_ReleaseMultipleTimes(t *testing.T) {
	pool := NewPortPool(33400, 33402, 11000) // 3 ports

	// Allocate all
	p1, _, _ := pool.Allocate()
	p2, _, _ := pool.Allocate()
	p3, _, _ := pool.Allocate()

	// Release in different order
	pool.Release(p2)
	pool.Release(p1)
	pool.Release(p3)

	// All should be available
	if pool.Available() != 3 {
		t.Errorf("expected 3 available, got %d", pool.Available())
	}
}

// TestPortPool_IsInUse_OutOfRange tests IsInUse with out of range ports.
func TestPortPool_IsInUse_OutOfRange(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	// Out of range ports should not be "in use"
	outOfRangePorts := []int{33399, 33411, 0, -1, 65536}

	for _, port := range outOfRangePorts {
		if pool.IsInUse(port) {
			t.Errorf("out of range port %d should not be in use", port)
		}
	}
}

// TestPortPool_ConcurrentAllocateReleaseSamePort tests race conditions.
func TestPortPool_ConcurrentAllocateReleaseSamePort(t *testing.T) {
	pool := NewPortPool(33400, 33400, 11000) // Single port

	const iterations = 1000
	var wg sync.WaitGroup

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ext, _, err := pool.Allocate()
			if err == nil {
				// Small delay to increase chance of race
				pool.Release(ext)
			}
		}()
	}

	wg.Wait()

	// Should have exactly 1 port available
	if pool.Available() != 1 {
		t.Errorf("expected 1 available after concurrent operations, got %d", pool.Available())
	}
}

// TestPortPool_InternalPortOutOfRange tests InternalPort calculation for out of range ports.
func TestPortPool_InternalPortOutOfRange(t *testing.T) {
	pool := NewPortPool(33400, 33410, 11000)

	// InternalPort doesn't validate range - it just calculates
	tests := []struct {
		external int
		expected int
	}{
		{33399, 44399},  // Below range
		{33411, 44411},  // Above range
		{0, 11000},      // Zero
		{-100, 10900},   // Negative
	}

	for _, tt := range tests {
		got := pool.InternalPort(tt.external)
		if got != tt.expected {
			t.Errorf("InternalPort(%d) = %d, want %d", tt.external, got, tt.expected)
		}
	}
}

// TestPortPool_StressTest runs a stress test with many operations.
func TestPortPool_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	pool := NewPortPool(33400, 33499, 11000) // 100 ports

	const numGoroutines = 100
	const operationsPerGoroutine = 1000

	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				ext, _, err := pool.Allocate()
				if err == nil {
					// Do some operations while holding the port
					pool.IsInUse(ext)
					pool.InternalPort(ext)
					pool.Available()
					pool.InUse()
					pool.Release(ext)
				}
			}
		}()
	}

	wg.Wait()

	// All ports should be available
	if pool.InUse() != 0 {
		t.Errorf("expected 0 in use after stress test, got %d", pool.InUse())
	}

	if pool.Available() != 100 {
		t.Errorf("expected 100 available after stress test, got %d", pool.Available())
	}
}

// TestPortPool_AllocateBoundaryPorts tests allocation at pool boundaries.
func TestPortPool_AllocateBoundaryPorts(t *testing.T) {
	pool := NewPortPool(33400, 33402, 11000) // 3 ports

	// Allocate first port
	p1, _, _ := pool.Allocate()
	if p1 != 33400 {
		t.Errorf("first port should be 33400, got %d", p1)
	}

	// Allocate middle port
	p2, _, _ := pool.Allocate()
	if p2 != 33401 {
		t.Errorf("second port should be 33401, got %d", p2)
	}

	// Allocate last port
	p3, _, _ := pool.Allocate()
	if p3 != 33402 {
		t.Errorf("third port should be 33402, got %d", p3)
	}

	// Release middle port
	pool.Release(33401)

	// Next allocation should get the middle port
	p4, _, _ := pool.Allocate()
	if p4 != 33401 {
		t.Errorf("fourth port should be 33401 (reused), got %d", p4)
	}
}

// TestPortPool_Total_Consistency tests Total() consistency.
func TestPortPool_Total_Consistency(t *testing.T) {
	tests := []struct {
		start    int
		end      int
		expected int
	}{
		{33400, 33400, 1},
		{33400, 33401, 2},
		{33400, 33499, 100},
		{1, 65535, 65535},
	}

	for _, tt := range tests {
		pool := NewPortPool(tt.start, tt.end, 11000)
		if pool.Total() != tt.expected {
			t.Errorf("Total() for range %d-%d: got %d, want %d", tt.start, tt.end, pool.Total(), tt.expected)
		}
	}
}
