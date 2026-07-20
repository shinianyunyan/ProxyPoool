package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	stdflag "flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nadoo/glider/guiweb"
	"github.com/nadoo/glider/internal/discovery"
	"github.com/nadoo/glider/internal/guicore"
	"github.com/nadoo/glider/internal/proxysource"
)

const (
	defaultGUIAddress = "127.0.0.1:8088"
	maxGUIRequestBody = 1 << 20
)

//go:embed internal/proxysource/default_urls.txt
var embeddedSeedSources string

type guiOptions struct {
	address string
	dataDir string
	noOpen  bool
}

type guiApp struct {
	mu          sync.RWMutex
	operationMu sync.Mutex

	settings         guicore.Settings
	proxies          []proxysource.Proxy
	blacklist        map[string]struct{}
	targets          []discovery.Target
	fetchErrors      []proxysource.SourceError
	fetchSources     []proxysource.SourceStatus
	settingsPath     string
	proxiesPath      string
	blacklistPath    string
	targetsPath      string
	configPath       string
	fofaConfigPath   string
	hunterConfigPath string
	manager          *guicore.Manager
	client           *http.Client
}

type settingsPatch struct {
	Listen            *string   `json:"listen"`
	Strategy          *string   `json:"strategy"`
	Check             *string   `json:"check"`
	CheckInterval     *int      `json:"checkInterval"`
	Sources           *[]string `json:"sources"`
	SourceTimeout     *int      `json:"sourceTimeoutSeconds"`
	SourceWorkers     *int      `json:"sourceConcurrency"`
	Protocol          *string   `json:"protocol"`
	FofaQuery         *string   `json:"fofaQuery"`
	FofaKeyEnv        *string   `json:"fofaKeyEnv"`
	FofaResultLimit   *int      `json:"fofaResultLimit"`
	HunterQuery       *string   `json:"hunterQuery"`
	HunterKeyEnv      *string   `json:"hunterKeyEnv"`
	HunterResultLimit *int      `json:"hunterResultLimit"`
}

type fetchRequest struct {
	Sources  []string `json:"sources"`
	Protocol string   `json:"protocol"`
	Apply    bool     `json:"apply"`
}

type discoveryConfigRequest struct {
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	Key      string `json:"key"`
}

type blacklistRequest struct {
	Proxy       string `json:"proxy"`
	Blacklisted bool   `json:"blacklisted"`
}

type proxyView struct {
	proxysource.Proxy
	Blacklisted bool `json:"blacklisted"`
}

type discoveryProviderView struct {
	Provider      string `json:"provider"`
	KeyConfigured bool   `json:"keyConfigured"`
	KeySource     string `json:"keySource"`
	Key           string `json:"key"`
	Endpoint      string `json:"endpoint"`
	ConfigPath    string `json:"configPath"`
	ConfigError   string `json:"configError"`
	Query         string `json:"query"`
	ResultLimit   int    `json:"resultLimit"`
}

func guiModeRequested(executable string, args []string) bool {
	if guiFlagPresent(args) {
		return true
	}
	return guiExecutable(executable) && guiExecutableRequestsGUI(args)
}

func guiExecutable(executable string) bool {
	name := strings.ToLower(filepath.Base(executable))
	name = strings.TrimSuffix(name, ".exe")
	return name == "glider-gui"
}

func guiFlagPresent(args []string) bool {
	for _, arg := range args {
		if arg == "-gui" || arg == "--gui" || arg == "-gui=true" || arg == "--gui=true" {
			return true
		}
	}
	return false
}

func guiExecutableRequestsGUI(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			arg = "-" + strings.TrimPrefix(arg, "--")
		}
		switch {
		case arg == "-gui-no-open":
			continue
		case arg == "-gui-address" || arg == "-gui-data-dir":
			i++
		case strings.HasPrefix(arg, "-gui-address=") || strings.HasPrefix(arg, "-gui-data-dir="):
			continue
		default:
			return false
		}
	}
	return true
}

