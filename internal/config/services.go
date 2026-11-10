package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// legacyApp is the connection details stored by older versions.
type legacyApp struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

// legacy holds the config keys that older versions used for services.
type legacy struct {
	Sonarr legacyApp `json:"sonarr"`
	Radarr legacyApp `json:"radarr"`
}

// file is the on-disk shape: the config plus legacy keys read only for migration.
type file struct {
	Config
	legacy
}

// UnmarshalJSON decodes the embedded Config and the legacy keys together while
// still rejecting unknown fields.
func (f *file) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for _, k := range []string{"sonarr", "radarr"} {
		if v, ok := raw[k]; ok {
			target := &f.Sonarr
			if k == "radarr" {
				target = &f.Radarr
			}
			if err := json.Unmarshal(v, target); err != nil {
				return err
			}
			delete(raw, k)
		}
	}
	rest, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(rest))
	dec.DisallowUnknownFields()
	return dec.Decode(&f.Config)
}

// migrate converts legacy sonarr and radarr entries into services when the
// config has no services of its own. The legacy keys vanish on the next save.
func (c *Config) migrate(l legacy) {
	if len(c.Services) > 0 {
		return
	}
	for _, old := range []struct {
		typ, name string
		legacyApp
	}{{"sonarr", "Sonarr", l.Sonarr}, {"radarr", "Radarr", l.Radarr}} {
		if strings.TrimSpace(old.URL) == "" {
			continue
		}
		c.Services = append(c.Services, Service{
			ID: old.typ, Name: old.name, Type: old.typ,
			URL: old.URL, APIKey: old.APIKey, Enabled: true,
		})
	}
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
