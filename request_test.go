package boomi

import (
	"net/url"
	"testing"
)

func mustClient(t *testing.T, mutate func(*Config)) *Client {
	t.Helper()

	cfg := validConfig()
	if mutate != nil {
		mutate(&cfg)
	}

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return c
}

func TestURLNormal(t *testing.T) {
	c := mustClient(t, nil)

	got, err := c.url(Request{Path: []string{"Component", "abc-123"}})
	if err != nil {
		t.Fatalf("url: %v", err)
	}

	want := "https://api.boomi.com/api/rest/v1/acme-ABC123/Component/abc-123"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestURLPartnerOverrideEncoding(t *testing.T) {
	c := mustClient(t, func(cfg *Config) {
		cfg.Partner = true
		cfg.OverrideAccount = "sub account&x=1"
	})

	got, err := c.url(Request{Path: []string{"ComponentMetadata", "query"}})
	if err != nil {
		t.Fatalf("url: %v", err)
	}

	want := "https://api.boomi.com/partner/api/rest/v1/acme-ABC123/ComponentMetadata/query?overrideAccount=sub+account%26x%3D1"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestURLTildeSuffixUnescaped(t *testing.T) {
	c := mustClient(t, nil)

	got, err := c.url(Request{Path: []string{"Component", "abc"}, RawSuffix: "~5"})
	if err != nil {
		t.Fatalf("url: %v", err)
	}

	want := "https://api.boomi.com/api/rest/v1/acme-ABC123/Component/abc~5"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}

	// Base64 branch ID form: +, / and = must survive unescaped.
	got, err = c.url(Request{Path: []string{"Component", "abc"}, RawSuffix: "~QjoxMjM4+/OTM4="})
	if err != nil {
		t.Fatalf("url: %v", err)
	}

	want = "https://api.boomi.com/api/rest/v1/acme-ABC123/Component/abc~QjoxMjM4+/OTM4="
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestURLInvalidRawSuffix(t *testing.T) {
	c := mustClient(t, nil)
	for _, suffix := range []string{"~", "5", "~a b", "~a?b", "~abc#", "version~5"} {
		if _, err := c.url(Request{Path: []string{"Component", "abc"}, RawSuffix: suffix}); err == nil {
			t.Errorf("url accepted RawSuffix %q", suffix)
		}
	}
}

func TestURLHostileSegmentEscaped(t *testing.T) {
	c := mustClient(t, nil)

	got, err := c.url(Request{Path: []string{"Component", "a/b?c=d"}})
	if err != nil {
		t.Fatalf("url: %v", err)
	}

	want := "https://api.boomi.com/api/rest/v1/acme-ABC123/Component/a%2Fb%3Fc=d"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestURLExistingQueryMergedWithOverride(t *testing.T) {
	c := mustClient(t, func(cfg *Config) {
		cfg.Partner = true
		cfg.OverrideAccount = "sub-1"
	})
	q := url.Values{}
	q.Set("foo", "bar baz")

	got, err := c.url(Request{Path: []string{"Thing", "query"}, Query: q})
	if err != nil {
		t.Fatalf("url: %v", err)
	}
	// url.Values.Encode sorts keys; both parameters share one "?".
	want := "https://api.boomi.com/partner/api/rest/v1/acme-ABC123/Thing/query?foo=bar+baz&overrideAccount=sub-1"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	// The caller's Values must not be mutated.
	if q.Get("overrideAccount") != "" {
		t.Fatal("url mutated the caller's Query values")
	}
}

func TestURLQueryWithoutOverride(t *testing.T) {
	c := mustClient(t, nil)
	q := url.Values{}
	q.Set("limit", "100")

	got, err := c.url(Request{Path: []string{"Thing", "query"}, Query: q})
	if err != nil {
		t.Fatalf("url: %v", err)
	}

	want := "https://api.boomi.com/api/rest/v1/acme-ABC123/Thing/query?limit=100"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestURLEmptyPathRejected(t *testing.T) {
	c := mustClient(t, nil)
	if _, err := c.url(Request{}); err == nil {
		t.Fatal("url accepted empty path")
	}

	if _, err := c.url(Request{Path: []string{"Component", ""}}); err == nil {
		t.Fatal("url accepted empty path segment")
	}
}
