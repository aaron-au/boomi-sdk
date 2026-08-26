package boomi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/aaron-au/boomi-sdk/pace"
)

// Kind classifies an error for callers that map errors to process exit
// codes. The SDK never calls os.Exit; the mapping below is ata's exit-code
// contract, defined here so callers map rather than invent:
//
//	KindValidation → exit 2 (bad request, not found, malformed input)
//	KindAuth       → exit 3 (credential rejected or circuit open)
//	KindConflict   → exit 4 (state conflict, e.g. 409)
//	KindTransport  → exit 5 (network failure, timeout, 5xx, 429, truncation)
//
// KindUnknown carries no mapping; callers choose their own general code.
type Kind int

// The recognised kinds; the exit-code mapping each one carries is
// documented on Kind.
const (
	KindUnknown Kind = iota
	KindValidation
	KindAuth
	KindConflict
	KindTransport
)

// kindUnknownName names KindUnknown and any out-of-range Kind value.
const kindUnknownName = "unknown"

// String names the Kind for error messages and logs.
func (k Kind) String() string {
	switch k {
	case KindUnknown:
		return kindUnknownName
	case KindValidation:
		return "validation"
	case KindAuth:
		return "auth"
	case KindConflict:
		return "conflict"
	case KindTransport:
		return "transport"
	default:
		return kindUnknownName
	}
}

// KindOf walks err with errors.As/errors.Is and returns its Kind. An
// *APIError anywhere in the chain decides; otherwise sentinels and
// transport error types are checked. Unrecognised errors are KindUnknown.
func KindOf(err error) Kind {
	if err == nil {
		return KindUnknown
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Kind()
	}

	switch {
	case errors.Is(err, ErrCircuitOpen), errors.Is(err, ErrAuth), errors.Is(err, pace.ErrOpen):
		return KindAuth
	case errors.Is(err, ErrTruncated):
		return KindTransport
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return KindTransport
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return KindTransport
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return KindTransport
	}

	return KindUnknown
}

// maxErrorBody caps how much of a failed response body an APIError
// retains: 64KiB.
const maxErrorBody = 64 << 10

// APIError is a non-2xx platform response.
type APIError struct {
	StatusCode int
	// Method and Path identify the failed call; Path is the request path
	// as sent, not including the host.
	Method string
	Path   string
	// Body is the raw response body, capped at 64KiB.
	Body []byte
	// JSON is the parsed body, populated only when the response
	// Content-Type is JSON and the body parses; nil otherwise.
	JSON map[string]any
}

// Error renders the status line plus a whitespace-collapsed excerpt of the
// body.
func (e *APIError) Error() string {
	msg := fmt.Sprintf("boomi: %s %s: HTTP %d", e.Method, e.Path, e.StatusCode)
	if len(e.Body) > 0 {
		excerpt := strings.Join(strings.Fields(string(e.Body)), " ")

		const excerptCap = 200
		if len(excerpt) > excerptCap {
			excerpt = excerpt[:excerptCap] + "…"
		}

		if excerpt != "" {
			msg += ": " + excerpt
		}
	}

	return msg
}

// Kind maps the HTTP status to an error Kind: 401/403 are auth, 409 is
// conflict, 429 and all 5xx are transport (the retryable band), and every
// other 4xx — 400, 404, and friends — is validation.
func (e *APIError) Kind() Kind {
	switch {
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden:
		return KindAuth
	case e.StatusCode == http.StatusConflict:
		return KindConflict
	case e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500:
		return KindTransport
	case e.StatusCode >= http.StatusBadRequest:
		return KindValidation
	default:
		return KindUnknown
	}
}

// Sentinel errors. Test with errors.Is.
var (
	// ErrCircuitOpen means the auth circuit breaker has opened: two auth
	// rejections in this process, and the SDK refuses further calls
	// locally — a locked account is not recovered by trying harder.
	ErrCircuitOpen = errors.New(
		"boomi: auth circuit open — two auth rejections; refusing further calls (a locked account is not recovered by trying harder)",
	)
	// ErrAuth means the platform rejected the credential (401/403).
	// Never retried, at any level.
	ErrAuth = errors.New("boomi: authentication rejected")
	// ErrTruncated means a paginated query collected fewer results than
	// the platform reported on the first page. The SDK returns this
	// rather than passing off partial results as complete.
	ErrTruncated = errors.New("boomi: pagination truncated — collected fewer results than the platform reported")
)

// Quirk predicates. These detect known platform behaviours that hide
// behind generic status codes; they are detection only, never policy —
// what to do about a match is the caller's decision.

// asAPIError extracts an *APIError from err's chain.
func asAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}

	return nil, false
}

// IsBranchUnlicensed reports whether err is the platform's refusal of a
// branch operation on an account without the branching feature: a 400 or
// 403 whose body contains "does not have access rights".
func IsBranchUnlicensed(err error) bool {
	e, ok := asAPIError(err)
	if !ok || (e.StatusCode != http.StatusBadRequest && e.StatusCode != http.StatusForbidden) {
		return false
	}

	return strings.Contains(string(e.Body), "does not have access rights")
}

// IsDuplicateDeploy reports whether err is the platform's rejection of a
// deployment that already exists: a 400 whose body contains "duplicate"
// (case-insensitive).
func IsDuplicateDeploy(err error) bool {
	e, ok := asAPIError(err)
	if !ok || e.StatusCode != http.StatusBadRequest {
		return false
	}

	return strings.Contains(strings.ToLower(string(e.Body)), "duplicate")
}

// IsLogNotReady reports whether err is the platform's "log not ready yet"
// shape: a 400 whose body contains "is invalid", which the platform emits
// while an execution log is still being assembled.
func IsLogNotReady(err error) bool {
	e, ok := asAPIError(err)
	if !ok || e.StatusCode != http.StatusBadRequest {
		return false
	}

	return strings.Contains(string(e.Body), "is invalid")
}
