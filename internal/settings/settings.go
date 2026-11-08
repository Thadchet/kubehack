// Package settings stores user preferences that outlive one launch.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// DefaultPath is where the desktop app keeps its settings.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "kubehack", "settings.json"), nil
}

type fileFormat struct {
	ReadOnlyContexts []string        `json:"readOnlyContexts"`
	RecentForwards   []RecentForward `json:"recentForwards,omitempty"`
}

// RecentForward is a port forward that started, kept so that the next forward
// to the same object can offer the same ports.
type RecentForward struct {
	Context   string `json:"context"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Port      int    `json:"port"`
	LocalPort int    `json:"localPort"`
}

func (f RecentForward) sameTarget(other RecentForward) bool {
	return f.sameObject(other) && f.Port == other.Port
}

func (f RecentForward) sameObject(other RecentForward) bool {
	return f.Context == other.Context && f.Namespace == other.Namespace &&
		f.Kind == other.Kind && f.Name == other.Name
}

// maxRecentForwards bounds the file. The oldest entries go first.
const maxRecentForwards = 200

// Store holds settings in memory and saves them to path. An empty path keeps
// them in memory only.
type Store struct {
	path string

	mu       sync.RWMutex
	readOnly []string
	// forwards is newest first, with one entry for each object and port.
	forwards []RecentForward
}

// Open loads the store. A missing file is normal on first launch. An unreadable
// file is an error, because guessing could silently make a protected context
// writable.
func Open(path string) (*Store, error) {
	store := &Store{path: path, readOnly: []string{}}
	if path == "" {
		return store, nil
	}

	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	var file fileFormat
	if err := json.Unmarshal(content, &file); err != nil {
		return nil, fmt.Errorf("parse settings %s: %w", path, err)
	}
	if file.ReadOnlyContexts != nil {
		store.readOnly = file.ReadOnlyContexts
	}
	store.forwards = file.RecentForwards
	return store, nil
}

// ReadOnlyContexts returns the protected context names.
func (s *Store) ReadOnlyContexts() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.readOnly)
}

// IsReadOnly reports whether writes to a context are blocked.
func (s *Store) IsReadOnly(context string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Contains(s.readOnly, context)
}

// SetReadOnly changes a context's protection and saves it. The change only
// takes effect if it could be saved, so the UI never shows a state that the
// next launch would lose.
func (s *Store) SetReadOnly(context string, readOnly bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := slices.DeleteFunc(slices.Clone(s.readOnly), func(name string) bool {
		return name == context
	})
	if readOnly {
		next = append(next, context)
		slices.Sort(next)
	}
	if err := s.save(fileFormat{ReadOnlyContexts: next, RecentForwards: s.forwards}); err != nil {
		return err
	}
	s.readOnly = next
	return nil
}

// RecentForwards returns the remembered forwards to one object, newest first.
func (s *Store) RecentForwards(context, namespace, kind, name string) []RecentForward {
	object := RecentForward{Context: context, Namespace: namespace, Kind: kind, Name: name}
	s.mu.RLock()
	defer s.mu.RUnlock()
	found := []RecentForward{}
	for _, forward := range s.forwards {
		if forward.sameObject(object) {
			found = append(found, forward)
		}
	}
	return found
}

// RememberForward records a forward as the newest, replacing an older one to
// the same object and port.
func (s *Store) RememberForward(forward RecentForward) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make([]RecentForward, 0, len(s.forwards)+1)
	next = append(next, forward)
	for _, old := range s.forwards {
		if !old.sameTarget(forward) && len(next) < maxRecentForwards {
			next = append(next, old)
		}
	}
	if err := s.save(fileFormat{ReadOnlyContexts: s.readOnly, RecentForwards: next}); err != nil {
		return err
	}
	s.forwards = next
	return nil
}

func (s *Store) save(file fileFormat) error {
	if s.path == "" {
		return nil
	}
	content, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}

	// Renaming means a crash cannot leave a half-written file, which Open would
	// refuse to load.
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
