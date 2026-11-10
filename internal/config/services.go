package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Struck713/brakarr/internal/arr"
)

// Service is one configured Sonarr or Radarr instance.
type Service struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	APIKey  string `json:"apiKey"`
	Enabled bool   `json:"enabled"`
}

// Apps returns the enabled services as arr.Apps.
func (c *Config) Apps() []arr.App {
	var apps []arr.App
	for _, s := range c.Services {
		if !s.Enabled {
			continue
		}
		if app, ok := arr.New(s.Type, s.Name, s.URL, s.APIKey); ok {
			apps = append(apps, app)
		}
	}
	return apps
}

// newID returns a random hex ID for a service.
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// normalizeServices trims service fields and assigns IDs where missing.
func (c *Config) normalizeServices() {
	for i := range c.Services {
		s := &c.Services[i]
		s.ID = strings.TrimSpace(s.ID)
		s.Name = strings.TrimSpace(s.Name)
		s.Type = strings.ToLower(strings.TrimSpace(s.Type))
		s.URL = strings.TrimSpace(s.URL)
		s.APIKey = strings.TrimSpace(s.APIKey)
		if s.ID == "" {
			s.ID = newID()
		}
	}
}

// validateServices checks every service and returns all problems found.
func (c *Config) validateServices() error {
	var errs []error
	ids := map[string]bool{}
	names := map[string]bool{}
	for i, s := range c.Services {
		label := s.Name
		if label == "" {
			label = fmt.Sprintf("service %d", i+1)
		}
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", label))
		} else if names[strings.ToLower(s.Name)] {
			errs = append(errs, fmt.Errorf("%s: name is already used by another service", label))
		}
		names[strings.ToLower(s.Name)] = true
		if !arr.Known(s.Type) {
			errs = append(errs, fmt.Errorf("%s: unknown type %q", label, s.Type))
		}
		if s.URL == "" {
			errs = append(errs, fmt.Errorf("%s: URL is required", label))
		}
		if s.APIKey == "" {
			errs = append(errs, fmt.Errorf("%s: API key is required", label))
		}
		if ids[s.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate service ID %q", label, s.ID))
		}
		ids[s.ID] = true
	}
	return errors.Join(errs...)
}

// validatePathMap checks that every path mapping has both sides.
func (c *Config) validatePathMap() error {
	for _, m := range c.ArrPathMap {
		if m.Local == "" || m.Remote == "" {
			return errors.New("every path mapping needs both a Brakarr path and an app path")
		}
	}
	return nil
}