func runGUI(args []string) error {
	opts, err := parseGUIOptions(args)
	if err != nil {
		return err
	}

	if err := requireLoopbackAddress(opts.address); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.dataDir, 0o700); err != nil {
		return fmt.Errorf("create GUI data directory: %w", err)
	}

	settingsPath := filepath.Join(opts.dataDir, "settings.json")
	settings, err := guicore.LoadSettings(settingsPath)
	if err != nil {
		return fmt.Errorf("load GUI settings: %w", err)
	}
	if _, statErr := os.Stat(settingsPath); errors.Is(statErr, os.ErrNotExist) {
		settings.Sources = parseSeedSources(embeddedSeedSources)
		if err := guicore.SaveSettings(settingsPath, settings); err != nil {
			return fmt.Errorf("save initial GUI settings: %w", err)
		}
	}

	configPath := filepath.Join(opts.dataDir, "managed.conf")
	fofaConfigPath := filepath.Join(opts.dataDir, "fofa.json")
	if err := discovery.EnsureProviderConfig(fofaConfigPath); err != nil {
		return fmt.Errorf("prepare FOFA provider config: %w", err)
	}
	hunterConfigPath := filepath.Join(opts.dataDir, "hunter.json")
	if err := discovery.EnsureHunterProviderConfig(hunterConfigPath); err != nil {
		return fmt.Errorf("prepare Hunter provider config: %w", err)
	}
	manager, err := guicore.NewManager(configPath)
	if err != nil {
		return fmt.Errorf("create glider process manager: %w", err)
	}

	app := &guiApp{
		settings:         settings,
		blacklist:        make(map[string]struct{}),
		settingsPath:     settingsPath,
		proxiesPath:      filepath.Join(opts.dataDir, "proxies.json"),
		blacklistPath:    filepath.Join(opts.dataDir, "blacklist.json"),
		targetsPath:      filepath.Join(opts.dataDir, "discovery-targets.json"),
		configPath:       configPath,
		fofaConfigPath:   fofaConfigPath,
		hunterConfigPath: hunterConfigPath,
		manager:          manager,
		client:           &http.Client{Timeout: 12 * time.Second},
	}
	if err := app.loadProxies(); err != nil {
		return fmt.Errorf("load saved proxies: %w", err)
	}
	if err := app.loadTargets(); err != nil {
		return fmt.Errorf("load saved discovery targets: %w", err)
	}
	if err := app.loadBlacklist(); err != nil {
		return fmt.Errorf("load proxy blacklist: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", app.handleStatus)
	mux.HandleFunc("/api/settings", app.handleSettings)
	mux.HandleFunc("/api/proxies", app.handleProxies)
	mux.HandleFunc("/api/discovery", app.handleDiscovery)
	mux.HandleFunc("/api/discover", app.handleDiscover)
	mux.HandleFunc("/api/fetch", app.handleFetch)
	mux.HandleFunc("/api/start", app.handleStart)
	mux.HandleFunc("/api/stop", app.handleStop)
	mux.HandleFunc("/api/logs", app.handleLogs)
	mux.Handle("/", guiweb.Handler())

	listener, err := net.Listen("tcp", opts.address)
	if err != nil {
		return fmt.Errorf("listen on GUI address %s: %w", opts.address, err)
	}

	server := &http.Server{
		Handler:           guiSecurityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	guiURL := "http://" + listener.Addr().String()
	fmt.Printf("glider GUI is running at %s\n", guiURL)
	if !opts.noOpen {
		go func() {
			time.Sleep(150 * time.Millisecond)
			_ = openBrowser(guiURL)
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		fmt.Printf("received %s, shutting down GUI\n", sig)
	case serveErr := <-errCh:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve GUI: %w", serveErr)
		}
	}

	_ = app.manager.Stop(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func parseGUIOptions(args []string) (guiOptions, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	dataDir := filepath.Join(configDir, "glider-gui")
	if guiExecutable(os.Args[0]) {
		executable, executableErr := os.Executable()
		if executableErr == nil {
			dataDir = filepath.Join(filepath.Dir(executable), "data")
		}
	}
	opts := guiOptions{
		address: defaultGUIAddress,
		dataDir: dataDir,
	}

	fs := stdflag.NewFlagSet("glider-gui", stdflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	gui := fs.Bool("gui", false, "run the local web GUI")
	fs.StringVar(&opts.address, "gui-address", opts.address, "GUI listen address")
	fs.StringVar(&opts.dataDir, "gui-data-dir", opts.dataDir, "GUI data directory")
	fs.BoolVar(&opts.noOpen, "gui-no-open", false, "do not open a browser")

	normalized := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			arg = "-" + strings.TrimPrefix(arg, "--")
		}
		normalized = append(normalized, arg)
	}
	if err := fs.Parse(normalized); err != nil {
		return opts, fmt.Errorf("parse GUI options: %w", err)
	}
	if !*gui {
		return opts, errors.New("GUI mode was not enabled")
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected GUI arguments: %s", strings.Join(fs.Args(), " "))
	}
	opts.dataDir, err = filepath.Abs(opts.dataDir)
	if err != nil {
		return opts, fmt.Errorf("resolve GUI data directory: %w", err)
	}
	return opts, nil
}

func requireLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid GUI address %q: %w", address, err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("GUI address must use a loopback host because the local API has no authentication")
	}
	return nil
}

