package objects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
	"github.com/aaron-au/boomi-sdk/progress"
)

// emptyFilter is the body sent when the caller supplies no filter: query
// everything. It matches internal/query's zero Filter exactly.
const emptyFilter = `{"QueryFilter":{}}`

// contentTypeJSON is the media type for query envelopes.
const contentTypeJSON = "application/json"

// mustFilter renders expr as a QueryFilter request body. It panics on a
// builder error, which cannot happen for the statically-shaped expressions
// this package constructs (non-empty properties, non-empty groups).
func mustFilter(expr query.Expression) json.RawMessage {
	b, err := (query.Filter{Expression: expr}).JSON()
	if err != nil {
		panic(fmt.Sprintf("objects: filter marshal failed: %v", err))
	}

	return b
}

// Page is one page of a paginated query response. numberOfResults on the
// first page is the total for the whole query; queryToken is absent or
// empty on the last page; result may be absent entirely, which decodes as
// a nil slice.
type Page[T any] struct {
	NumberOfResults int    `json:"numberOfResults"`
	QueryToken      string `json:"queryToken"`
	Result          []T    `json:"result"`
}

// observerOf returns the client's observer, falling back to progress.Nop
// so callers never nil-check.
func observerOf(c *boomi.Client) progress.Observer {
	if o := c.Observer(); o != nil {
		return o
	}

	return progress.Nop
}

// QueryAll runs POST {entity}/query with the given filter body, follows
// queryMore tokens until they run out, and returns every result. filter
// is the full request body ({"QueryFilter":{…}}); nil or empty selects
// the match-everything filter.
//
// Pagination completes or it fails: any queryMore failure returns an
// error and zero results, and a collected count that disagrees with the
// first page's numberOfResults returns an error wrapping
// boomi.ErrTruncated. Partial results are never passed off as complete.
//
// A PageEvent is emitted per collected page when the client exposes its
// observer (see observerProvider).
func QueryAll[T any](ctx context.Context, c *boomi.Client, entity string, filter json.RawMessage) ([]T, error) {
	if c == nil {
		return nil, errors.New("objects: nil client")
	}

	if entity == "" {
		return nil, errors.New("objects: empty entity")
	}

	obs := observerOf(c)

	page, err := queryFirst[T](ctx, c, entity, filter)
	if err != nil {
		return nil, err
	}

	total := page.NumberOfResults
	results := make([]T, 0, max(total, 0))
	results = append(results, page.Result...)
	token := page.QueryToken
	obs.OnPage(progress.PageEvent{Entity: entity, Done: len(results), Total: total, More: token != ""})

	for token != "" {
		next, moreErr := queryMore[T](ctx, c, entity, token)
		if moreErr != nil {
			return nil, moreErr
		}

		results = append(results, next.Result...)
		token = next.QueryToken
		obs.OnPage(progress.PageEvent{Entity: entity, Done: len(results), Total: total, More: token != ""})
	}

	if len(results) != total {
		return nil, fmt.Errorf("objects: %s query collected %d of %d reported results: %w",
			entity, len(results), total, boomi.ErrTruncated)
	}

	return results, nil
}

// QueryOne runs a single POST {entity}/query and returns the first
// result, or nil when the query matched nothing. It never follows
// queryMore: use it for filters that identify at most one object.
func QueryOne[T any](ctx context.Context, c *boomi.Client, entity string, filter json.RawMessage) (*T, error) {
	if c == nil {
		return nil, errors.New("objects: nil client")
	}

	if entity == "" {
		return nil, errors.New("objects: empty entity")
	}

	page, err := queryFirst[T](ctx, c, entity, filter)
	if err != nil {
		return nil, err
	}

	if len(page.Result) == 0 {
		//nolint:nilnil // documented contract: nil, nil means the query matched nothing — callers nil-check
		return nil, nil
	}

	return &page.Result[0], nil
}

// queryFirst sends the initial POST {entity}/query call.
func queryFirst[T any](ctx context.Context, c *boomi.Client, entity string, filter json.RawMessage) (*Page[T], error) {
	body := []byte(filter)
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte(emptyFilter)
	}

	return doPage[T](ctx, c, boomi.Request{
		Method:      http.MethodPost,
		Path:        []string{entity, "query"},
		Body:        bytes.NewReader(body),
		ContentType: contentTypeJSON,
		Accept:      contentTypeJSON,
		Class:       boomi.ClassRead,
	})
}

// queryMore sends POST {entity}/queryMore. The body is the raw token
// string — not JSON, not quoted — with Content-Type text/plain, exactly
// as the platform requires.
func queryMore[T any](ctx context.Context, c *boomi.Client, entity, token string) (*Page[T], error) {
	return doPage[T](ctx, c, boomi.Request{
		Method:      http.MethodPost,
		Path:        []string{entity, "queryMore"},
		Body:        strings.NewReader(token),
		ContentType: "text/plain",
		Accept:      contentTypeJSON,
		Class:       boomi.ClassRead,
	})
}

// doPage sends req and decodes one page envelope.
func doPage[T any](ctx context.Context, c *boomi.Client, req boomi.Request) (*Page[T], error) {
	resp, err := c.Do(ctx, req)
	if err != nil {
		return nil, err
	}

	if statusErr := checkStatus(resp, req.Method, wirePath(req)); statusErr != nil {
		return nil, statusErr
	}
	defer func() { _ = resp.Body.Close() }()

	var page Page[T]
	if decodeErr := json.NewDecoder(resp.Body).Decode(&page); decodeErr != nil {
		return nil, fmt.Errorf("objects: decoding %s response: %w", wirePath(req), decodeErr)
	}

	return &page, nil
}

// wirePath renders the request path for error messages, e.g.
// "Component/abc~5".
func wirePath(req boomi.Request) string {
	return strings.Join(req.Path, "/") + req.RawSuffix
}

// maxErrorBody caps how much of a failed response body is retained in an
// APIError, matching the root package's cap.
const maxErrorBody = 64 << 10

// checkStatus turns a non-2xx response into a *boomi.APIError, consuming
// and closing the body. It returns nil for 2xx and leaves the body open
// for the caller. It is defensive: if the root Client.Do already converts
// non-2xx responses into errors, this never fires.
func checkStatus(resp *boomi.Response, method, path string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

	apiErr := &boomi.APIError{
		StatusCode: resp.StatusCode,
		Method:     method,
		Path:       path,
		Body:       body,
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "json") {
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			apiErr.JSON = m
		}
	}

	return apiErr
}
