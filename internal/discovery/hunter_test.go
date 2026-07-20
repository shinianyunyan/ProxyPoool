package discovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSearchHunterRequestAndResults(t *testing.T) {
	const hunterQuery = `web.title=="proxy pool"`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/custom/search" {
			t.Errorf("path = %q, want /custom/search", r.URL.Path)
		}

		query := r.URL.Query()
		if query.Get("api-key") != "secret" ||
			query.Get("page") != "1" ||
			query.Get("page_size") != "25" ||
			query.Get("is_web") != "3" ||
			query.Get("tenant") != "demo" {
			t.Errorf("query parameters = %#v", query)
		}
		if query.Get("search") != base64.StdEncoding.EncodeToString([]byte(hunterQuery)) {
			t.Errorf("search = %q, want base64 of the original Hunter query", query.Get("search"))
		}
		decoded, err := base64.StdEncoding.DecodeString(query.Get("search"))
		if err != nil {
			t.Fatalf("decode search: %v", err)
		}
		if string(decoded) != hunterQuery {
			t.Errorf("decoded query = %q, want %q", decoded, hunterQuery)
		}

		fmt.Fprint(w, `{
			"code": 200,
			"message": "success",
			"data": {
				"total": 6,
				"arr": [
					{"url":"HTTPS://Example.COM:443/path?q=1","ip":"192.0.2.1","port":443,"protocol":"https"},
					{"url":"https://example.com/other","ip":"192.0.2.1","port":"443","protocol":"https"},
					{"ip":"2001:db8::1","port":"8080","protocol":"HTTP"},
					{"domain":"Portal.Example","port":8443,"base_protocol":"HTTPS"},
					{"url":"ftp://example.net","ip":"192.0.2.2","port":21,"protocol":"ftp"},
					{"ip":"not-an-ip","port":80,"protocol":"http"}
				]
			}
		}`)
	}))
	defer server.Close()

	result, err := SearchHunter(context.Background(), server.Client(), HunterConfig{
		Endpoint: server.URL + "/custom/search?tenant=demo",
		Key:      "secret",
		Query:    hunterQuery,
		Size:     25,
	})
	if err != nil {
		t.Fatalf("SearchHunter() error = %v", err)
	}
	if result.Total != 6 {
		t.Errorf("Total = %d, want 6", result.Total)
	}
	if len(result.Targets) != 3 {
		t.Fatalf("Targets = %#v, want three normalized and unique targets", result.Targets)
	}

	if got := result.Targets[0]; got.URL != "http://[2001:db8::1]:8080" ||
		got.Host != "2001:db8::1" || got.IP != "2001:db8::1" ||
		got.Port != 8080 || got.Protocol != "http" {
		t.Errorf("IPv6 target = %#v", got)
	}
	if got := result.Targets[1]; got.URL != "https://example.com" ||
		got.Host != "example.com" || got.IP != "192.0.2.1" ||
		got.Port != 443 || got.Protocol != "https" {
		t.Errorf("URL target = %#v", got)
	}
	if got := result.Targets[2]; got.URL != "https://portal.example:8443" ||
		got.Host != "portal.example" || got.IP != "" ||
		got.Port != 8443 || got.Protocol != "https" {
		t.Errorf("domain target = %#v", got)
	}
}

func TestSearchHunterPaginatesToRequestedLimit(t *testing.T) {
	var pages []int
	var pageSizes []int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			t.Fatalf("parse page: %v", err)
		}
		pageSize, err := strconv.Atoi(r.URL.Query().Get("page_size"))
		if err != nil {
			t.Fatalf("parse page_size: %v", err)
		}
		pages = append(pages, page)
		pageSizes = append(pageSizes, pageSize)

		start := (page - 1) * hunterPageSize
		assets := make([]map[string]any, pageSize)
		for i := range assets {
			assets[i] = map[string]any{
				"url":      fmt.Sprintf("http://node-%03d.example.test", start+i),
				"ip":       "192.0.2.1",
				"port":     80,
				"protocol": "http",
			}
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"data": map[string]any{
				"total": 500,
				"arr":   assets,
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	result, err := SearchHunter(context.Background(), server.Client(), HunterConfig{
		Endpoint: server.URL,
		Key:      "secret",
		Query:    `web.title=="proxy pool"`,
		Size:     150,
	})
	if err != nil {
		t.Fatalf("SearchHunter() error = %v", err)
	}
	if len(result.Targets) != 150 {
		t.Fatalf("Targets count = %d, want 150", len(result.Targets))
	}
	if result.Total != 500 {
		t.Errorf("Total = %d, want 500", result.Total)
	}
	if got := fmt.Sprint(pages); got != "[1 2]" {
		t.Errorf("pages = %s, want [1 2]", got)
	}
	if got := fmt.Sprint(pageSizes); got != "[100 50]" {
		t.Errorf("page sizes = %s, want [100 50]", got)
	}
}

func TestSearchHunterRedactsKeyFromAPIErrors(t *testing.T) {
	const key = "top-secret-hunter-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"code":401,"message":"invalid credential %s"}`, key)
	}))
	defer server.Close()

	_, err := SearchHunter(context.Background(), server.Client(), HunterConfig{
		Endpoint: server.URL,
		Key:      key,
		Query:    `web.title=="proxy pool"`,
	})
	if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("SearchHunter() error = %v, want redacted API error", err)
	}
}