func (a *guiApp) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	a.mu.RLock()
	response := struct {
		guicore.Status
		ProxyCount   int                        `json:"proxyCount"`
		TargetCount  int                        `json:"targetCount"`
		FetchErrors  []proxysource.SourceError  `json:"fetchErrors"`
		FetchSources []proxysource.SourceStatus `json:"sourceStatuses"`
		Runtime      guicore.RuntimeStatus      `json:"runtime"`
	}{
		Status:       a.manager.Status(),
		ProxyCount:   len(a.proxies),
		TargetCount:  len(a.targets),
		FetchErrors:  append([]proxysource.SourceError(nil), a.fetchErrors...),
		FetchSources: append([]proxysource.SourceStatus(nil), a.fetchSources...),
		Runtime:      a.manager.RuntimeStatus(),
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, response)
}

func (a *guiApp) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.mu.RLock()
		settings := a.settings
		a.mu.RUnlock()
		writeJSON(w, http.StatusOK, settings)
	case http.MethodPut:
		if !allowMutation(w, r) {
			return
		}
		a.operationMu.Lock()
		defer a.operationMu.Unlock()
		var patch settingsPatch
		if err := decodeJSON(w, r, &patch); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		a.mu.RLock()
		settings := a.settings
		forwards := availableProxyURLs(a.proxies, a.blacklist)
		a.mu.RUnlock()
		applySettingsPatch(&settings, patch)
		if _, err := guicore.BuildConfig(settings, forwards); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if err := guicore.SaveSettings(a.settingsPath, settings); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		a.mu.Lock()
		a.settings = settings
		a.mu.Unlock()
		writeJSON(w, http.StatusOK, settings)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPut)
	}
}

