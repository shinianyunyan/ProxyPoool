package guicore

import (
	"fmt"
	"strings"
)

// BuildConfig validates settings and forward URLs, then renders a glider config.
func BuildConfig(settings Settings, forwardURLs []string) (string, error) {
	if err := settings.Validate(); err != nil {
		return "", fmt.Errorf("validate settings: %w", err)
	}
	for i, forwardURL := range forwardURLs {
		if err := validateForward(forwardURL); err != nil {
			return "", fmt.Errorf("forwardURLs[%d]: %w", i, err)
		}
	}

	var config strings.Builder
	fmt.Fprintf(&config, "listen=%s\n", settings.Listen)
	config.WriteString("verbose=true\n")
	for _, forwardURL := range forwardURLs {
		fmt.Fprintf(&config, "forward=%s\n", forwardURL)
	}
	fmt.Fprintf(&config, "strategy=%s\n", settings.Strategy)
	fmt.Fprintf(&config, "check=%s\n", settings.Check)
	fmt.Fprintf(&config, "checkinterval=%d\n", settings.CheckInterval)
	fmt.Fprintf(&config, "checktimeout=%d\n", settings.CheckTimeout)
	fmt.Fprintf(&config, "maxfailures=%d\n", settings.MaxFailures)
	return config.String(), nil
}
