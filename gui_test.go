package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nadoo/glider/internal/discovery"
	"github.com/nadoo/glider/internal/guicore"
	"github.com/nadoo/glider/internal/proxysource"
)

func TestParseGUIOptions(t *testing.T) {
	opts, err := parseGUIOptions([]string{
		"--gui",
		"--gui-no-open",
		"--gui-address", "127.0.0.1:19090",
		"--gui-data-dir", "test-data",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.address != "127.0.0.1:19090" || !opts.noOpen || !filepath.IsAbs(opts.dataDir) {
		t.Fatalf("unexpected options: %+v", opts)
	}
}

func TestGUIModeRequestedForNamedGUIBinary(t *testing.T) {
	if !guiModeRequested(`C:\tools\glider-gui.exe`, nil) {
		t.Fatal("expected glider-gui.exe to enable GUI mode")
	}
	if !guiModeRequested("glider", []string{"--gui"}) {
		t.Fatal("expected --gui to enable GUI mode")
	}
	if guiModeRequested("glider", []string{"-listen", "socks5://127.0.0.1:1080"}) {
		t.Fatal("did not expect CLI mode to enable GUI")
	}
	if guiModeRequested(`C:\tools\glider-gui.exe`, []string{"-config", "managed.conf"}) {
		t.Fatal("did not expect glider CLI child mode to enable GUI")
	}
	if !guiModeRequested(`C:\tools\glider-gui.exe`, []string{"--gui-no-open", "--gui-address", "127.0.0.1:8088"}) {
		t.Fatal("expected glider-gui.exe with GUI flags to enable GUI mode")
	}
}

func TestEmbeddedSeedSourcesDoNotNeedSourceDirectory(t *testing.T) {
	sources := parseSeedSources(embeddedSeedSources)
	if len(sources) == 0 {
		t.Fatal("expected proxy source defaults to be embedded in the binary")
	}
	for _, source := range sources {
		if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
			t.Fatalf("unexpected embedded source %q", source)
		}
	}
}

func TestRequireLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8088", "localhost:8088", "[::1]:8088"} {
		if err := requireLoopbackAddress(address); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	if err := requireLoopbackAddress("0.0.0.0:8088"); err == nil {
		t.Fatal("expected a non-loopback address to be rejected")
	}
}

func TestSettingsPatchPreservesUnsentValues(t *testing.T) {
	settings := guicore.DefaultSettings()
	timeout, failures := settings.CheckTimeout, settings.MaxFailures
	listen := "socks5://127.0.0.1:9443"
	applySettingsPatch(&settings, settingsPatch{Listen: &listen})

	if settings.Listen != listen {
		t.Fatalf("listen = %q", settings.Listen)
	}
	if settings.CheckTimeout != timeout || settings.MaxFailures != failures {
		t.Fatalf("unsent settings changed: %+v", settings)
	}
}

