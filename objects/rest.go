package objects

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	boomi "github.com/aaron-au/boomi-sdk"
)

// This file holds the small JSON verbs every typed service is built from:
// GET/POST/DELETE against an object path, with JSON bodies marshalled to a
// seekable reader so the transport can re-send them on retry.

// errEmptyID rejects a service call missing its identifying argument
// before anything touches the wire.
func errEmptyID(what string) error {
	return fmt.Errorf("objects: %s id is empty", what)
}

// getJSON sends GET {path...} and decodes the JSON response into T.
func getJSON[T any](ctx context.Context, c *boomi.Client, path ...string) (T, error) {
	return doJSON[T](ctx, c, boomi.Request{
		Method: http.MethodGet,
		Path:   path,
		Accept: contentTypeJSON,
		Class:  boomi.ClassRead,
	})
}

// postJSON marshals body and sends POST {path...}, decoding the JSON
// response into T. The marshalled body is a bytes.Reader, so the
// transport can rewind and re-send it on retry.
func postJSON[T any](ctx context.Context, c *boomi.Client, body any, path ...string) (T, error) {
	var zero T

	raw, err := json.Marshal(body)
	if err != nil {
		return zero, fmt.Errorf("objects: marshal %s request: %w", wirePath(boomi.Request{Path: path}), err)
	}

	return doJSON[T](ctx, c, boomi.Request{
		Method:      http.MethodPost,
		Path:        path,
		Body:        bytes.NewReader(raw),
		ContentType: contentTypeJSON,
		Accept:      contentTypeJSON,
		Class:       boomi.ClassWrite,
	})
}

// deleteReq sends DELETE {path...} and discards any response body.
func deleteReq(ctx context.Context, c *boomi.Client, path ...string) error {
	req := boomi.Request{
		Method: http.MethodDelete,
		Path:   path,
		Accept: contentTypeJSON,
		Class:  boomi.ClassWrite,
	}

	resp, err := c.Do(ctx, req)
	if err != nil {
		return err
	}

	if statusErr := checkStatus(resp, req.Method, wirePath(req)); statusErr != nil {
		return statusErr
	}

	_ = resp.Body.Close()

	return nil
}

// doJSON sends req and decodes the JSON response into T.
func doJSON[T any](ctx context.Context, c *boomi.Client, req boomi.Request) (T, error) {
	var zero T

	resp, err := c.Do(ctx, req)
	if err != nil {
		return zero, err
	}

	if statusErr := checkStatus(resp, req.Method, wirePath(req)); statusErr != nil {
		return zero, statusErr
	}
	defer func() { _ = resp.Body.Close() }()

	var out T
	if decodeErr := json.NewDecoder(resp.Body).Decode(&out); decodeErr != nil {
		return zero, fmt.Errorf("objects: decoding %s response: %w", wirePath(req), decodeErr)
	}

	return out, nil
}
