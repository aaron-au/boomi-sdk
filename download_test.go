package boomi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDownloadStreamsSameSiteURL(t *testing.T) {
	var sawAuth, sawUA string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawUA = r.Header.Get("User-Agent")
		_, _ = fmt.Fprint(w, "report,body")
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)

	body, err := c.Download(context.Background(), srv.URL+"/report/123")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = body.Close() }()

	raw, _ := io.ReadAll(body)
	if string(raw) != "report,body" {
		t.Fatalf("body = %q, want %q", raw, "report,body")
	}

	if !strings.HasPrefix(sawAuth, "Basic ") {
		t.Fatalf("Authorization = %q, want Basic auth", sawAuth)
	}

	if sawUA == "" {
		t.Fatal("User-Agent header was not set")
	}
}

func TestDownloadRefusesCrossSiteURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("cross-site download reached the wire")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)

	_, err := c.Download(context.Background(), "http://evil.example.com/report")
	if err == nil || !strings.Contains(err.Error(), "refusing to follow") {
		t.Fatalf("err = %v, want a cross-site refusal", err)
	}
}

func TestDownloadNotReadyOn202(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)

	_, err := c.Download(context.Background(), srv.URL+"/report")
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
}

func TestDownloadFailureIsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)

	_, err := c.Download(context.Background(), srv.URL+"/report")
	apiErr := mustAPIError(t, err)

	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestSameSite(t *testing.T) {
	cases := []struct {
		target, base string
		want         bool
	}{
		{"api.boomi.com", "api.boomi.com", true},
		{"platform.boomi.com", "api.boomi.com", true},
		{"boomi.com.evil.net", "api.boomi.com", false},
		{"evil.example.com", "api.boomi.com", false},
		{"127.0.0.1", "127.0.0.1", true},
		{"localhost", "localhost", true},
		{"localhost", "127.0.0.1", false},
	}

	for _, tc := range cases {
		if got := sameSite(tc.target, tc.base); got != tc.want {
			t.Errorf("sameSite(%q, %q) = %v, want %v", tc.target, tc.base, got, tc.want)
		}
	}
}