func (a *guiApp) handleProxies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.mu.RLock()
		proxies := proxyViews(a.proxies, a.blacklist)
		a.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{"proxies": proxies})
	case http.MethodPut:
		if !allowMutation(w, r) {
			return
		}
		a.operationMu.Lock()
		defer a.operationMu.Unlock()

		var request blacklistRequest
		if err := decodeJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		request.Proxy = strings.TrimSpace(request.Proxy)
		a.mu.RLock()
		settings := a.settings
		proxies := append([]proxysource.Proxy(nil), a.proxies...)
		blacklist := cloneStringSet(a.blacklist)
		a.mu.RUnlock()

		if !containsProxy(proxies, request.Proxy) {
			writeAPIError(w, http.StatusNotFound, errors.New("代理不存在于当前代理池"))
			return
		}
		if request.Blacklisted {
			blacklist[request.Proxy] = struct{}{}
		} else {
			delete(blacklist, request.Proxy)
		}

		forwards := availableProxyURLs(proxies, blacklist)
		if len(forwards) == 0 {
			writeAPIError(w, http.StatusConflict, errors.New("至少需要保留一个未拉黑代理"))
			return
		}
		configText, err := guicore.BuildConfig(settings, forwards)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if err := writeAtomic(a.configPath, []byte(configText), 0o600); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		if a.blacklistPath != "" {
			if err := saveStringSet(a.blacklistPath, blacklist); err != nil {
				writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("save proxy blacklist: %w", err))
				return
			}
		}

		a.mu.Lock()
		a.blacklist = blacklist
		a.mu.Unlock()
		if a.manager != nil && a.manager.Status().Running {
			if err := a.manager.Stop(5 * time.Second); err != nil {
				writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("restart proxy pool: %w", err))
				return
			}
			if err := a.manager.Start(); err != nil {
				writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("restart proxy pool: %w", err))
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"proxy":       request.Proxy,
			"blacklisted": request.Blacklisted,
			"proxies":     proxyViews(proxies, blacklist),
		})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPut)
	}
}

func (a *guiApp) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.mu.RLock()
		settings := a.settings
		targets := append([]discovery.Target(nil), a.targets...)
		a.mu.RUnlock()
		fofa := a.providerView("fofa", settings)
		hunter := a.providerView("hunter", settings)
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":      fofa.Provider,
			"keyConfigured": fofa.KeyConfigured,
			"keySource":     fofa.KeySource,
			"key":           fofa.Key,
			"endpoint":      fofa.Endpoint,
			"configPath":    fofa.ConfigPath,
			"configError":   fofa.ConfigError,
			"providers": map[string]discoveryProviderView{
				"fofa":   fofa,
				"hunter": hunter,
			},
			"targets":     targets,
			"targetCount": len(targets),
		})
	case http.MethodPut:
		if !allowMutation(w, r) {
			return
		}
		a.operationMu.Lock()
		defer a.operationMu.Unlock()
		var request discoveryConfigRequest
		if err := decodeJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if request.Provider == "" {
			request.Provider = "fofa"
		}
		configPath, ok := a.providerConfigPath(request.Provider)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("unsupported discovery provider %q", request.Provider))
			return
		}
		if err := discovery.SaveProviderConfig(configPath, discovery.ProviderConfig{
			Endpoint: request.Endpoint,
			Key:      request.Key,
		}); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		a.mu.RLock()
		settings := a.settings
		a.mu.RUnlock()
		view := a.providerView(request.Provider, settings)
		writeJSON(w, http.StatusOK, map[string]any{
			"provider":      view.Provider,
			"keyConfigured": view.KeyConfigured,
			"keySource":     view.KeySource,
			"key":           view.Key,
			"endpoint":      view.Endpoint,
			"configPath":    view.ConfigPath,
		})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPut)
	}
}