func TestHandleSettingsMergesPartialUpdate(t *testing.T) {
	settings := guicore.DefaultSettings()
	app := &guiApp{
		settings:     settings,
		settingsPath: filepath.Join(t.TempDir(), "settings.json"),
	}
	body := bytes.NewBufferString(`{"listen":"socks5://127.0.0.1:9443","checkInterval":45}`)
	request := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	app.handleSettings(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if app.settings.Listen != "socks5://127.0.0.1:9443" || app.settings.CheckInterval != 45 {
		t.Fatalf("partial update was not applied: %+v", app.settings)
	}
	if app.settings.CheckTimeout != settings.CheckTimeout || app.settings.MaxFailures != settings.MaxFailures {
		t.Fatalf("partial update cleared defaults: %+v", app.settings)
	}
}

func TestHandleFetchAppliesValidatedProxies(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxies_status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"proxies": []map[string]any{{
				"ip":        "127.0.0.1",
				"port":      19999,
				"protocol":  "socks5",
				"validated": true,
			}},
		})
	}))
	defer source.Close()
	failedSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer failedSource.Close()

	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "managed.conf")
	app := &guiApp{
		settings:     guicore.DefaultSettings(),
		targets:      []discovery.Target{{URL: source.URL}, {URL: failedSource.URL}},
		settingsPath: filepath.Join(dataDir, "settings.json"),
		proxiesPath:  filepath.Join(dataDir, "proxies.json"),
		configPath:   configPath,
		manager:      guicore.NewManagerWithExecutable(os.Args[0], configPath),
		client:       source.Client(),
	}
	payload, _ := json.Marshal(fetchRequest{
		Sources:  nil,
		Protocol: "socks5",
		Apply:    true,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/fetch", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	app.handleFetch(response, request)
	defer app.manager.Stop(0)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var responseBody struct {
		SourceCount int                        `json:"sourceCount"`
		Sources     []proxysource.SourceStatus `json:"sources"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatal(err)
	}
	if responseBody.SourceCount != 2 {
		t.Fatalf("sourceCount = %d, want 2", responseBody.SourceCount)
	}
	if len(responseBody.Sources) != 2 || !responseBody.Sources[0].Success ||
		responseBody.Sources[0].ProxyCount != 1 || responseBody.Sources[1].Success ||
		!strings.Contains(responseBody.Sources[1].Error, "503") {
		t.Fatalf("unexpected source statuses: %#v", responseBody.Sources)
	}
	if len(app.proxies) != 1 || app.proxies[0].IP != "127.0.0.1" {
		t.Fatalf("unexpected proxies: %+v", app.proxies)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "forward=socks5://127.0.0.1:19999") {
		t.Fatalf("managed config does not contain proxy:\n%s", config)
	}
}

func TestHandleDiscoverSavesTargets(t *testing.T) {
	const key = "test-fofa-key"
	var finalQuery string
	fofa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom/search" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("email") != "" || r.URL.Query().Get("key") != key {
			t.Errorf("unexpected credentials in request")
		}
		decoded, err := base64.StdEncoding.DecodeString(r.URL.Query().Get("qbase64"))
		if err != nil {
			t.Errorf("decode query: %v", err)
		}
		finalQuery = string(decoded)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": false,
			"size":  1,
			"results": [][]any{{
				"https://pool.example:8443", "192.0.2.10", 8443, "https",
			}},
		})
	}))
	defer fofa.Close()

	settings := guicore.DefaultSettings()
	dataDir := t.TempDir()
	fofaConfigPath := filepath.Join(dataDir, "fofa.json")
	configData, err := json.Marshal(discovery.ProviderConfig{Endpoint: fofa.URL + "/custom/search", Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fofaConfigPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	app := &guiApp{
		settings:       settings,
		targetsPath:    filepath.Join(dataDir, "discovery-targets.json"),
		fofaConfigPath: fofaConfigPath,
		client:         fofa.Client(),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/discover", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	app.handleDiscover(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if finalQuery != settings.FofaQuery {
		t.Fatalf("query = %q, want %q", finalQuery, settings.FofaQuery)
	}
	if strings.Contains(response.Body.String(), key) {
		t.Fatal("FOFA key leaked in API response")
	}
	if len(app.targets) != 1 || app.targets[0].URL != "https://pool.example:8443" {
		t.Fatalf("unexpected targets: %+v", app.targets)
	}
	if _, err := os.Stat(app.targetsPath); err != nil {
		t.Fatalf("targets were not persisted: %v", err)
	}

	discoveryResponse := httptest.NewRecorder()
	app.handleDiscovery(discoveryResponse, httptest.NewRequest(http.MethodGet, "/api/discovery", nil))
	if discoveryResponse.Code != http.StatusOK {
		t.Fatalf("discovery status = %d, body = %s", discoveryResponse.Code, discoveryResponse.Body.String())
	}
	if !strings.Contains(discoveryResponse.Body.String(), `"keySource":"config"`) ||
		!strings.Contains(discoveryResponse.Body.String(), `"key":"`+key+`"`) ||
		!strings.Contains(discoveryResponse.Body.String(), `/custom/search`) {
		t.Fatalf("unexpected discovery status: %s", discoveryResponse.Body.String())
	}
}

func TestHandleDiscoveryUpdatesProviderConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fofa.json")
	if err := discovery.EnsureProviderConfig(path); err != nil {
		t.Fatal(err)
	}
	app := &guiApp{
		settings:       guicore.DefaultSettings(),
		fofaConfigPath: path,
	}
	request := httptest.NewRequest(http.MethodPut, "/api/discovery", bytes.NewBufferString(
		`{"endpoint":"https://third.example/api/search","key":"new-key"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	app.handleDiscovery(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	config, err := discovery.LoadProviderConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Endpoint != "https://third.example/api/search" || config.Key != "new-key" {
		t.Fatalf("unexpected saved provider config: %#v", config)
	}
}

func TestMergeProxySources(t *testing.T) {
	got := mergeProxySources(
		[]string{"http://manual.example/", "http://manual.example"},
		[]discovery.Target{{URL: "https://found.example/"}, {URL: "http://manual.example"}},
	)
	want := []string{"http://manual.example", "https://found.example"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sources = %v, want %v", got, want)
	}
}

func TestProxyURLsSupportsIPv6(t *testing.T) {
	urls := proxyURLs([]proxysource.Proxy{{
		IP:       "2001:db8::1",
		Port:     1080,
		Protocol: "socks5",
	}})
	if len(urls) != 1 || urls[0] != "socks5://[2001:db8::1]:1080" {
		t.Fatalf("unexpected URLs: %v", urls)
	}
}
