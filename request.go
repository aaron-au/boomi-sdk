package boomi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aaron-au/boomi-sdk/pace"
)

// Class re-exports pace.Class so callers classify requests without
// importing pace. The type lives in pace to avoid an import cycle.
type Class = pace.Class

// Request classes: reads run concurrently inside the paced budget, writes
// are serialised.
const (
	ClassRead  Class = pace.Read
	ClassWrite Class = pace.Write
)

// Request describes one platform API call.
type Request struct {
	// Method is the HTTP method, e.g. http.MethodGet.
	Method string
	// Path is the endpoint path as individual segments relative to the
	// account base, e.g. []string{"Component", componentID}. Each segment
	// is escaped with url.PathEscape, so a hostile value containing "/"
	// stays one segment.
	Path []string
	// RawSuffix is appended to the final path segment unescaped, for the
	// platform's tilde forms ("~5", "~QjoxMjM4OTM4..."). It must match
	// ^~[A-Za-z0-9+/=]+$ — a tilde followed by version digits or a
	// base64 branch ID.
	RawSuffix string
	// Query holds URL query parameters. overrideAccount is merged in by
	// the client for partner mode; it is never string-appended.
	Query url.Values
	// Body is streamed to the wire, never buffered.
	Body io.Reader
	// ContentType and Accept set the corresponding headers when non-empty.
	ContentType string
	Accept      string
	// Class tells the limiter whether this is a read or a write.
	Class Class
}

// Response is the raw platform reply. The caller owns Body and must close
// it.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// rawSuffixRe validates Request.RawSuffix: a tilde followed by version
// digits or base64 (which may contain + / =).
var rawSuffixRe = regexp.MustCompile(`^~[A-Za-z0-9+/=]+$`)

// url builds the absolute request URL:
//
//	{host}/api/rest/v1/{accountId}/{segments...}{rawSuffix}[?query]
//
// or, in partner mode:
//
//	{host}/partner/api/rest/v1/{accountId}/{segments...}{rawSuffix}[?query]
//
// Path segments are escaped individually; RawSuffix is appended unescaped
// after validation. OverrideAccount is merged into the query via
// url.Values and encoded with everything else — never appended with a
// literal "?", so endpoints that carry their own query string compose
// correctly.
func (c *Client) url(r Request) (string, error) {
	if len(r.Path) == 0 {
		return "", errors.New("boomi: request has no path segments")
	}

	for _, seg := range r.Path {
		if seg == "" {
			return "", errors.New("boomi: request path contains an empty segment")
		}
	}

	if r.RawSuffix != "" && !rawSuffixRe.MatchString(r.RawSuffix) {
		return "", fmt.Errorf("boomi: invalid RawSuffix %q: must match ~[A-Za-z0-9+/=]+", r.RawSuffix)
	}

	var b strings.Builder
	b.WriteString(c.cfg.Host)

	if c.cfg.Partner {
		b.WriteString("/partner")
	}

	b.WriteString("/api/rest/v1/")
	b.WriteString(url.PathEscape(c.cfg.AccountID))

	for _, seg := range r.Path {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(seg))
	}

	b.WriteString(r.RawSuffix)

	q := make(url.Values, len(r.Query)+1)
	for k, vs := range r.Query {
		q[k] = append([]string(nil), vs...)
	}

	if c.cfg.OverrideAccount != "" {
		q.Set("overrideAccount", c.cfg.OverrideAccount)
	}

	if enc := q.Encode(); enc != "" {
		b.WriteByte('?')
		b.WriteString(enc)
	}

	return b.String(), nil
}
