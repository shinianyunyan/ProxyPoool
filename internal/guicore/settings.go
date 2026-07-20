package guicore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultListen        = "socks5://127.0.0.1:8443"
	defaultStrategy      = "rr"
	defaultCheck         = "http://www.msftconnecttest.com/connecttest.txt#expect=200"
	defaultCheckInterval = 30
	defaultCheckTimeout  = 10
	defaultMaxFailures   = 3
	defaultProtocol      = "socks5"
	defaultSourceTimeout = 10
	defaultSourceWorkers = 8
	defaultFofaQuery     = `title="代理池网页管理界面" || body="pending_proxies_cnt"`
	defaultFofaKeyEnv    = "FOFA_KEY"
	defaultFofaLimit     = 100
	defaultHunterQuery   = `web.title="代理池网页管理界面" || web.body="pending_proxies_cnt"`
	defaultHunterKeyEnv  = "HUNTER_KEY"
	defaultHunterLimit   = 100
)

// Settings contains the GUI-managed subset of glider configuration.
type Settings struct {
	Listen        string   `json:"listen"`
	Strategy      string   `json:"strategy"`
	Check         string   `json:"check"`
	CheckInterval int      `json:"checkInterval"`
	CheckTimeout  int      `json:"checkTimeout"`
	MaxFailures   int      `json:"maxFailures"`
	Protocol      string   `json:"protocol"`
	Sources       []string `json:"sources"`
	SourceTimeout int      `json:"sourceTimeoutSeconds"`
	SourceWorkers int      `json:"sourceConcurrency"`
	// Deprecated fields are accepted only to migrate settings written by older GUI builds.
	FofaEmail         string `json:"fofaEmail,omitempty"`
	FofaQuery         string `json:"fofaQuery"`
	FofaScope         string `json:"fofaScope,omitempty"`
	FofaKeyEnv        string `json:"fofaKeyEnv"`
	FofaResultLimit   int    `json:"fofaResultLimit"`
	HunterQuery       string `json:"hunterQuery"`
	HunterKeyEnv      string `json:"hunterKeyEnv"`
	HunterResultLimit int    `json:"hunterResultLimit"`
}

// DefaultSettings returns settings matching glider's CLI defaults.
func DefaultSettings() Settings {
	return Settings{
		Listen:            defaultListen,
		Strategy:          defaultStrategy,
		Check:             defaultCheck,
		CheckInterval:     defaultCheckInterval,
		CheckTimeout:      defaultCheckTimeout,
		MaxFailures:       defaultMaxFailures,
		Protocol:          defaultProtocol,
		Sources:           []string{},
		SourceTimeout:     defaultSourceTimeout,
		SourceWorkers:     defaultSourceWorkers,
		FofaQuery:         defaultFofaQuery,
		FofaKeyEnv:        defaultFofaKeyEnv,
		FofaResultLimit:   defaultFofaLimit,
		HunterQuery:       defaultHunterQuery,
		HunterKeyEnv:      defaultHunterKeyEnv,
		HunterResultLimit: defaultHunterLimit,
	}
}

