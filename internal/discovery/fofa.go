// Package discovery finds HTTP proxy-pool API targets.
package discovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	// DefaultEndpoint is the default FOFA API service root.
	DefaultEndpoint = "https://fofa.info"

	maxResponseSize int64 = 4 << 20
	defaultSize           = 100
)

// Target is a normalized HTTP or HTTPS service returned by FOFA.
type Target struct {
	URL       string   `json:"url"`
	Host      string   `json:"host"`
	IP        string   `json:"ip"`
	Port      int      `json:"port"`
	Protocol  string   `json:"protocol"`
	Providers []string `json:"providers,omitempty"`
}

// Result contains the normalized targets and the size reported by FOFA.
type Result struct {
	Targets []Target `json:"targets"`
	Total   int      `json:"total"`
}

// Config configures one FOFA search.
type Config struct {
	Endpoint string
	Key      string
	Query    string
	Size     int
}

// Search queries FOFA and returns normalized HTTP and HTTPS targets.
func Search(ctx context.Context, client *http.Client, cfg Config) (Result, error) {
	query := strings.TrimSpace(cfg.Query)
	if query == "" {
		return Result{}, errors.New("FOFA query must not be empty")
	}
	if cfg.Key == "" {
		return Result{}, errors.New("FOFA key must not be empty")
	}
	if client == nil {
		client = http.DefaultClient
	}

	endpoint, err := searchEndpoint(cfg.Endpoint)
	if err != nil {
		return Result{}, safeError(cfg.Key, "invalid FOFA endpoint: %v", err)
	}

	size := cfg.Size
	if size <= 0 {
		size = defaultSize
	}

	values := endpoint.Query()
	values.Set("key", cfg.Key)
	values.Set("qbase64", base64.StdEncoding.EncodeToString([]byte(query)))
	values.Set("fields", "host,ip,port,protocol")
	values.Set("size", strconv.Itoa(size))
	endpoint.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Result{}, safeError(cfg.Key, "create FOFA request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, ctxErr
		}
		return Result{}, safeError(cfg.Key, "FOFA request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Result{}, fmt.Errorf("FOFA returned unexpected HTTP status: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return Result{}, safeError(cfg.Key, "read FOFA response: %v", err)
	}
	if int64(len(body)) > maxResponseSize {
		return Result{}, fmt.Errorf("FOFA response exceeds %d bytes", maxResponseSize)
	}

	var payload struct {
		Error   bool                `json:"error"`
		Errmsg  string              `json:"errmsg"`
		Size    int                 `json:"size"`
		Results [][]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Result{}, safeError(cfg.Key, "decode FOFA response: %v", err)
	}
	if payload.Error {
		message := strings.TrimSpace(payload.Errmsg)
		if message == "" {
			message = "unknown API error"
		}
		return Result{}, safeError(cfg.Key, "FOFA API error: %s", message)
	}

	targets := make([]Target, 0, len(payload.Results))
	seen := make(map[string]struct{})
	for _, row := range payload.Results {
		target, ok := targetFromRow(row)
		if !ok {
			continue
		}
		if _, exists := seen[target.URL]; exists {
			continue
		}
		seen[target.URL] = struct{}{}
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].URL < targets[j].URL
	})

	return Result{Targets: targets, Total: payload.Size}, nil
}

func searchEndpoint(endpoint string) (*url.URL, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return nil, errors.New("endpoint must be an HTTP or HTTPS URL")
	}

	if u.Path == "" || u.Path == "/" {
		u.Path = "/api/v1/search/all"
	}
	u.RawPath = ""
	u.Fragment = ""
	return u, nil
}

func targetFromRow(row []json.RawMessage) (Target, bool) {
	if len(row) < 4 {
		return Target{}, false
	}

	host, _ := rawString(row[0])
	ip, _ := rawString(row[1])
	port, _ := rawPort(row[2])
	protocol, _ := rawString(row[3])

	if target, ok := targetFromURL(host, ip, port); ok {
		return target, true
	}
	return targetFromAddress(ip, port, protocol)
}

func targetFromURL(rawURL, rawIP string, resultPort int) (Target, bool) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.User != nil {
		return Target{}, false
	}

	protocol := strings.ToLower(u.Scheme)
	if protocol != "http" && protocol != "https" {
		return Target{}, false
	}
	host, ok := normalizeHost(u.Hostname())
	if !ok {
		return Target{}, false
	}

	port := resultPort
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return Target{}, false
		}
	}
	if port == 0 {
		port = defaultPort(protocol)
	}
	if port < 1 || port > 65535 {
		return Target{}, false
	}

	ip, _ := normalizeIP(rawIP)
	if ip == "" {
		ip, _ = normalizeIP(host)
	}

	return Target{
		URL:      baseURL(protocol, host, port),
		Host:     host,
		IP:       ip,
		Port:     port,
		Protocol: protocol,
	}, true
}

func targetFromAddress(rawIP string, port int, rawProtocol string) (Target, bool) {
	protocol := strings.ToLower(strings.TrimSpace(rawProtocol))
	if protocol != "http" && protocol != "https" {
		return Target{}, false
	}
	ip, ok := normalizeIP(rawIP)
	if !ok || port < 1 || port > 65535 {
		return Target{}, false
	}

	return Target{
		URL:      baseURL(protocol, ip, port),
		Host:     ip,
		IP:       ip,
		Port:     port,
		Protocol: protocol,
	}, true
}

func baseURL(protocol, host string, port int) string {
	if port == defaultPort(protocol) {
		return protocol + "://" + hostForURL(host)
	}
	return protocol + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func hostForURL(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func defaultPort(protocol string) int {
	if protocol == "https" {
		return 443
	}
	return 80
}

func normalizeIP(value string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	return addr.Unmap().String(), true
}

func normalizeHost(host string) (string, bool) {
	host = strings.TrimSpace(host)
	if ip, ok := normalizeIP(host); ok {
		return ip, true
	}
	if len(host) == 0 || len(host) > 253 {
		return "", false
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
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

func rawString(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func rawPort(raw json.RawMessage) (int, bool) {
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, true
	}

	value, ok := rawString(raw)
	if !ok {
		return 0, false
	}
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return port, true
}

func safeError(key, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if key != "" {
		message = strings.ReplaceAll(message, key, "[redacted]")
	}
	return errors.New(message)
}
