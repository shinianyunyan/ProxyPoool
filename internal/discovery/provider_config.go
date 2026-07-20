package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ProviderConfig contains local asset-search API connection details.
type ProviderConfig struct {
	Endpoint string `json:"endpoint"`
	Key      string `json:"key"`
}

// DefaultProviderConfig returns an official FOFA-compatible configuration.
func DefaultProviderConfig() ProviderConfig {
	return ProviderConfig{
		Endpoint: DefaultEndpoint + "/api/v1/search/all",
	}
}

// DefaultHunterProviderConfig returns the official Hunter API configuration.
func DefaultHunterProviderConfig() ProviderConfig {
	return ProviderConfig{
		Endpoint: DefaultHunterEndpoint + "/openApi/search",
	}
}

// EnsureProviderConfig creates a local example configuration without replacing an existing file.
func EnsureProviderConfig(path string) error {
	return ensureProviderConfig(path, DefaultProviderConfig(), "FOFA")
}

// EnsureHunterProviderConfig creates a local Hunter configuration without replacing an existing file.
func EnsureHunterProviderConfig(path string) error {
	return ensureProviderConfig(path, DefaultHunterProviderConfig(), "Hunter")
}

func ensureProviderConfig(path string, defaultConfig ProviderConfig, provider string) error {
	if path == "" {
		return errors.New(provider + " provider config path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s provider config directory: %w", provider, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create %s provider config: %w", provider, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(defaultConfig)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode %s provider config: %w", provider, encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s provider config: %w", provider, closeErr)
	}
	return nil
}

// LoadProviderConfig reads and validates a local provider API configuration.
func LoadProviderConfig(path string) (ProviderConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return ProviderConfig{}, fmt.Errorf("open provider config: %w", err)
	}
	defer file.Close()

	var config ProviderConfig
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return ProviderConfig{}, fmt.Errorf("decode provider config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return ProviderConfig{}, fmt.Errorf("decode provider config: %w", err)
	}

	config, err = validateProviderConfig(config)
	if err != nil {
		return ProviderConfig{}, err
	}
	return config, nil
}

// SaveProviderConfig validates and atomically stores provider API details.
func SaveProviderConfig(path string, config ProviderConfig) error {
	if path == "" {
		return errors.New("provider config path must not be empty")
	}
	config, err := validateProviderConfig(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create provider config directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".provider-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary provider config: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(config)
	closeErr := temp.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode provider config: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close provider config: %w", closeErr)
	}
	if err := os.Rename(tempPath, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace provider config: %w", err)
	}
	return nil
}

func validateProviderConfig(config ProviderConfig) (ProviderConfig, error) {
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	config.Key = strings.TrimSpace(config.Key)
	if config.Endpoint == "" {
		return ProviderConfig{}, errors.New("provider endpoint must not be empty")
	}
	if _, err := searchEndpoint(config.Endpoint); err != nil {
		return ProviderConfig{}, fmt.Errorf("validate provider endpoint: %w", err)
	}
	return config, nil
}
