package discovery

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchRequestAndResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/api/v2/custom/search" {
			t.Errorf("path = %q", r.URL.Path)
		}

		query := r.URL.Query()
		if query.Get("email") != "" ||
			query.Get("key") != "secret" ||
			query.Get("fields") != "host,ip,port,protocol" ||
			query.Get("size") != "25" ||
			query.Get("tenant") != "demo" {
			t.Errorf("query parameters = %#v", query)
		}
		decoded, err := base64.StdEncoding.DecodeString(query.Get("qbase64"))
		if err != nil {
			t.Fatalf("decode qbase64: %v", err)
		}
		if string(decoded) != `app="proxy"` {
			t.Errorf("decoded query = %q", decoded)
		}

		fmt.Fprint(w, `{
			"error": false,
			"errmsg": "",
			"size": 42,
			"results": [
				["HTTPS://Example.COM:443/path?q=1", "192.0.2.1", 443, "https"],
				["https://example.com/other", "192.0.2.1", "443", "https"],
				["", "2001:db8::1", "8080", "HTTP"],
				["ftp://example.net", "192.0.2.2", 21, "ftp"],
				["", "not-an-ip", 80, "http"]
			]
		}`)
	}))
	defer server.Close()

	result, err := Search(context.Background(), server.Client(), Config{
		Endpoint: server.URL + "/api/v2/custom/search?tenant=demo",
		Key:      "secret",
		Query:    ` app="proxy" `,
		Size:     25,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Total != 42 {
		t.Errorf("Total = %d, want 42", result.Total)
	}
	if len(result.Targets) != 2 {
		t.Fatalf("Targets = %#v, want two targets", result.Targets)
	}

	if got := result.Targets[0]; got.URL != "http://[2001:db8::1]:8080" ||
		got.Host != "2001:db8::1" || got.IP != "2001:db8::1" ||
		got.Port != 8080 || got.Protocol != "http" {
		t.Errorf("IPv6 target = %#v", got)
	}
	if got := result.Targets[1]; got.URL != "https://example.com" ||
		got.Host != "example.com" || got.IP != "192.0.2.1" ||
		got.Port != 443 || got.Protocol != "https" {
		t.Errorf("host target = %#v", got)
	}
}

func TestSearchUsesDefaultSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("size") != "100" {
			t.Errorf("size = %q, want 100", r.URL.Query().Get("size"))
		}
		fmt.Fprint(w, `{"error":false,"size":0,"results":[]}`)
	}))
	defer server.Close()

	_, err := Search(context.Background(), server.Client(), Config{
		Endpoint: server.URL,
		Key:      "secret",
		Query:    "query",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
}

func TestSearchRedactsKeyFromAPIErrors(t *testing.T) {
	const key = "top-secret-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"error":true,"errmsg":"invalid credential %s"}`, key)
	}))
	defer server.Close()

	_, err := Search(context.Background(), server.Client(), Config{
		Endpoint: server.URL,
		Key:      key,
		Query:    "query",
	})
	if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("Search() error = %v, want redacted API error", err)
	}
}

func TestSearchRedactsKeyFromTransportErrors(t *testing.T) {
	const key = "transport-secret"
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("request URL was %s", req.URL)
	})}

	_, err := Search(context.Background(), client, Config{
		Endpoint: "http://example.test",
		Key:      key,
		Query:    "query",
	})
	if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("Search() error = %v, want redacted transport error", err)
	}
}

func TestSearchRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", int(maxResponseSize)+1))
	}))
	defer server.Close()

	_, err := Search(context.Background(), server.Client(), Config{
		Endpoint: server.URL,
		Key:      "secret",
		Query:    "query",
	})
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("Search() error = %v, want oversized-response error", err)
	}
}

func TestSearchCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Search(ctx, http.DefaultClient, Config{
		Endpoint: "http://example.test",
		Key:      "secret",
		Query:    "query",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Search() error = %v, want context.Canceled", err)
	}
}

func TestSearchRequiresQueryAndKey(t *testing.T) {
	base := Config{
		Endpoint: "http://example.test",
		Key:      "secret",
		Query:    "query",
	}

	cfg := base
	cfg.Query = " "
	if _, err := Search(context.Background(), http.DefaultClient, cfg); err == nil {
		t.Error("Search() accepted an empty query")
	}
	cfg = base
	cfg.Key = ""
	if _, err := Search(context.Background(), http.DefaultClient, cfg); err == nil {
		t.Error("Search() accepted an empty key")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
