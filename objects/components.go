package objects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	boomi "github.com/aaron-au/boomi-sdk"
)

// componentEntity is the platform object name for components.
const componentEntity = "Component"

// contentTypeXML is the media type for component XML bodies.
const contentTypeXML = "application/xml"

// Components accesses Component objects. Bodies are opaque XML: every
// method returns or accepts a raw stream and never parses it. The caller
// owns each returned io.ReadCloser and must close it.
type Components struct {
	c *boomi.Client
}

// NewComponents returns a Components service over c.
func NewComponents(c *boomi.Client) Components {
	return Components{c: c}
}

// validateComponentID rejects empty ids and ids containing '~': the tilde
// forms are reached only through GetVersion and GetOnBranch, never by
// smuggling a suffix into the id.
func validateComponentID(id string) error {
	if id == "" {
		return errors.New("objects: component id is empty")
	}

	if strings.Contains(id, "~") {
		return fmt.Errorf("objects: component id %q contains '~'; use GetVersion or GetOnBranch for tilde forms", id)
	}

	return nil
}

// Get returns the current component XML: GET Component/{id}.
func (s Components) Get(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := validateComponentID(id); err != nil {
		return nil, err
	}

	return s.stream(ctx, boomi.Request{
		Method: http.MethodGet,
		Path:   []string{componentEntity, id},
		Accept: contentTypeXML,
		Class:  boomi.ClassRead,
	})
}

// GetVersion returns a specific saved version of the component:
// GET Component/{id}~{version}. version must be >= 1. The tilde reaches
// the wire unescaped via the request's RawSuffix.
func (s Components) GetVersion(ctx context.Context, id string, version int) (io.ReadCloser, error) {
	if err := validateComponentID(id); err != nil {
		return nil, err
	}

	if version < 1 {
		return nil, fmt.Errorf("objects: component version %d is invalid; versions are positive integers", version)
	}

	return s.stream(ctx, boomi.Request{
		Method:    http.MethodGet,
		Path:      []string{componentEntity, id},
		RawSuffix: "~" + strconv.Itoa(version),
		Accept:    contentTypeXML,
		Class:     boomi.ClassRead,
	})
}

// GetOnBranch returns the component as it exists on a branch:
// GET Component/{id}~{branchId}. Branch ids are base64 strings starting
// "Qjo"; anything else is rejected before touching the wire. The tilde
// reaches the wire unescaped via the request's RawSuffix.
func (s Components) GetOnBranch(ctx context.Context, id, branchID string) (io.ReadCloser, error) {
	if err := validateComponentID(id); err != nil {
		return nil, err
	}

	if !strings.HasPrefix(branchID, "Qjo") {
		return nil, fmt.Errorf(
			"objects: branch id %q is invalid; branch ids are base64 strings starting \"Qjo\"",
			branchID,
		)
	}

	return s.stream(ctx, boomi.Request{
		Method:    http.MethodGet,
		Path:      []string{componentEntity, id},
		RawSuffix: "~" + branchID,
		Accept:    contentTypeXML,
		Class:     boomi.ClassRead,
	})
}

// Create creates a component from the XML stream: POST Component. The
// body is streamed to the wire untouched; the response is the platform's
// XML for the created component.
func (s Components) Create(ctx context.Context, xml io.Reader) (io.ReadCloser, error) {
	if xml == nil {
		return nil, errors.New("objects: component create body is nil")
	}

	return s.stream(ctx, boomi.Request{
		Method:      http.MethodPost,
		Path:        []string{componentEntity},
		Body:        xml,
		ContentType: contentTypeXML,
		Accept:      contentTypeXML,
		Class:       boomi.ClassWrite,
	})
}

// Update updates the component from the XML stream: POST Component/{id}.
// The body is streamed to the wire untouched; the response is the
// platform's XML for the new version.
func (s Components) Update(ctx context.Context, id string, xml io.Reader) (io.ReadCloser, error) {
	if err := validateComponentID(id); err != nil {
		return nil, err
	}

	if xml == nil {
		return nil, errors.New("objects: component update body is nil")
	}

	return s.stream(ctx, boomi.Request{
		Method:      http.MethodPost,
		Path:        []string{componentEntity, id},
		Body:        xml,
		ContentType: contentTypeXML,
		Accept:      contentTypeXML,
		Class:       boomi.ClassWrite,
	})
}

// stream sends req and hands back the raw response body.
func (s Components) stream(ctx context.Context, req boomi.Request) (io.ReadCloser, error) {
	resp, err := s.c.Do(ctx, req)
	if err != nil {
		return nil, err
	}

	if statusErr := checkStatus(resp, req.Method, wirePath(req)); statusErr != nil {
		return nil, statusErr
	}

	return resp.Body, nil
}