func (a *guiApp) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !allowMutation(w, r) {
		return
	}
	a.operationMu.Lock()
	defer a.operationMu.Unlock()

	var request struct{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	a.mu.RLock()
	settings := a.settings
	a.mu.RUnlock()

	type configuredProvider struct {
		name   string
		config discovery.ProviderConfig
		query  string
		limit  int
	}
	var configured []configuredProvider
	var configErrors []map[string]string
	for _, name := range []string{"fofa", "hunter"} {
		provider, _, err := a.loadDiscoveryProvider(name, settings)
		if err != nil {
			configErrors = append(configErrors, map[string]string{"provider": name, "error": err.Error()})
			continue
		}
		if provider.Key == "" {
			continue
		}
		item := configuredProvider{name: name, config: provider}
		if name == "hunter" {
			item.query = settings.HunterQuery
			item.limit = settings.HunterResultLimit
		} else {
			item.query = settings.FofaQuery
			item.limit = settings.FofaResultLimit
		}
		configured = append(configured, item)
	}
	if len(configured) == 0 {
		writeAPIError(w, http.StatusBadRequest, errors.New("请至少配置一个 FOFA 或 Hunter API Key"))
		return
	}

	type searchResult struct {
		provider string
		result   discovery.Result
		err      error
	}
	results := make(chan searchResult, len(configured))
	for _, item := range configured {
		item := item
		go func() {
			var result discovery.Result
			var err error
			if item.name == "hunter" {
				result, err = discovery.SearchHunter(r.Context(), a.client, discovery.HunterConfig{
					Endpoint: item.config.Endpoint,
					Key:      item.config.Key,
					Query:    item.query,
					Size:     item.limit,
				})
			} else {
				result, err = discovery.Search(r.Context(), a.client, discovery.Config{
					Endpoint: item.config.Endpoint,
					Key:      item.config.Key,
					Query:    item.query,
					Size:     item.limit,
				})
			}
			results <- searchResult{provider: item.name, result: result, err: err}
		}()
	}

	targets := make([]discovery.Target, 0)
	providerResults := make([]map[string]any, 0, len(configured))
	searchErrors := append([]map[string]string(nil), configErrors...)
	total := 0
	for range configured {
		item := <-results
		if item.err != nil {
			searchErrors = append(searchErrors, map[string]string{"provider": item.provider, "error": item.err.Error()})
			continue
		}
		for index := range item.result.Targets {
			item.result.Targets[index].Providers = []string{item.provider}
		}
		total += item.result.Total
		targets = append(targets, item.result.Targets...)
		providerResults = append(providerResults, map[string]any{
			"provider":    item.provider,
			"targetCount": len(item.result.Targets),
			"total":       item.result.Total,
		})
	}
	targets = uniqueTargets(targets)
	if len(targets) == 0 {
		message := "FOFA 和 Hunter 均未返回可用的 HTTP/HTTPS 目标，已保存目标未被替换"
		if len(searchErrors) > 0 {
			message = "已配置的资产搜索接口均失败，已保存目标未被替换"
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": message, "errors": searchErrors})
		return
	}
	if err := saveJSONAtomic(a.targetsPath, targets); err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("save discovery targets: %w", err))
		return
	}
	a.mu.Lock()
	a.targets = targets
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"providers":   providerResults,
		"errors":      searchErrors,
		"targets":     targets,
		"targetCount": len(targets),
		"total":       total,
	})
}

func (a *guiApp) loadDiscoveryProvider(name string, settings guicore.Settings) (discovery.ProviderConfig, string, error) {
	configPath, ok := a.providerConfigPath(name)
	if !ok {
		return discovery.ProviderConfig{}, "", fmt.Errorf("unsupported discovery provider %q", name)
	}
	provider, err := discovery.LoadProviderConfig(configPath)
	if err != nil {
		return discovery.ProviderConfig{}, "", err
	}
	if provider.Key != "" {
		return provider, "config", nil
	}
	keyEnv := settings.FofaKeyEnv
	if name == "hunter" {
		keyEnv = settings.HunterKeyEnv
	}
	provider.Key = strings.TrimSpace(os.Getenv(keyEnv))
	if provider.Key != "" {
		return provider, "environment", nil
	}
	return provider, "", nil
}

func (a *guiApp) providerConfigPath(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "fofa":
		return a.fofaConfigPath, true
	case "hunter":
		return a.hunterConfigPath, true
	default:
		return "", false
	}
}

