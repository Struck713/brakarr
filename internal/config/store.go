package config

import (
	"errors"
	"slices"
	"sync"
)

// Store holds the current configuration in memory and persists changes to disk.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Config

	hooks       sync.Mutex
	check       func(Config) error
	subscribers []func()
}

// Open loads the configuration at path, creating it with defaults if needed
// and rewriting it if it was migrated from an older version.
func Open(path string) (*Store, error) {
	c, stale, err := read(path)
	if err != nil {
		return nil, err
	}
	if stale {
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

// SetCheck sets an extra check run by Save after validation and before the configuration is
// written, such as making sure the configured GPU works. A failing check rejects the save.
func (s *Store) SetCheck(fn func(Config) error) {
	s.hooks.Lock()
	defer s.hooks.Unlock()
	s.check = fn
}

// Subscribe registers fn to be called after every saved change. It runs synchronously in the
// saving goroutine, after the store is unlocked, so it may call Get to read the new configuration.
func (s *Store) Subscribe(fn func()) {
	s.hooks.Lock()
	defer s.hooks.Unlock()
	s.subscribers = append(s.subscribers, fn)
}

// Save validates, persists and applies a new configuration.
func (s *Store) Save(c Config) error {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	s.hooks.Lock()
	check := s.check
	s.hooks.Unlock()
	if check != nil {
		if err := check(c); err != nil {
			return err
		}
	}
	if err := s.write(c); err != nil {
		return err
	}
	s.notify()
	return nil
}

// write persists c and makes it the current configuration.
func (s *Store) write(c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := Write(s.path, c); err != nil {
		return err
	}
	s.cur = c
	return nil
}

// notify calls every subscriber.
func (s *Store) notify() {
	s.hooks.Lock()
	subs := slices.Clone(s.subscribers)
	s.hooks.Unlock()
	for _, fn := range subs {
		fn()
	}
}

// UpdateServices applies fn to a copy of the configuration under the store
// lock, then persists and applies the result. Only services and path mappings
// are validated, so they can be set up before the rest of the config is valid.
func (s *Store) UpdateServices(fn func(*Config) error) error {
	if err := s.updateServices(fn); err != nil {
		return err
	}
	s.notify()
	return nil
}

// updateServices applies and persists a services change under the store lock.
func (s *Store) updateServices(fn func(*Config) error) error {
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
