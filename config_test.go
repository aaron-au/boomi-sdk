package boomi

import (
	"strings"
	"testing"

	"github.com/aaron-au/boomi-sdk/progress"
)

func validConfig() Config {
	return Config{
		Host:      "https://api.boomi.com",
		AccountID: "acme-ABC123",
		Username:  "user@example.com",
		Token:     "secret-token",
	}
}

func TestNewValid(t *testing.T) {
	c, err := New(validConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if c == nil {
		t.Fatal("New returned nil client")
	}
}

func TestNewRequiredFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing host", func(c *Config) { c.Host = "" }},
		{"missing account", func(c *Config) { c.AccountID = "" }},
		{"missing username", func(c *Config) { c.Username = "" }},
		{"missing token", func(c *Config) { c.Token = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)

			if _, err := New(cfg); err == nil {
				t.Fatal("New accepted invalid config")
			}
		})
	}
}

func TestNewHostValidation(t *testing.T) {
	bad := []string{
		"api.boomi.com",       // not absolute
		"ftp://api.boomi.com", // wrong scheme
		"://nope",             // unparseable
		"https://",            // no host
		"/api/rest/v1/acct",   // path only
		"boomi.com/api",       // relative with path
	}
	for _, host := range bad {
		cfg := validConfig()

		cfg.Host = host
		if _, err := New(cfg); err == nil {
			t.Errorf("New accepted Host %q", host)
		}
	}

	good := []string{
		"https://api.boomi.com",
		"http://localhost:8080",
		"https://api.boomi.com/", // trailing slash trimmed
	}
	for _, host := range good {
		cfg := validConfig()
		cfg.Host = host

		c, err := New(cfg)
		if err != nil {
			t.Errorf("New rejected Host %q: %v", host, err)
			continue
		}

		if strings.HasSuffix(c.cfg.Host, "/") {
			t.Errorf("Host %q not trimmed: stored %q", host, c.cfg.Host)
		}
	}
}

func TestNewRPS(t *testing.T) {
	cfg := validConfig()

	cfg.RPS = 11
	if _, err := New(cfg); err == nil {
		t.Fatal("New accepted RPS 11; platform allowance is 10")
	}

	cfg = validConfig()

	cfg.RPS = 10.5
	if _, err := New(cfg); err == nil {
		t.Fatal("New accepted RPS 10.5")
	}

	cfg = validConfig()

	cfg.RPS = 10
	if _, err := New(cfg); err != nil {
		t.Fatalf("New rejected RPS 10: %v", err)
	}

	cfg = validConfig()
	cfg.RPS = 0

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if c.cfg.RPS != 8 {
		t.Fatalf("RPS 0 defaulted to %g, want 8", c.cfg.RPS)
	}

	cfg = validConfig()
	cfg.RPS = -3

	c, err = New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if c.cfg.RPS != 8 {
		t.Fatalf("RPS -3 defaulted to %g, want 8", c.cfg.RPS)
	}
}

func TestNewPartnerOverridePairing(t *testing.T) {
	cfg := validConfig()

	cfg.Partner = true
	if _, err := New(cfg); err == nil {
		t.Fatal("New accepted Partner without OverrideAccount")
	}

	cfg = validConfig()

	cfg.OverrideAccount = "sub-account"
	if _, err := New(cfg); err == nil {
		t.Fatal("New accepted OverrideAccount without Partner")
	}

	cfg = validConfig()
	cfg.Partner = true

	cfg.OverrideAccount = "sub-account"
	if _, err := New(cfg); err != nil {
		t.Fatalf("New rejected valid partner config: %v", err)
	}
}

func TestNewDefaults(t *testing.T) {
	c, err := New(validConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if c.cfg.ReadConcurrency != 4 {
		t.Errorf("ReadConcurrency = %d, want 4", c.cfg.ReadConcurrency)
	}

	if c.observer != progress.Nop {
		t.Error("observer did not default to progress.Nop")
	}

	if c.cfg.Retry == nil {
		t.Fatal("Retry policy not defaulted")
	}

	if c.cfg.Retry.MaxAttempts != 3 {
		t.Errorf("default Retry.MaxAttempts = %d, want 3", c.cfg.Retry.MaxAttempts)
	}

	if c.httpClient == nil {
		t.Error("httpClient not defaulted")
	}

	if c.authUser != "BOOMI_TOKEN.user@example.com" {
		t.Errorf("authUser = %q, want BOOMI_TOKEN.user@example.com", c.authUser)
	}

	if c.authPass != "secret-token" {
		t.Errorf("authPass = %q", c.authPass)
	}

	if !strings.HasPrefix(c.userAgent, "boomi-sdk-go/") {
		t.Errorf("userAgent = %q, want boomi-sdk-go/ prefix", c.userAgent)
	}
}

func TestNewUserAgentAppended(t *testing.T) {
	cfg := validConfig()
	cfg.UserAgent = "companion/0.1.0"

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !strings.HasSuffix(c.userAgent, " companion/0.1.0") {
		t.Errorf("userAgent = %q, want caller suffix appended", c.userAgent)
	}
}
