package objects

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	boomi "github.com/aaron-au/boomi-sdk"
)

// Format selects the wire format for a raw call. The platform serves
// every object in both; the format decides the Accept header and, for
// requests with a body, the Content-Type.
type Format string

// The two formats the platform speaks.
const (
	FormatXML  Format = contentTypeXML
	FormatJSON Format = contentTypeJSON
)

// mediaType returns the header value, defaulting an unset Format to XML —
// the format the platform's own tooling and the Boomi Companion plugin
// speak natively.
func (f Format) mediaType() string {
	if f == "" {
		return contentTypeXML
	}

	return string(f)
}

// Raw is the untyped escape hatch: every typed service in this package
// has a struct-shaped view of one object, and Raw is the same wire with
// no view at all. The caller names the path, picks the format, and owns
// both document streams — nothing is parsed, translated, or reordered in
// between, which is what "the XML as-is" requires.
//
// Requests still go through the full transport: pacing, the auth circuit
// breaker, retry, and the progress observer all apply. The platform's
// tilde forms (Component/{id}~{version}) are not reachable here — path
// segments are escaped individually — use the Components service for
// those.
//
// Pagination is not driven for you: a query answers one page in the
// chosen format, and following queryMore is the caller's business. For
// completed JSON pagination use QueryAll (typed, or with
// json.RawMessage rows).
type Raw struct {
	c *boomi.Client
}

// NewRaw returns a Raw service over c.
func NewRaw(c *boomi.Client) Raw {
	return Raw{c: c}
}

// Get streams GET {path...} in the chosen format. The caller owns the
// stream and must close it.
func (r Raw) Get(ctx context.Context, f Format, path ...string) (io.ReadCloser, error) {
	if err := checkRawPath(path); err != nil {
		return nil, err
	}

	return r.stream(ctx, boomi.Request{
		Method: http.MethodGet,
		Path:   path,
		Accept: f.mediaType(),
		Class:  boomi.ClassRead,
	})
}

// Post streams body to POST {path...} in the chosen format and returns
// the response stream. This is the write door: create, update, and
// action endpoints all take their document exactly as the caller wrote
// it. The caller owns the response stream and must close it.
//
// A body that implements io.Seeker (bytes.Reader, strings.Reader,
// os.File) can be re-sent on retry; any other body is sent at most once.
func (r Raw) Post(ctx context.Context, f Format, body io.Reader, path ...string) (io.ReadCloser, error) {
	if err := checkRawPath(path); err != nil {
		return nil, err
	}

	if body == nil {
		return nil, errors.New("objects: raw post body is nil")
	}

	return r.stream(ctx, boomi.Request{
		Method:      http.MethodPost,
		Path:        path,
		Body:        body,
		ContentType: f.mediaType(),
		Accept:      f.mediaType(),
		Class:       boomi.ClassWrite,
	})
}

// Query streams one page of POST {entity}/query in the chosen format.
// filter is the complete request body in that same format — a JSON
// QueryFilter envelope or an XML QueryConfig document — and nil queries
// everything (an empty JSON envelope or an empty QueryConfig,
// format-matched). Paced as a read, so parallel queries are not
// serialised behind writes.
//
// The response is one page as the platform sent it; a queryMore token
// inside it is the caller's to follow (POST {entity}/queryMore with the
// bare token, Content-Type text/plain).
func (r Raw) Query(ctx context.Context, f Format, entity string, filter io.Reader) (io.ReadCloser, error) {
	if entity == "" {
		return nil, errors.New("objects: empty entity")
	}

	req := boomi.Request{
		Method:      http.MethodPost,
		Path:        []string{entity, querySegment},
		Body:        filter,
		ContentType: f.mediaType(),
		Accept:      f.mediaType(),
		Class:       boomi.ClassRead,
	}
	if filter == nil {
		req.Body, req.ContentType = emptyRawFilter(f)
	}

	return r.stream(ctx, req)
}

// Delete sends DELETE {path...} and discards any response body.
func (r Raw) Delete(ctx context.Context, path ...string) error {
	if err := checkRawPath(path); err != nil {
		return err
	}

	return deleteReq(ctx, r.c, path...)
}

// checkRawPath rejects an empty path before anything touches the wire.
func checkRawPath(path []string) error {
	if len(path) == 0 {
		return errors.New("objects: raw call has no path segments")
	}

	return nil
}

// emptyRawFilter returns the match-everything query body for the format.
func emptyRawFilter(f Format) (body io.Reader, contentType string) {
	if f.mediaType() == contentTypeXML {
		return strings.NewReader(emptyXMLQueryConfig), contentTypeXML
	}

	return strings.NewReader(emptyFilter), contentTypeJSON
}

// emptyXMLQueryConfig is the XML form of the list-all filter, namespaced
// as the platform requires.
const emptyXMLQueryConfig = `<QueryConfig xmlns="http://api.platform.boomi.com/"/>`

// stream sends req and hands back the raw response body.
func (r Raw) stream(ctx context.Context, req boomi.Request) (io.ReadCloser, error) {
	resp, err := r.c.Do(ctx, req)
	if err != nil {
		return nil, err
	}

	if statusErr := checkStatus(resp, req.Method, wirePath(req)); statusErr != nil {
		return nil, statusErr
	}

	return resp.Body, nil
}
