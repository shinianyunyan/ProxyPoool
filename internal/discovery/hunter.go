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
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	// DefaultHunterEndpoint is the official Hunter API service root.
	DefaultHunterEndpoint = "https://hunter.qianxin.com"

	hunterPageSize = 100
)

// HunterConfig configures one Hunter search.
type HunterConfig struct {
	Endpoint string
	Key      string
	Query    string
	Size     int
}

// SearchHunter queries Hunter and returns normalized HTTP and HTTPS targets.
func SearchHunter(ctx context.Context, client *http.Client, cfg HunterConfig) (Result, error) {
	query := strings.TrimSpace(cfg.Query)
	if query == "" {
		return Result{}, errors.New("Hunter query must not be empty")
	}
	if cfg.Key == "" {
		return Result{}, errors.New("Hunter key must not be empty")
	}
	if client == nil {
		client = http.DefaultClient
	}

	endpoint, err := hunterSearchEndpoint(cfg.Endpoint)
	if err != nil {
		return Result{}, safeError(cfg.Key, "invalid Hunter endpoint: %v", err)
	}
	size := cfg.Size
	if size <= 0 {
		size = defaultSize
	}

	targets := make([]Target, 0, size)
	seen := make(map[string]struct{}, size)
	total := 0
	for page := 1; len(targets) < size; page++ {
		pageSize := min(hunterPageSize, size-len(targets))
		pageResult, rawCount, err := searchHunterPage(ctx, client, endpoint, cfg.Key, query, page, pageSize)
		if err != nil {
			return Result{}, err
		}
		if pageResult.Total > total {
			total = pageResult.Total
		}
		for _, target := range pageResult.Targets {
			if _, exists := seen[target.URL]; exists {
				continue
			}
			seen[target.URL] = struct{}{}
			targets = append(targets, target)
			if len(targets) == size {
				break
			}
		}
		if rawCount < pageSize || (total > 0 && page*pageSize >= total) {
			break
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].URL < targets[j].URL
	})
	return Result{Targets: targets, Total: total}, nil
}

func searchHunterPage(
	ctx context.Context,
	client *http.Client,
	base *url.URL,
	key string,
	query string,
	page int,
	pageSize int,
) (Result, int, error) {
	endpoint := *base
	values := endpoint.Query()
	values.Set("api-key", key)
	values.Set("search", base64.StdEncoding.EncodeToString([]byte(query)))
	values.Set("page", strconv.Itoa(page))
	values.Set("page_size", strconv.Itoa(pageSize))
	values.Set("is_web", "3")
	endpoint.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Result{}, 0, safeError(key, "create Hunter request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, 0, ctxErr
		}
		return Result{}, 0, safeError(key, "Hunter request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Result{}, 0, fmt.Errorf("Hunter returned unexpected HTTP status: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return Result{}, 0, safeError(key, "read Hunter response: %v", err)
	}
	if int64(len(body)) > maxResponseSize {
		return Result{}, 0, fmt.Errorf("Hunter response exceeds %d bytes", maxResponseSize)
	}

	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Total int           `json:"total"`
			Arr   []hunterAsset `json:"arr"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Result{}, 0, safeError(key, "decode Hunter response: %v", err)
	}
	if payload.Code != 0 && payload.Code != http.StatusOK {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "unknown API error"
		}
		return Result{}, 0, safeError(key, "Hunter API error: %s", message)
	}

	targets := make([]Target, 0, len(payload.Data.Arr))
	seen := make(map[string]struct{}, len(payload.Data.Arr))
	for _, asset := range payload.Data.Arr {
		target, ok := asset.target()
		if !ok {
			continue
		}
		if _, exists := seen[target.URL]; exists {
			continue
		}
		seen[target.URL] = struct{}{}
		targets = append(targets, target)
	}
	return Result{Targets: targets, Total: payload.Data.Total}, len(payload.Data.Arr), nil
}

type hunterAsset struct {
	URL          string          `json:"url"`
	IP           string          `json:"ip"`
	Domain       string          `json:"domain"`
	Port         json.RawMessage `json:"port"`
	Protocol     string          `json:"protocol"`
	BaseProtocol string          `json:"base_protocol"`
}

func (a hunterAsset) target() (Target, bool) {
	port, _ := rawPort(a.Port)
	if target, ok := targetFromURL(a.URL, a.IP, port); ok {
		return target, true
	}

	protocol := strings.ToLower(strings.TrimSpace(a.Protocol))
	if protocol == "" {
		protocol = strings.ToLower(strings.TrimSpace(a.BaseProtocol))
	}
	if target, ok := targetFromAddress(a.IP, port, protocol); ok {
		return target, true
	}

	host, ok := normalizeHost(a.Domain)
	if !ok || (protocol != "http" && protocol != "https") || port < 1 || port > 65535 {
		return Target{}, false
	}
	return targetFromURL(protocol+"://"+net.JoinHostPort(host, strconv.Itoa(port)), a.IP, port)
}

func hunterSearchEndpoint(endpoint string) (*url.URL, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultHunterEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return nil, errors.New("endpoint must be an HTTP or HTTPS URL")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/openApi/search"
	}
	u.RawPath = ""
	u.Fragment = ""
	return u, nil
}