func (a *guiApp) providerView(name string, settings guicore.Settings) discoveryProviderView {
	provider, keySource, err := a.loadDiscoveryProvider(name, settings)
	configPath, _ := a.providerConfigPath(name)
	view := discoveryProviderView{
		Provider:   name,
		KeySource:  keySource,
		Endpoint:   provider.Endpoint,
		ConfigPath: configPath,
	}
	if name == "hunter" {
		view.Query = settings.HunterQuery
		view.ResultLimit = settings.HunterResultLimit
	} else {
		view.Query = settings.FofaQuery
		view.ResultLimit = settings.FofaResultLimit
	}
	if err != nil {
		view.ConfigError = err.Error()
		return view
	}
	view.KeyConfigured = provider.Key != ""
	if keySource == "config" {
		view.Key = provider.Key
	}
	return view
}

func (a *guiApp) handleFetch(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !allowMutation(w, r) {
		return
	}
	a.operationMu.Lock()
	defer a.operationMu.Unlock()

	var request fetchRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if !request.Apply {
		writeAPIError(w, http.StatusBadRequest, errors.New("apply must be true"))
		return
	}
	a.mu.RLock()
	settings := a.settings
	targets := append([]discovery.Target(nil), a.targets...)
	blacklist := cloneStringSet(a.blacklist)
	a.mu.RUnlock()
	settings.Sources = request.Sources
	settings.Protocol = request.Protocol
	if err := guicore.SaveSettings(a.settingsPath, settings); err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("save proxy source settings: %w", err))
		return
	}
	a.mu.Lock()
	a.settings = settings
	a.mu.Unlock()

	sources := mergeProxySources(settings.Sources, targets)
	result := proxysource.FetchWithOptions(r.Context(), a.client, sources, settings.Protocol, proxysource.FetchOptions{
		PerSourceTimeout: time.Duration(settings.SourceTimeout) * time.Second,
		Concurrency:      settings.SourceWorkers,
	})
	if len(result.Proxies) == 0 {
		a.mu.Lock()
		a.fetchErrors = result.Errors
		a.fetchSources = result.Sources
		a.mu.Unlock()
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":       "没有从代理源获取到已验证的代理，现有代理池未被替换",
			"errors":      result.Errors,
			"sourceCount": len(result.Sources),
			"sources":     result.Sources,
		})
		return
	}

	blacklist = pruneBlacklist(result.Proxies, blacklist)
	forwards := availableProxyURLs(result.Proxies, blacklist)
	if len(forwards) == 0 {
		writeAPIError(w, http.StatusConflict, errors.New("拉取到的代理均已被拉黑，现有代理池未被替换"))
		return
	}
	configText, err := guicore.BuildConfig(settings, forwards)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if err := writeAtomic(a.configPath, []byte(configText), 0o600); err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("write managed config: %w", err))
		return
	}
	if err := saveJSONAtomic(a.proxiesPath, result.Proxies); err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("save proxies: %w", err))
		return
	}
	if a.blacklistPath != "" {
		if err := saveStringSet(a.blacklistPath, blacklist); err != nil {
			writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("save proxy blacklist: %w", err))
			return
		}
	}

	if a.manager.Status().Running {
		if err := a.manager.Stop(5 * time.Second); err != nil {
			writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("stop existing proxy pool: %w", err))
			return
		}
	}
	if err := a.manager.Start(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("start proxy pool: %w", err))
		return
	}

	a.mu.Lock()
	a.proxies = result.Proxies
	a.blacklist = blacklist
	a.fetchErrors = result.Errors
	a.fetchSources = result.Sources
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"proxies":     proxyViews(result.Proxies, blacklist),
		"errors":      result.Errors,
		"sourceCount": len(result.Sources),
		"sources":     result.Sources,
		"status":      a.manager.Status(),
	})
}

