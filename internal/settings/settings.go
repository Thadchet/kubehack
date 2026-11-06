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
	ReadOnlyContexts []string `json:"readOnlyContexts"`
}

// Store holds settings in memory and saves them to path. An empty path keeps
// them in memory only.
type Store struct {
	path string

	mu       sync.RWMutex
	readOnly []string
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
	if err := s.save(next); err != nil {
		return err
	}
	s.readOnly = next
	return nil
}

func (s *Store) save(readOnly []string) error {
	if s.path == "" {
		return nil
	}
	content, err := json.MarshalIndent(fileFormat{ReadOnlyContexts: readOnly}, "", "  ")
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
