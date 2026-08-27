package objects

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	boomi "github.com/aaron-au/boomi-sdk"
)

// Diffs compares saved versions of a component via ComponentDiffRequest.
// The platform's diff document is returned as an opaque stream, matching
// the Components service's treatment of component XML: the SDK moves it,
// the caller interprets it.
type Diffs struct {
	c *boomi.Client
}

// NewDiffs returns a Diffs service over c.
func NewDiffs(c *boomi.Client) Diffs {
	return Diffs{c: c}
}

// diffRequestWire is the POST ComponentDiffRequest body.
type diffRequestWire struct {
	Type          string `json:"@type"`
	ComponentID   string `json:"componentId"`
	SourceVersion int    `json:"sourceVersion"`
	TargetVersion int    `json:"targetVersion"`
}

// Versions compares two saved versions of one component:
// POST ComponentDiffRequest. The caller owns the returned stream and must
// close it.
func (d Diffs) Versions(ctx context.Context, componentID string, source, target int) (io.ReadCloser, error) {
	if componentID == "" {
		return nil, errEmptyID("component")
	}

	if source < 1 || target < 1 {
		return nil, fmt.Errorf("objects: component versions %d and %d are invalid; versions are positive integers",
			source, target)
	}

	raw, err := json.Marshal(diffRequestWire{
		Type:          "ComponentDiffRequest",
		ComponentID:   componentID,
		SourceVersion: source,
		TargetVersion: target,
	})
	if err != nil {
		return nil, fmt.Errorf("objects: marshal ComponentDiffRequest: %w", err)
	}

	req := boomi.Request{
		Method:      http.MethodPost,
		Path:        []string{"ComponentDiffRequest"},
		Body:        bytes.NewReader(raw),
		ContentType: contentTypeJSON,
		Accept:      contentTypeJSON,
		Class:       boomi.ClassRead,
	}

	resp, err := d.c.Do(ctx, req)
	if err != nil {
		return nil, err
	}

	if statusErr := checkStatus(resp, req.Method, wirePath(req)); statusErr != nil {
		return nil, statusErr
	}

	return resp.Body, nil
}
