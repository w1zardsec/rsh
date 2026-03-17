package listener

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Status string

const (
	StatusListening Status = "LISTENING"
	StatusStopped   Status = "STOPPED"
	StatusError     Status = "ERROR"
)

// Shell represents an accepted reverse shell connection.
type Shell struct {
	ID          int
	Conn        net.Conn
	RemoteAddr  string
	ConnectedAt time.Time
}

// Entry represents a single active listener.
type Entry struct {
	ID        int
	Interface string
	Port      string
	Status    Status

	shells   []*Shell
	listener net.Listener
	stopCh   chan struct{}
	mu       sync.RWMutex
}

func (e *Entry) ShellCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.shells)
}

func (e *Entry) Shells() []*Shell {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]*Shell, len(e.shells))
	copy(result, e.shells)
	return result
}

// ShellConnectedMsg is emitted when a new shell connects to a listener.
type ShellConnectedMsg struct {
	ListenerID int
	Shell      *Shell
}

// Manager handles the lifecycle of TCP listeners.
type Manager struct {
	mu        sync.RWMutex
	entries   map[int]*Entry
	nextID    atomic.Int32
	nextShell atomic.Int32
}

func NewManager() *Manager {
	return &Manager{
		entries: make(map[int]*Entry),
	}
}

// Start creates a new TCP listener on the given interface and port.
// onShell is called (in a goroutine) each time a new connection is accepted.
func (m *Manager) Start(iface, port string, onShell func(ShellConnectedMsg)) (int, error) {
	addr := iface + ":" + port
	if iface == "" || iface == "0.0.0.0" {
		addr = "0.0.0.0:" + port
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("listen %s: %w", addr, err)
	}

	id := int(m.nextID.Add(1))
	displayIface := iface
	if displayIface == "" {
		displayIface = "0.0.0.0"
	}

	entry := &Entry{
		ID:        id,
		Interface: displayIface,
		Port:      port,
		Status:    StatusListening,
		shells:    make([]*Shell, 0),
		listener:  ln,
		stopCh:    make(chan struct{}),
	}

	m.mu.Lock()
	m.entries[id] = entry
	m.mu.Unlock()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-entry.stopCh:
					// Normal stop.
					return
				default:
					entry.mu.Lock()
					entry.Status = StatusError
					entry.mu.Unlock()
					return
				}
			}

			shellID := int(m.nextShell.Add(1))
			shell := &Shell{
				ID:          shellID,
				Conn:        conn,
				RemoteAddr:  conn.RemoteAddr().String(),
				ConnectedAt: time.Now(),
			}

			entry.mu.Lock()
			entry.shells = append(entry.shells, shell)
			entry.mu.Unlock()

			if onShell != nil {
				go onShell(ShellConnectedMsg{ListenerID: id, Shell: shell})
			}
		}
	}()

	return id, nil
}

// Stop terminates a listener and closes all its shells.
func (m *Manager) Stop(id int) error {
	m.mu.Lock()
	entry, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("listener #%d not found", id)
	}
	delete(m.entries, id)
	m.mu.Unlock()

	close(entry.stopCh)
	entry.listener.Close()

	entry.mu.Lock()
	entry.Status = StatusStopped
	for _, s := range entry.shells {
		s.Conn.Close()
	}
	entry.mu.Unlock()

	return nil
}

// Entries returns a snapshot of all active listener entries.
func (m *Manager) Entries() []*Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Entry, 0, len(m.entries))
	for _, e := range m.entries {
		result = append(result, e)
	}
	return result
}

// Count returns the number of active listeners.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// TotalShells returns the total number of connected shells across all listeners.
func (m *Manager) TotalShells() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, e := range m.entries {
		total += e.ShellCount()
	}
	return total
}

// RemoveShell closes and removes a single shell from its parent listener.
func (m *Manager) RemoveShell(id int) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		e.mu.Lock()
		for i, s := range e.shells {
			if s.ID == id {
				s.Conn.Close()
				e.shells = append(e.shells[:i], e.shells[i+1:]...)
				e.mu.Unlock()
				return nil
			}
		}
		e.mu.Unlock()
	}
	return fmt.Errorf("shell #%d not found", id)
}

// FindShell returns the Shell with the given ID across all listeners, or nil.
func (m *Manager) FindShell(id int) *Shell {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		e.mu.RLock()
		for _, s := range e.shells {
			if s.ID == id {
				e.mu.RUnlock()
				return s
			}
		}
		e.mu.RUnlock()
	}
	return nil
}
