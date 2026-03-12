// Package session provides session lifecycle management for the RDP broker.
package session

import (
	"errors"
	"sync"
)

// Errors for port pool operations.
var (
	ErrNoPortsAvailable = errors.New("no ports available in pool")
	ErrPortNotInUse     = errors.New("port is not in use")
	ErrPortOutOfRange   = errors.New("port is out of range")
)

// PortPool manages allocation of port pairs for proxy sessions.
// Each session gets an external port (for the gatekeeper) and an internal port
// (for freerdp-proxy3). The internal port is calculated as external + offset.
//
// Thread-safe: all methods are safe for concurrent use.
type PortPool struct {
	mu             sync.Mutex
	startPort      int
	endPort        int
	internalOffset int
	inUse          map[int]bool // Tracks which external ports are allocated
}

// NewPortPool creates a new port pool with the given range and offset.
// Ports from startPort to endPort (inclusive) will be available for allocation.
// Internal ports are calculated as external port + internalOffset.
func NewPortPool(startPort, endPort, internalOffset int) *PortPool {
	return &PortPool{
		startPort:      startPort,
		endPort:        endPort,
		internalOffset: internalOffset,
		inUse:          make(map[int]bool),
	}
}

// Allocate reserves a port pair and returns the external and internal ports.
// Returns ErrNoPortsAvailable if all ports are in use.
func (p *PortPool) Allocate() (externalPort, internalPort int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Find the first available port
	for port := p.startPort; port <= p.endPort; port++ {
		if !p.inUse[port] {
			p.inUse[port] = true
			return port, port + p.internalOffset, nil
		}
	}

	return 0, 0, ErrNoPortsAvailable
}

// Release returns a port pair to the pool.
// Returns ErrPortNotInUse if the port was not allocated.
// Returns ErrPortOutOfRange if the port is not in the pool's range.
func (p *PortPool) Release(externalPort int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if externalPort < p.startPort || externalPort > p.endPort {
		return ErrPortOutOfRange
	}

	if !p.inUse[externalPort] {
		return ErrPortNotInUse
	}

	delete(p.inUse, externalPort)
	return nil
}

// Available returns the number of ports currently available.
func (p *PortPool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return (p.endPort - p.startPort + 1) - len(p.inUse)
}

// InUse returns the number of ports currently allocated.
func (p *PortPool) InUse() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.inUse)
}

// Total returns the total number of ports in the pool.
func (p *PortPool) Total() int {
	return p.endPort - p.startPort + 1
}

// IsInUse checks if a specific external port is currently allocated.
func (p *PortPool) IsInUse(externalPort int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.inUse[externalPort]
}

// InternalPort calculates the internal port for a given external port.
func (p *PortPool) InternalPort(externalPort int) int {
	return externalPort + p.internalOffset
}

// Range returns the start and end ports of the pool.
func (p *PortPool) Range() (start, end int) {
	return p.startPort, p.endPort
}