func applySettingsPatch(settings *guicore.Settings, patch settingsPatch) {
	if patch.Listen != nil {
		settings.Listen = *patch.Listen
	}
	if patch.Strategy != nil {
		settings.Strategy = *patch.Strategy
	}
	if patch.Check != nil {
		settings.Check = *patch.Check
	}
	if patch.CheckInterval != nil {
		settings.CheckInterval = *patch.CheckInterval
	}
	if patch.Sources != nil {
		settings.Sources = append([]string(nil), (*patch.Sources)...)
	}
	if patch.SourceTimeout != nil {
		settings.SourceTimeout = *patch.SourceTimeout
	}
	if patch.SourceWorkers != nil {
		settings.SourceWorkers = *patch.SourceWorkers
	}
	if patch.Protocol != nil {
		settings.Protocol = *patch.Protocol
	}
	if patch.FofaQuery != nil {
		settings.FofaQuery = *patch.FofaQuery
	}
	if patch.FofaKeyEnv != nil {
		settings.FofaKeyEnv = *patch.FofaKeyEnv
	}
	if patch.FofaResultLimit != nil {
		settings.FofaResultLimit = *patch.FofaResultLimit
	}
	if patch.HunterQuery != nil {
		settings.HunterQuery = *patch.HunterQuery
	}
	if patch.HunterKeyEnv != nil {
		settings.HunterKeyEnv = *patch.HunterKeyEnv
	}
	if patch.HunterResultLimit != nil {
		settings.HunterResultLimit = *patch.HunterResultLimit
	}
}

func (a *guiApp) handleStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !allowMutation(w, r) {
		return
	}
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	a.mu.RLock()
	settings := a.settings
	proxies := append([]proxysource.Proxy(nil), a.proxies...)
	blacklist := cloneStringSet(a.blacklist)
	a.mu.RUnlock()
	forwards := availableProxyURLs(proxies, blacklist)
	if len(forwards) == 0 {
		writeAPIError(w, http.StatusConflict, errors.New("代理池为空，请先拉取代理"))
		return
	}
	configText, err := guicore.BuildConfig(settings, forwards)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if err := writeAtomic(a.configPath, []byte(configText), 0o600); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	if err := a.manager.Start(); err != nil {
		writeAPIError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, a.manager.Status())
}

func (a *guiApp) handleStop(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !allowMutation(w, r) {
		return
	}
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	if err := a.manager.Stop(5 * time.Second); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, a.manager.Status())
}

func (a *guiApp) handleLogs(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": a.manager.Logs()})
}

func (a *guiApp) loadProxies() error {
	f, err := os.Open(a.proxiesPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	var proxies []proxysource.Proxy
	decoder := json.NewDecoder(io.LimitReader(f, maxGUIRequestBody))
	if err := decoder.Decode(&proxies); err != nil {
		return err
	}
	a.proxies = proxies
	return nil
}

func (a *guiApp) loadTargets() error {
	f, err := os.Open(a.targetsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	decoder := json.NewDecoder(io.LimitReader(f, maxGUIRequestBody))
	if err := decoder.Decode(&a.targets); err != nil {
		return err
	}
	return nil
}

func (a *guiApp) loadBlacklist() error {
	file, err := os.Open(a.blacklistPath)
	if errors.Is(err, os.ErrNotExist) {
		if a.blacklist == nil {
			a.blacklist = make(map[string]struct{})
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	var entries []string
	decoder := json.NewDecoder(io.LimitReader(file, maxGUIRequestBody))
	if err := decoder.Decode(&entries); err != nil {
		return err
	}
	a.blacklist = make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			a.blacklist[entry] = struct{}{}
		}
	}
	return nil
}

func saveStringSet(path string, values map[string]struct{}) error {
	entries := make([]string, 0, len(values))
	for value := range values {
		entries = append(entries, value)
	}
	sort.Strings(entries)
	return saveJSONAtomic(path, entries)
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	clone := make(map[string]struct{}, len(values))
	for value := range values {
		clone[value] = struct{}{}
	}
	return clone
}

func proxyViews(proxies []proxysource.Proxy, blacklist map[string]struct{}) []proxyView {
	views := make([]proxyView, 0, len(proxies))
	for _, item := range proxies {
		_, blacklisted := blacklist[proxyURL(item)]
		views = append(views, proxyView{Proxy: item, Blacklisted: blacklisted})
	}
	return views
}

func containsProxy(proxies []proxysource.Proxy, key string) bool {
	for _, item := range proxies {
		if proxyURL(item) == key {
			return true
		}
	}
	return false
}

func availableProxyURLs(proxies []proxysource.Proxy, blacklist map[string]struct{}) []string {
	forwards := make([]string, 0, len(proxies))
	for _, item := range proxies {
		key := proxyURL(item)
		if _, blocked := blacklist[key]; !blocked {
			forwards = append(forwards, key)
		}
	}
	return forwards
}

func proxyURL(proxy proxysource.Proxy) string {
	return (&url.URL{
		Scheme: proxy.Protocol,
		Host:   net.JoinHostPort(proxy.IP, strconv.Itoa(proxy.Port)),
	}).String()
}

func uniqueTargets(targets []discovery.Target) []discovery.Target {
	unique := make([]discovery.Target, 0, len(targets))
	indexByURL := make(map[string]int, len(targets))
	for _, target := range targets {
		if index, exists := indexByURL[target.URL]; exists {
			unique[index].Providers = mergeProviders(unique[index].Providers, target.Providers)
			continue
		}
		target.Providers = mergeProviders(nil, target.Providers)
		indexByURL[target.URL] = len(unique)
		unique = append(unique, target)
	}
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].URL < unique[j].URL
	})
	return unique
}

