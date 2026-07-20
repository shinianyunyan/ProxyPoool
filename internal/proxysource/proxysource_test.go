package proxysource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFetchFiltersDeduplicatesAndSorts(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"proxies": [
				{"ip":"Example.COM","port":1080,"protocol":"socks5","source":"fingerprint-a","validated":true},
				{"ip":"example.com","port":1080,"protocol":"socks5","source":"duplicate","validated":true},
				{"ip":"2001:db8::1","port":1081,"protocol":"socks5","validated":true},
				{"ip":"192.0.2.2","port":8080,"protocol":"http","validated":true},
				{"ip":"192.0.2.3","port":1080,"protocol":"socks5","validated":false},
				{"ip":"bad host","port":1080,"protocol":"socks5","validated":true},
				{"ip":"192.0.2.4","port":0,"protocol":"socks5","validated":true}
			]
		}`)
	}))
	defer server.Close()

	result := Fetch(
		context.Background(),
		server.Client(),
		[]string{" ", "# disabled", server.URL + "/api/?ignored=yes#fragment"},
		"socks5",
	)

	if len(result.Errors) != 0 {
		t.Fatalf("Fetch() errors = %#v", result.Errors)
	}
	if requestedPath != "/api/proxies_status" {
		t.Fatalf("requested path = %q, want %q", requestedPath, "/api/proxies_status")
	}
	if len(result.Proxies) != 2 {
		t.Fatalf("Fetch() proxies = %#v, want 2 entries", result.Proxies)
	}

	first := result.Proxies[0]
	if first.IP != "2001:db8::1" || first.Port != 1081 || first.Source != server.URL+"/api/?ignored=yes#fragment" {
		t.Errorf("first proxy = %#v", first)
	}
	second := result.Proxies[1]
	if second.IP != "example.com" || second.Port != 1080 || second.Source != "fingerprint-a" {
		t.Errorf("second proxy = %#v", second)
	}
}

func TestFetchContinuesAfterSourceErrors(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"proxies":[{"ip":"192.0.2.1","port":1080,"protocol":"socks5","validated":true}]}`)
	}))
	defer good.Close()

	badStatus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer badStatus.Close()

	result := Fetch(
		context.Background(),
		good.Client(),
		[]string{"ftp://example.com", badStatus.URL, good.URL},
		"socks5",
	)

	if len(result.Proxies) != 1 {
		t.Fatalf("Fetch() proxies = %#v, want one successful proxy", result.Proxies)
	}
	if len(result.Errors) != 2 {
		t.Fatalf("Fetch() errors = %#v, want two source errors", result.Errors)
	}
	if len(result.Sources) != 3 || result.Sources[0].Success || result.Sources[1].Success || !result.Sources[2].Success {
		t.Fatalf("Fetch() source statuses = %#v", result.Sources)
	}
	if result.Errors[0].Source != "ftp://example.com" ||
		!strings.Contains(result.Errors[0].Error, "unsupported") {
		t.Errorf("invalid-source error = %#v", result.Errors[0])
	}
	if result.Errors[1].Source != badStatus.URL ||
		!strings.Contains(result.Errors[1].Error, "503") {
		t.Errorf("HTTP-status error = %#v", result.Errors[1])
	}
}

func TestFetchNormalisesProtocolAndMappedIPv4(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"proxies":[
			{"ip":"192.0.2.1","port":1080,"protocol":"SOCKS5","validated":true},
			{"ip":"::ffff:192.0.2.1","port":1080,"protocol":"socks5","validated":true}
		]}`)
	}))
	defer server.Close()

	result := Fetch(context.Background(), server.Client(), []string{server.URL}, " SOCKS5 ")
	if len(result.Proxies) != 1 || result.Proxies[0].IP != "192.0.2.1" ||
		result.Proxies[0].Protocol != "socks5" || result.Sources[0].ProxyCount != 1 {
		t.Fatalf("normalised result = %#v", result)
	}
}

func TestFetchWithOptionsRunsSourcesConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseAll()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		fmt.Fprint(w, `{"proxies":[{"ip":"192.0.2.1","port":1080,"protocol":"socks5","validated":true}]}`)
	}))
	defer server.Close()

	done := make(chan Result, 1)
	go func() {
		done <- FetchWithOptions(
			context.Background(),
			server.Client(),
			[]string{server.URL + "/one", server.URL + "/two"},
			"socks5",
			FetchOptions{PerSourceTimeout: time.Second, Concurrency: 2},
		)
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			releaseAll()
			t.Fatal("sources did not start concurrently")
		}
	}
	releaseAll()
	result := <-done
	if len(result.Sources) != 2 || !result.Sources[0].Success || !result.Sources[1].Success {
		t.Fatalf("FetchWithOptions() statuses = %#v", result.Sources)
	}
}

func TestFetchWithOptionsTimesOutEachSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	result := FetchWithOptions(
		context.Background(),
		server.Client(),
		[]string{server.URL},
		"socks5",
		FetchOptions{PerSourceTimeout: 20 * time.Millisecond, Concurrency: 1},
	)

	if len(result.Sources) != 1 || result.Sources[0].Success {
		t.Fatalf("FetchWithOptions() statuses = %#v", result.Sources)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Error, "deadline exceeded") {
		t.Fatalf("FetchWithOptions() errors = %#v", result.Errors)
	}
}

func TestFetchRejectsInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"proxies":`)
	}))
	defer server.Close()

	result := Fetch(context.Background(), server.Client(), []string{server.URL}, "socks5")

	if len(result.Proxies) != 0 || len(result.Errors) != 1 ||
		!strings.Contains(result.Errors[0].Error, "decode response") {
		t.Fatalf("Fetch() = %#v, want one decode error", result)
	}
}

func TestFetchRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", int(maxResponseSize)+1))
	}))
	defer server.Close()

	result := Fetch(context.Background(), server.Client(), []string{server.URL}, "socks5")

	if len(result.Proxies) != 0 || len(result.Errors) != 1 ||
		!strings.Contains(result.Errors[0].Error, "response exceeds") {
		t.Fatalf("Fetch() = %#v, want one oversized-response error", result)
	}
}
