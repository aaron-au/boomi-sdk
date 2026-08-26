package boomi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aaron-au/boomi-sdk/internal/version"
	"github.com/aaron-au/boomi-sdk/pace"
	"github.com/aaron-au/boomi-sdk/progress"
)

// Config carries everything a Client needs. It arrives as a struct: the
// SDK reads no environment variables and no files — the caller decides
// where configuration came from.
type Config struct {
	// Host is the platform base URL, e.g. "https://api.boomi.com".
	// Required; must parse as an absolute http(s) URL.
	Host string
	// AccountID is the Boomi account the client operates on. Required.
	AccountID string
	// Username and Token form the API token credential. Required. The
	// Authorization header is HTTP Basic with username
	// "BOOMI_TOKEN."+Username and password Token.
	Username string
	Token    string

	// Partner switches the base path to /partner/api/rest/v1 and requires
	// OverrideAccount, which names the sub-account every request targets.
	// Setting one without the other is a configuration error.
	Partner         bool
	OverrideAccount string

	// UserAgent is appended to the default "boomi-sdk-go/{version}".
	UserAgent string

	// RPS is the pacing rate in requests per second. Zero or negative
	// selects the default of 8. Values above 10 are rejected: the
	// platform allows ~10/s per account and the allowance is shared.
	RPS float64
	// ReadConcurrency caps concurrent reads inside the paced budget.
	// Zero or negative selects the default of 4.
	ReadConcurrency int

	// HTTPClient overrides the transport; nil selects http.DefaultClient.
	HTTPClient *http.Client
	// Observer receives progress events; nil selects progress.Nop.
	Observer progress.Observer
	// Retry overrides the retry policy; nil selects pace.DefaultPolicy().
	Retry *pace.Policy
}

// Defaults applied by New for zero-valued optional fields.
const (
	defaultRPS             = 8
	maxRPS                 = 10
	defaultReadConcurrency = 4
)

// checkRequired rejects a Config missing any of the always-required
// fields.
func checkRequired(cfg Config) error {
	if cfg.Host == "" {
		return errors.New("boomi: Config.Host is required")
	}

	if cfg.AccountID == "" {
		return errors.New("boomi: Config.AccountID is required")
	}

	if cfg.Username == "" {
		return errors.New("boomi: Config.Username is required")
	}

	if cfg.Token == "" {
		return errors.New("boomi: Config.Token is required")
	}

	return nil
}

// checkHost rejects a Config.Host that is not an absolute http(s) URL.
func checkHost(host string) error {
	u, err := url.Parse(host)
	if err != nil {
		return fmt.Errorf("boomi: Config.Host %q is not a valid URL: %w", host, err)
	}

	if !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("boomi: Config.Host %q must be an absolute http(s) URL", host)
	}

	return nil
}

// New validates cfg, applies defaults, and returns a ready Client. It
// performs no network I/O; the process-wide limiter and breaker for the
// account are attached lazily on first use, so constructing a Client never
// resets pacing.
func New(cfg Config) (*Client, error) {
	if err := checkRequired(cfg); err != nil {
		return nil, err
	}

	if err := checkHost(cfg.Host); err != nil {
		return nil, err
	}

	cfg.Host = strings.TrimRight(cfg.Host, "/")

	if cfg.RPS > maxRPS {
		return nil, fmt.Errorf(
			"boomi: Config.RPS %g exceeds the platform allowance of %d requests per second per account; the allowance is shared",
			cfg.RPS,
			maxRPS,
		)
	}

	if cfg.RPS <= 0 {
		cfg.RPS = defaultRPS
	}

	if cfg.ReadConcurrency <= 0 {
		cfg.ReadConcurrency = defaultReadConcurrency
	}

	if cfg.Partner && cfg.OverrideAccount == "" {
		return nil, errors.New(
			"boomi: partner mode requires OverrideAccount; the request would silently hit the wrong account",
		)
	}

	if !cfg.Partner && cfg.OverrideAccount != "" {
		return nil, errors.New(
			"boomi: OverrideAccount is set but Partner is false; enable Partner or clear OverrideAccount",
		)
	}

	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}

	if cfg.Observer == nil {
		cfg.Observer = progress.Nop
	}

	if cfg.Retry == nil {
		p := pace.DefaultPolicy()
		cfg.Retry = &p
	}

	return &Client{
		cfg:        cfg,
		httpClient: cfg.HTTPClient,
		observer:   cfg.Observer,
		userAgent:  version.UserAgent(cfg.UserAgent),
		authUser:   "BOOMI_TOKEN." + cfg.Username,
		authPass:   cfg.Token,
	}, nil
}
