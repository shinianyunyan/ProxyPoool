// Package proxysource fetches validated proxies from proxy-pool HTTP APIs.
package proxysource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxResponseSize          int64 = 4 << 20
	defaultSourceTimeout           = 12 * time.Second
	defaultSourceConcurrency       = 8
)

// Proxy is a validated proxy returned by a source.
type Proxy struct {
	IP        string `json:"ip"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Source    string `json:"source"`
	Validated bool   `json:"validated"`
}

// SourceError describes an error from one source without stopping other sources.
type SourceError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// SourceStatus describes the result of checking and fetching one source.
type SourceStatus struct {
	Source     string `json:"source"`
	Success    bool   `json:"success"`
	ProxyCount int    `json:"proxyCount"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// FetchOptions controls bounded concurrent source requests.
type FetchOptions struct {
	PerSourceTimeout time.Duration
	Concurrency      int
}

// Result contains the proxies and per-source errors collected by Fetch.
type Result struct {
	Proxies []Proxy        `json:"proxies"`
	Errors  []SourceError  `json:"errors"`
	Sources []SourceStatus `json:"sources"`
}

// Fetch retrieves validated proxies of protocol from each source.
func Fetch(ctx context.Context, client *http.Client, sources []string, protocol string) Result {
	return FetchWithOptions(ctx, client, sources, protocol, FetchOptions{
		PerSourceTimeout: defaultSourceTimeout,
		Concurrency:      defaultSourceConcurrency,
	})
}

// FetchWithOptions retrieves sources concurrently with an independent timeout for each source.
func FetchWithOptions(ctx context.Context, client *http.Client, sources []string, protocol string, opts FetchOptions) Result {
	if client == nil {
		client = http.DefaultClient
	}
	if opts.PerSourceTimeout <= 0 {
		opts.PerSourceTimeout = defaultSourceTimeout
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultSourceConcurrency
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))

	result := Result{
		Proxies: make([]Proxy, 0),
		Errors:  make([]SourceError, 0),
		Sources: make([]SourceStatus, 0),
	}
	normalizedSources := uniqueSources(sources)
	if len(normalizedSources) == 0 {
		return result
	}

	fetchClient := client
	if client.Timeout != 0 {
		cloned := *client
		cloned.Timeout = 0
		fetchClient = &cloned
	}

	type sourceResult struct {
		proxies  []Proxy
		status   SourceStatus
		fetchErr error
	}
	results := make([]sourceResult, len(normalizedSources))
	jobs := make(chan int, len(normalizedSources))
	for index := range normalizedSources {
		jobs <- index
	}
	close(jobs)

	workers := min(opts.Concurrency, len(normalizedSources))
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for index := range jobs {
				source := normalizedSources[index]
				started := time.Now()
				sourceCtx, cancel := context.WithTimeout(ctx, opts.PerSourceTimeout)
				proxies, err := fetchSource(sourceCtx, fetchClient, source)
				cancel()
				status := SourceStatus{
					Source:     source,
					Success:    err == nil,
					DurationMS: time.Since(started).Milliseconds(),
				}
				if err != nil {
					status.Error = err.Error()
				}
				results[index] = sourceResult{proxies: proxies, status: status, fetchErr: err}
			}
		}()
	}
	wait.Wait()

	seen := make(map[string]struct{})
	for index, sourceResult := range results {
		status := sourceResult.status
		if sourceResult.fetchErr != nil {
			result.Errors = append(result.Errors, SourceError{
				Source: normalizedSources[index],
				Error:  sourceResult.fetchErr.Error(),
			})
			result.Sources = append(result.Sources, status)
			continue
		}

		for _, proxy := range sourceResult.proxies {
			proxy.Protocol = strings.ToLower(strings.TrimSpace(proxy.Protocol))
			if proxy.Protocol != protocol || !proxy.Validated {
				continue
			}

			host, ok := normalizeHost(proxy.IP)
			if !ok || proxy.Port < 1 || proxy.Port > 65535 {
				continue
			}
			proxy.IP = host
			if proxy.Source == "" {
				proxy.Source = normalizedSources[index]
			}
			key := proxy.Protocol + "://" + net.JoinHostPort(proxy.IP, strconv.Itoa(proxy.Port))
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			status.ProxyCount++
			result.Proxies = append(result.Proxies, proxy)
		}
		result.Sources = append(result.Sources, status)
	}

	sort.Slice(result.Proxies, func(i, j int) bool {
		left := result.Proxies[i]
		right := result.Proxies[j]
		leftKey := left.Protocol + "://" + net.JoinHostPort(left.IP, strconv.Itoa(left.Port))
		rightKey := right.Protocol + "://" + net.JoinHostPort(right.IP, strconv.Itoa(right.Port))
		return leftKey < rightKey
	})

	return result
}

func uniqueSources(sources []string) []string {
	unique := make([]string, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		source = strings.TrimRight(strings.TrimSpace(source), "/")
		if source == "" || strings.HasPrefix(source, "#") {
			continue
		}
		if _, exists := seen[source]; exists {
			continue
		}
		seen[source] = struct{}{}
		unique = append(unique, source)
	}
	return unique
}

func fetchSource(ctx context.Context, client *http.Client, source string) ([]Proxy, error) {
	endpoint, err := sourceEndpoint(source)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected HTTP status: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > maxResponseSize {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseSize)
	}

	var payload struct {
		Proxies []Proxy `json:"proxies"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return payload.Proxies, nil
}

func sourceEndpoint(source string) (string, error) {
	u, err := url.Parse(source)
	if err != nil {
		return "", fmt.Errorf("invalid source URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported source URL scheme %q", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
		return "", fmt.Errorf("invalid source URL")
	}

	u.Path = strings.TrimRight(u.Path, "/") + "/proxies_status"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func normalizeHost(host string) (string, bool) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", false
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap().String(), true
	}

	if len(host) > 253 {
		return "", false
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	if host == "" {
		return "", false
	}

	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') &&
				(char < 'A' || char > 'Z') &&
				(char < '0' || char > '9') &&
				char != '-' {
				return "", false
			}
		}
	}

	return strings.ToLower(host), true
}
