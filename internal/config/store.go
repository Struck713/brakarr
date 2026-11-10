package config

import (
	"errors"
	"os"
	"slices"
	"sync"
)

// Store holds the current configuration in memory and persists changes to disk.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Config
}

// Open loads the configuration at path, creating it with defaults if needed.
func Open(path string) (*Store, error) {
	c, err := Read(path)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		if err := Write(path, c); err != nil {
			return nil, err
		}
	}
	return &Store{path: path, cur: c}, nil
}

// Path returns the file the configuration is stored in.
func (s *Store) Path() string { return s.path }

// Get returns a copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Save validates, persists and applies a new configuration.
func (s *Store) Save(c Config) error {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := Write(s.path, c); err != nil {
		return err
	}
	s.cur = c
	return nil
}

// UpdateServices applies fn to a copy of the configuration under the store
// lock, then persists and applies the result. Only services and path mappings
// are validated, so they can be set up before the rest of the config is valid.
func (s *Store) UpdateServices(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cur
	// Get is a shallow copy, so clone the slices fn may modify in place.
	c.Services = slices.Clone(c.Services)
	c.ArrPathMap = slices.Clone(c.ArrPathMap)
	if err := fn(&c); err != nil {
		return err
	}
	c.Normalize()
	if err := errors.Join(c.validateServices(), c.validatePathMap()); err != nil {
		return err
	}
	if err := Write(s.path, c); err != nil {
		return err
	}
	s.cur = c
	return nil
}
