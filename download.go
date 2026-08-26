package boomi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aaron-au/boomi-sdk/progress"
)

// sameSite reports whether two hosts share a registrable domain.
//
// A deliberately simple last-two-labels comparison, not a public-suffix
// implementation: the question being asked is "is this download on the
// same platform as the API base the operator configured", and both hosts
// are ones the operator chose. An exact host match always passes, so a
// self-hosted or proxied base URL is unaffected. A bare hostname or an IP
// has no registrable domain and only ever matches exactly.
func sameSite(target, base string) bool {
	if strings.EqualFold(target, base) {
		return true
	}

	// The registrable domain is the last two labels of the hostname.
	const domainLabels = 2

	registrable := func(host string) string {
		labels := strings.Split(strings.ToLower(strings.TrimSuffix(host, ".")), ".")
		if len(labels) < domainLabels {
			return ""
		}

		return strings.Join(labels[len(labels)-domainLabels:], ".")
	}

	domain := registrable(target)

	return domain != "" && domain == registrable(base)
}

// checkDownloadURL parses rawURL and refuses any location that is not on
// the same site and scheme as the configured API host. The URL arrives in
// a response body, and a client that will fetch any URL a server names is
// a client that can be pointed anywhere — including at a host that would
// then receive this account's credentials in the Authorization header.
//
// Same site rather than same origin: the platform hands out downloads on
// platform.boomi.com while the API is api.boomi.com, so an origin check
// would refuse every real report.
func (c *Client) checkDownloadURL(rawURL string) (*url.URL, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("boomi: download url: %w", err)
	}

	base, err := url.Parse(c.cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("boomi: base url: %w", err)
	}

	if !strings.EqualFold(target.Scheme, base.Scheme) || !sameSite(target.Hostname(), base.Hostname()) {
		return nil, fmt.Errorf("boomi: refusing to follow a download url on %s://%s — the API is at %s://%s",
			target.Scheme, target.Host, base.Scheme, base.Host)
	}

	return target, nil
}

// Download fetches a file from a URL the platform handed back, rather
// than from a path this SDK composed. Some reports (ConnectionLicensingReport
// among them) are produced asynchronously and answered with a location
// instead of a body; that location is not under the API path root, so it
// cannot go through the normal request builder.
//
// The URL must be on the same site and scheme as Config.Host — see
// checkDownloadURL. The request is paced and breaker-guarded like any
// other, but sent exactly once: while the file is still being generated
// the platform answers 202 or 204 and Download returns an error wrapping
// ErrNotReady, so the caller's own poll loop is the retry and a transport
// retry underneath it would only double up. The caller owns the returned
// body and must close it.
func (c *Client) Download(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	c.attachPace()

	if err := c.breaker.Allow(); err != nil {
		return nil, fmt.Errorf("%w (%w)", ErrCircuitOpen, err)
	}

	target, err := c.checkDownloadURL(rawURL)
	if err != nil {
		return nil, err
	}

	release, waited, acquireErr := c.limiter.Acquire(ctx, ClassRead)
	if acquireErr != nil {
		return nil, acquireErr
	}
	defer release()

	if waited > 0 {
		c.observer.OnPaced(progress.PacedEvent{Waited: waited, RPS: c.cfg.RPS})
	}

	return c.sendDownload(ctx, target)
}

// sendDownload performs the single download attempt.
func (c *Client) sendDownload(ctx context.Context, target *url.URL) (io.ReadCloser, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("boomi: GET %s: %w", target.Path, err)
	}

	hreq.SetBasicAuth(c.authUser, c.authPass)
	hreq.Header.Set("User-Agent", c.userAgent)

	c.observer.OnRequest(progress.RequestEvent{Method: http.MethodGet, Path: target.Path, Attempt: 1})

	resp, doErr := c.httpClient.Do(hreq)
	if doErr != nil {
		return nil, fmt.Errorf("boomi: GET %s: %w", target.Path, doErr)
	}

	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		_ = resp.Body.Close()

		return nil, fmt.Errorf("boomi: GET %s: %w", target.Path, ErrNotReady)

	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		apiErr := c.newAPIError(http.MethodGet, target.Path, resp)
		c.breaker.RecordAuthFailure()

		return nil, fmt.Errorf("%w: %w", ErrAuth, apiErr)

	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return resp.Body, nil

	default:
		return nil, c.newAPIError(http.MethodGet, target.Path, resp)
	}
}
