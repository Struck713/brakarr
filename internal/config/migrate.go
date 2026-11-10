package config

import (
	"fmt"
	"strings"
)

// migration upgrades a decoded config file by one version in place.
type migration func(m map[string]any) error

// migrations[i] upgrades a config file from version i to version i+1. Append
// new steps to the end and never edit or reorder existing ones.
var migrations = []migration{
	migrateServices,
	migrateDevice,
}

// currentVersion is the config file version this build writes.
var currentVersion = len(migrations)

// migrate upgrades m from its stored version to currentVersion and reports the
// version it started at.
func migrate(m map[string]any) (int, error) {
	from := 0
	if v, ok := m["version"]; ok {
		f, ok := v.(float64)
		if !ok || f != float64(int(f)) || f < 0 {
			return 0, fmt.Errorf("version: invalid value %v", v)
		}
		from = int(f)
	}
	if from > currentVersion {
		return from, fmt.Errorf("config version %d is newer than this build (%d)", from, currentVersion)
	}
	for i := from; i < currentVersion; i++ {
		if err := migrations[i](m); err != nil {
			return from, fmt.Errorf("migrate config to version %d: %w", i+1, err)
		}
	}
	m["version"] = currentVersion
	return from, nil
}

// migrateServices (v0 -> v1) replaces the single sonarr and radarr entries with
// the services list, unless the file already has services of its own.
func migrateServices(m map[string]any) error {
	existing, _ := m["services"].([]any)
	hadServices := len(existing) > 0
	services := existing
	for _, app := range []struct{ typ, name string }{{"sonarr", "Sonarr"}, {"radarr", "Radarr"}} {
		raw, present := m[app.typ]
		delete(m, app.typ)
		if !present || hadServices {
			continue
		}
		old, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected an object", app.typ)
		}
		url, _ := old["url"].(string)
		apiKey, _ := old["apiKey"].(string)
		if strings.TrimSpace(url) == "" {
			continue
		}
		services = append(services, map[string]any{
			"id": app.typ, "name": app.name, "type": app.typ,
			"url": url, "apiKey": apiKey, "enabled": true,
		})
	}
	if services != nil {
		m["services"] = services
	}
	return nil
}

// migrateDevice (v1 -> v2) moves vaapiDevice to device. Files of this version
// predate the encoder setting, so a missing encoder means VAAPI.
func migrateDevice(m map[string]any) error {
	raw, present := m["vaapiDevice"]
	delete(m, "vaapiDevice")
	if !present {
		return nil
	}
	dev, ok := raw.(string)
	if !ok {
		return fmt.Errorf("vaapiDevice: expected a string")
	}
	enc, _ := m["encoder"].(string)
	if enc == "" {
		enc = "vaapi"
	}
	if cur, _ := m["device"].(string); cur == "" && strings.EqualFold(strings.TrimSpace(enc), "vaapi") {
		m["device"] = dev
	}
	return nil
}
