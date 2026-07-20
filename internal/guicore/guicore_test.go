package guicore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "-config" {
		runHelperProcess(os.Args[2])
		return
	}
	os.Exit(m.Run())
}

func runHelperProcess(configPath string) {
	mode, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	switch string(mode) {
	case "exit-error":
		fmt.Fprintln(os.Stdout, "helper stdout")
		fmt.Fprintln(os.Stderr, "helper stderr")
		os.Exit(7)
	case "sleep":
		fmt.Fprintln(os.Stdout, "helper ready")
		time.Sleep(30 * time.Second)
	case "runtime-status":
		path := os.Getenv("GLIDER_GUI_RUNTIME_STATUS_FILE")
		_ = os.WriteFile(path, []byte(`{"generatedAt":"2026-07-20T12:00:00Z","current":"socks5://192.0.2.2:1080","forwarders":[{"url":"socks5://192.0.2.2:1080","address":"192.0.2.2:1080","enabled":true,"failures":0,"latencyMs":42}]}`), 0o600)
		time.Sleep(30 * time.Second)
	default:
		os.Exit(0)
	}
}

func TestDefaultSettings(t *testing.T) {
	got := DefaultSettings()
	if got.Listen != "socks5://127.0.0.1:8443" ||
		got.Strategy != "rr" ||
		got.CheckInterval != 30 ||
		got.CheckTimeout != 10 ||
		got.MaxFailures != 3 ||
		got.Protocol != "socks5" ||
		got.SourceTimeout != 10 ||
		got.SourceWorkers != 8 ||
		got.FofaEmail != "" ||
		got.FofaQuery != `title="代理池网页管理界面" || body="pending_proxies_cnt"` ||
		got.FofaScope != "" ||
		got.FofaKeyEnv != "FOFA_KEY" ||
		got.FofaResultLimit != 100 ||
		got.Sources == nil {
		t.Fatalf("unexpected defaults: %#v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}
}

func TestSettingsRejectsUnrelatedSecretEnvironmentName(t *testing.T) {
	settings := DefaultSettings()
	settings.FofaKeyEnv = "AWS_SECRET_ACCESS_KEY"
	if err := settings.Validate(); err == nil {
		t.Fatal("expected unrelated secret environment name to be rejected")
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	t.Setenv("FOFA_KEY", "must-not-be-persisted")
	want := DefaultSettings()
	want.Strategy = "lha"
	want.Sources = []string{"https://example.test/search"}
	want.FofaResultLimit = 250

	if err := SaveSettings(path, want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "must-not-be-persisted") {
		t.Fatal("FOFA API key was persisted")
	}
	got, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", want) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", got, want)
	}

	want.Strategy = "dh"
	if err := SaveSettings(path, want); err != nil {
		t.Fatalf("overwrite settings: %v", err)
	}
	got, err = LoadSettings(path)
	if err != nil || got.Strategy != "dh" {
		t.Fatalf("atomic overwrite not visible: got=%#v err=%v", got, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".settings.json.tmp-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v, err=%v", matches, err)
	}
}

func TestLoadSettingsMigratesLegacyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{
		"listen": "socks5://127.0.0.1:8443",
		"strategy": "rr",
		"check": "http://www.msftconnecttest.com/connecttest.txt#expect=200",
		"checkInterval": 30,
		"checkTimeout": 10,
		"maxFailures": 3,
		"protocol": "socks5",
		"sources": [],
		"fofaEmail": "old@example.com",
		"fofaScope": "domain=\"example.com\""
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings legacy file: %v", err)
	}
	defaults := DefaultSettings()
	if got.FofaEmail != defaults.FofaEmail ||
		got.FofaQuery != defaults.FofaQuery ||
		got.FofaScope != defaults.FofaScope ||
		got.FofaKeyEnv != defaults.FofaKeyEnv ||
		got.FofaResultLimit != defaults.FofaResultLimit {
		t.Fatalf("FOFA defaults not migrated: %#v", got)
	}
	if err := SaveSettings(path, got); err != nil {
		t.Fatalf("SaveSettings migrated file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fofaEmail") || strings.Contains(string(data), "fofaScope") {
		t.Fatalf("deprecated FOFA fields were persisted: %s", data)
	}
}

func TestLoadSettingsMissingReturnsDefaults(t *testing.T) {
	got, err := LoadSettings(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.Listen != DefaultSettings().Listen {
		t.Fatalf("got %#v", got)
	}
}

func TestLoadSettingsRejectsUnknownAndTrailingJSON(t *testing.T) {
	for name, content := range map[string]string{
		"unknown":  `{"unknown":true}`,
		"trailing": `{}` + "\n{}",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadSettings(path); err == nil {
				t.Fatal("expected invalid JSON to be rejected")
			}
		})
	}
}

func TestSettingsValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Settings)
	}{
		{"listen scheme", func(s *Settings) { s.Listen = "ftp://127.0.0.1:1" }},
		{"listen port", func(s *Settings) { s.Listen = "socks5://127.0.0.1" }},
		{"strategy", func(s *Settings) { s.Strategy = "random" }},
		{"check interval", func(s *Settings) { s.CheckInterval = 0 }},
		{"check timeout", func(s *Settings) { s.CheckTimeout = -1 }},
		{"max failures", func(s *Settings) { s.MaxFailures = 0 }},
		{"protocol", func(s *Settings) { s.Protocol = "https" }},
		{"source newline", func(s *Settings) { s.Sources = []string{"https://example.test\nforward=x"} }},
		{"source timeout zero", func(s *Settings) { s.SourceTimeout = 0 }},
		{"source timeout high", func(s *Settings) { s.SourceTimeout = 121 }},
		{"source concurrency zero", func(s *Settings) { s.SourceWorkers = 0 }},
		{"source concurrency high", func(s *Settings) { s.SourceWorkers = 33 }},
		{"fofa query newline", func(s *Settings) { s.FofaQuery = "title=x\nbody=y" }},
		{"fofa query NUL", func(s *Settings) { s.FofaQuery = "title=x\x00" }},
		{"hunter query newline", func(s *Settings) { s.HunterQuery = "web.title=x\nweb.body=y" }},
		{"hunter query NUL", func(s *Settings) { s.HunterQuery = "web.title=x\x00" }},
		{"fofa key env empty", func(s *Settings) { s.FofaKeyEnv = "" }},
		{"fofa key env newline", func(s *Settings) { s.FofaKeyEnv = "FOFA\nKEY" }},
		{"fofa key env NUL", func(s *Settings) { s.FofaKeyEnv = "FOFA\x00KEY" }},
		{"fofa limit zero", func(s *Settings) { s.FofaResultLimit = 0 }},
		{"fofa limit high", func(s *Settings) { s.FofaResultLimit = 501 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := DefaultSettings()
			test.mutate(&settings)
			if err := settings.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBuildConfig(t *testing.T) {
	settings := DefaultSettings()
	got, err := BuildConfig(settings, []string{
		"socks5://user:pass@127.0.0.1:1080",
		"http://proxy.example:8080",
	})
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	want := strings.Join([]string{
		"listen=socks5://127.0.0.1:8443",
		"verbose=true",
		"forward=socks5://user:pass@127.0.0.1:1080",
		"forward=http://proxy.example:8080",
		"strategy=rr",
		"check=http://www.msftconnecttest.com/connecttest.txt#expect=200",
		"checkinterval=30",
		"checktimeout=10",
		"maxfailures=3",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("config mismatch:\n%s", got)
	}
}

func TestBuildConfigRejectsInvalidForwards(t *testing.T) {
	for _, forwardURL := range []string{
		"https://127.0.0.1:443",
		"socks5://127.0.0.1",
		"socks5://127.0.0.1:1080\nstrategy=dh",
	} {
		t.Run(forwardURL, func(t *testing.T) {
			if _, err := BuildConfig(DefaultSettings(), []string{forwardURL}); err == nil {
				t.Fatal("expected invalid forward URL to be rejected")
			}
		})
	}
}

func TestManagerStartStopAndDuplicateGuard(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "managed.conf")
	if err := os.WriteFile(configPath, []byte("sleep"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManagerWithExecutable(os.Args[0], configPath)
	if err := manager.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(100 * time.Millisecond) })

	if err := manager.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate Start error = %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return strings.Contains(manager.Logs(), "helper ready")
	})
	status := manager.Status()
	if !status.Running || status.PID <= 0 || status.StartTime.IsZero() {
		t.Fatalf("invalid running status: %#v", status)
	}

	if err := manager.Stop(100 * time.Millisecond); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	status = manager.Status()
	if status.Running || status.PID != 0 || status.ExitError != "" {
		t.Fatalf("invalid stopped status: %#v", status)
	}
}

func TestManagerCapturesExitAndLogs(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "managed.conf")
	if err := os.WriteFile(configPath, []byte("exit-error"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManagerWithExecutable(os.Args[0], configPath)
	if err := manager.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return !manager.Status().Running
	})
	status := manager.Status()
	if status.ExitError == "" {
		t.Fatalf("expected exit error: %#v", status)
	}
	logs := manager.Logs()
	if !strings.Contains(logs, "helper stdout") || !strings.Contains(logs, "helper stderr") {
		t.Fatalf("missing output in logs: %q", logs)
	}
}

func TestManagerReadsChildRuntimeStatus(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "managed.conf")
	if err := os.WriteFile(configPath, []byte("runtime-status"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManagerWithExecutable(os.Args[0], configPath)
	if err := manager.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = manager.Stop(100 * time.Millisecond) })

	waitFor(t, time.Second, func() bool {
		return len(manager.RuntimeStatus().Forwarders) == 1
	})
	status := manager.RuntimeStatus()
	if status.Current != "socks5://192.0.2.2:1080" ||
		!status.Forwarders[0].Enabled || status.Forwarders[0].LatencyMS != 42 {
		t.Fatalf("unexpected runtime status: %#v", status)
	}
}

func TestRollingLogPersistsAndTails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glider.log")
	log := newRollingLog(path)
	log.maxSize = 16
	log.backups = 2
	if err := log.Open(); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Write([]byte("first line\n")); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if got := log.Tail(5); got != "line\n" {
		t.Fatalf("tail = %q", got)
	}
	if err := log.Open(); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Write([]byte("second line\n")); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file missing: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
