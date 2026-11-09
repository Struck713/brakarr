package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseValue(t *testing.T) {
	tests := map[string]string{
		`true`:                       "true",
		`true   # comment`:           "true",
		`  true  `:                   "true",
		`"a # b"`:                    "a # b",
		`'a # b' # trailing`:         "a # b",
		`"quoted"`:                   "quoted",
		`/path/with#hash`:            "/path/with#hash",
		`value#nospace`:              "value#nospace",
		`10m        # 0 = scan once`: "10m",
		``:                           "",
	}
	for in, want := range tests {
		if got := parseValue(in); got != want {
			t.Errorf("parseValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadDotEnvStripsInlineComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("BRAKARR_TEST_KEY=abc   # note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAKARR_TEST_KEY", "")
	os.Unsetenv("BRAKARR_TEST_KEY")
	defer os.Unsetenv("BRAKARR_TEST_KEY")
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BRAKARR_TEST_KEY"); got != "abc" {
		t.Errorf("got %q, want abc", got)
	}
}
