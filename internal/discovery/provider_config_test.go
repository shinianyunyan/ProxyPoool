package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureAndLoadProviderConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "fofa.json")
	if err := EnsureProviderConfig(path); err != nil {
		t.Fatalf("EnsureProviderConfig: %v", err)
	}
	config, err := LoadProviderConfig(path)
	if err != nil {
		t.Fatalf("LoadProviderConfig: %v", err)
	}
	if config.Endpoint != "https://fofa.info/api/v1/search/all" || config.Key != "" {
		t.Fatalf("unexpected default config: %#v", config)
	}

	const custom = `{"endpoint":"https://third.example/custom/search?tenant=demo","key":" local-key "}`
	if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureProviderConfig(path); err != nil {
		t.Fatalf("EnsureProviderConfig existing file: %v", err)
	}
	config, err = LoadProviderConfig(path)
	if err != nil {
		t.Fatalf("LoadProviderConfig custom file: %v", err)
	}
	if config.Endpoint != "https://third.example/custom/search?tenant=demo" || config.Key != "local-key" {
		t.Fatalf("unexpected custom config: %#v", config)
	}
}

func TestLoadProviderConfigRejectsInvalidContent(t *testing.T) {
	tests := []string{
		`{"endpoint":"","key":"x"}`,
		`{"endpoint":"file:///tmp/fofa","key":"x"}`,
		`{"endpoint":"https://example.test","key":"x","unknown":true}`,
		`{"endpoint":"https://example.test","key":"x"} {}`,
	}
	for _, content := range tests {
		path := filepath.Join(t.TempDir(), "fofa.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadProviderConfig(path); err == nil {
			t.Fatalf("LoadProviderConfig accepted %s", strings.TrimSpace(content))
		}
	}
}

func TestSaveProviderConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "fofa.json")
	if err := SaveProviderConfig(path, ProviderConfig{
		Endpoint: " https://third.example/api/search ",
		Key:      " local-key ",
	}); err != nil {
		t.Fatal(err)
	}
	config, err := LoadProviderConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Endpoint != "https://third.example/api/search" || config.Key != "local-key" {
		t.Fatalf("unexpected saved config: %#v", config)
	}
}