// Validate checks whether the settings can be safely rendered as a glider config.
func (s Settings) Validate() error {
	if err := validateListen(s.Listen); err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	switch s.Strategy {
	case "rr", "ha", "lha", "dh":
	default:
		return fmt.Errorf("strategy: unsupported value %q", s.Strategy)
	}

	if err := validateCheck(s.Check); err != nil {
		return fmt.Errorf("check: %w", err)
	}
	if s.CheckInterval <= 0 {
		return errors.New("checkInterval must be greater than zero")
	}
	if s.CheckTimeout <= 0 {
		return errors.New("checkTimeout must be greater than zero")
	}
	if s.MaxFailures <= 0 {
		return errors.New("maxFailures must be greater than zero")
	}

	switch s.Protocol {
	case "socks5", "http":
	default:
		return fmt.Errorf("protocol: unsupported value %q", s.Protocol)
	}

	for i, source := range s.Sources {
		if strings.TrimSpace(source) == "" {
			return fmt.Errorf("sources[%d] must not be empty", i)
		}
		if containsLineBreak(source) {
			return fmt.Errorf("sources[%d] must not contain a line break", i)
		}
	}
	if s.SourceTimeout < 1 || s.SourceTimeout > 120 {
		return errors.New("sourceTimeoutSeconds must be between 1 and 120")
	}
	if s.SourceWorkers < 1 || s.SourceWorkers > 32 {
		return errors.New("sourceConcurrency must be between 1 and 32")
	}
	if containsLineBreakOrNUL(s.FofaQuery) {
		return errors.New("fofaQuery must not contain a line break or NUL")
	}
	if strings.TrimSpace(s.FofaKeyEnv) == "" {
		return errors.New("fofaKeyEnv must not be empty")
	}
	if containsLineBreakOrNUL(s.FofaKeyEnv) {
		return errors.New("fofaKeyEnv must not contain a line break or NUL")
	}
	if !isFOFAKeyEnvironmentName(s.FofaKeyEnv) {
		return errors.New("fofaKeyEnv must use the FOFA_ or GLIDER_FOFA_ namespace")
	}
	if s.FofaResultLimit < 1 || s.FofaResultLimit > 500 {
		return errors.New("fofaResultLimit must be between 1 and 500")
	}
	if containsLineBreakOrNUL(s.HunterQuery) {
		return errors.New("hunterQuery must not contain a line break or NUL")
	}
	if strings.TrimSpace(s.HunterKeyEnv) == "" {
		return errors.New("hunterKeyEnv must not be empty")
	}
	if containsLineBreakOrNUL(s.HunterKeyEnv) {
		return errors.New("hunterKeyEnv must not contain a line break or NUL")
	}
	if !isHunterKeyEnvironmentName(s.HunterKeyEnv) {
		return errors.New("hunterKeyEnv must use the HUNTER_ or GLIDER_HUNTER_ namespace")
	}
	if s.HunterResultLimit < 1 || s.HunterResultLimit > 500 {
		return errors.New("hunterResultLimit must be between 1 and 500")
	}
	return nil
}

func isFOFAKeyEnvironmentName(name string) bool {
	if !strings.HasPrefix(name, "FOFA_") && !strings.HasPrefix(name, "GLIDER_FOFA_") {
		return false
	}
	for _, char := range name {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func isHunterKeyEnvironmentName(name string) bool {
	if !strings.HasPrefix(name, "HUNTER_") && !strings.HasPrefix(name, "GLIDER_HUNTER_") {
		return false
	}
	for _, char := range name {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

// LoadSettings reads settings from path. A missing file yields defaults.
func LoadSettings(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}

	settings := DefaultSettings()
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	settings.FofaEmail = ""
	settings.FofaScope = ""
	if err := settings.Validate(); err != nil {
		return Settings{}, fmt.Errorf("validate settings: %w", err)
	}
	if settings.Sources == nil {
		settings.Sources = []string{}
	}
	return settings, nil
}

// SaveSettings validates and atomically replaces the settings file at path.
func SaveSettings(path string, settings Settings) error {
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("validate settings: %w", err)
	}
	if path == "" {
		return errors.New("settings path must not be empty")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

func validateListen(raw string) error {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return errors.New("must be a non-empty URL without surrounding whitespace")
	}
	if containsLineBreak(raw) {
		return errors.New("must not contain a line break")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	switch u.Scheme {
	case "socks5", "http":
	default:
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("credentials, query, and fragment are not allowed")
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("path is not allowed")
	}
	return validateHostPort(u.Host)
}

func validateCheck(raw string) error {
	if raw == "disable" {
		return nil
	}
	if strings.TrimSpace(raw) != raw || raw == "" {
		return errors.New("must be a non-empty URL or \"disable\"")
	}
	if containsLineBreak(raw) {
		return errors.New("must not contain a line break")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		if u.Hostname() == "" {
			return errors.New("host is required")
		}
	case "tcp":
		if err := validateHostPort(u.Host); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	return nil
}

func validateForward(raw string) error {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return errors.New("must be a non-empty URL without surrounding whitespace")
	}
	if containsLineBreak(raw) {
		return errors.New("must not contain a line break")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	switch u.Scheme {
	case "socks5", "http":
	default:
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("path is not allowed")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("query and fragment are not allowed")
	}
	return validateHostPort(u.Host)
}

func validateHostPort(hostPort string) error {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return fmt.Errorf("host and port are required: %w", err)
	}
	if host == "" {
		return errors.New("host is required")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("port %q must be between 1 and 65535", port)
	}
	return nil
}

func containsLineBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func containsLineBreakOrNUL(value string) bool {
	return strings.ContainsAny(value, "\r\n\x00")
}