func mergeProviders(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	merged := make([]string, 0, len(left)+len(right))
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			merged = append(merged, value)
		}
	}
	sort.Strings(merged)
	return merged
}

func pruneBlacklist(proxies []proxysource.Proxy, blacklist map[string]struct{}) map[string]struct{} {
	available := make(map[string]struct{}, len(proxies))
	for _, proxy := range proxies {
		available[proxyURL(proxy)] = struct{}{}
	}
	pruned := make(map[string]struct{}, len(blacklist))
	for value := range blacklist {
		if _, exists := available[value]; exists {
			pruned[value] = struct{}{}
		}
	}
	return pruned
}

func mergeProxySources(manual []string, targets []discovery.Target) []string {
	merged := make([]string, 0, len(manual)+len(targets))
	seen := make(map[string]struct{}, len(manual)+len(targets))
	for _, source := range manual {
		source = strings.TrimRight(strings.TrimSpace(source), "/")
		if source == "" {
			continue
		}
		if _, ok := seen[source]; !ok {
			seen[source] = struct{}{}
			merged = append(merged, source)
		}
	}
	for _, target := range targets {
		source := strings.TrimRight(strings.TrimSpace(target.URL), "/")
		if source == "" {
			continue
		}
		if _, ok := seen[source]; !ok {
			seen[source] = struct{}{}
			merged = append(merged, source)
		}
	}
	return merged
}

func proxyURLs(proxies []proxysource.Proxy) []string {
	forwards := make([]string, 0, len(proxies))
	for _, proxy := range proxies {
		forwards = append(forwards, proxyURL(proxy))
	}
	return forwards
}

func parseSeedSources(content string) []string {
	var sources []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			sources = append(sources, line)
		}
	}
	return sources
}

func guiSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func allowMutation(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") &&
		r.ContentLength != 0 {
		writeAPIError(w, http.StatusUnsupportedMediaType, errors.New("Content-Type must be application/json"))
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || !strings.EqualFold(parsed.Host, r.Host) {
		writeAPIError(w, http.StatusForbidden, errors.New("cross-origin request rejected"))
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxGUIRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	methodNotAllowed(w, method)
	return false
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeAPIError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func saveJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'), 0o600)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tempPath, path)
}

func openBrowser(address string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		cmd = exec.Command("open", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	return cmd.Start()
}
