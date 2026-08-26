package boomi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aaron-au/boomi-sdk/pace"
)

func TestAPIErrorKind(t *testing.T) {
	cases := []struct {
		status int
		want   Kind
	}{
		{401, KindAuth},
		{403, KindAuth},
		{409, KindConflict},
		{400, KindValidation},
		{404, KindValidation},
		{422, KindValidation},
		{429, KindTransport},
		{500, KindTransport},
		{502, KindTransport},
		{503, KindTransport},
	}
	for _, tc := range cases {
		e := &APIError{StatusCode: tc.status}
		if got := e.Kind(); got != tc.want {
			t.Errorf("Kind(%d) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestKindOf(t *testing.T) {
	wrapped := fmt.Errorf("query failed: %w", &APIError{StatusCode: 401, Method: "GET", Path: "/Component/x"})

	cases := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil", nil, KindUnknown},
		{"wrapped APIError", wrapped, KindAuth},
		{"ErrAuth", fmt.Errorf("call: %w", ErrAuth), KindAuth},
		{"ErrCircuitOpen", ErrCircuitOpen, KindAuth},
		{"pace.ErrOpen", fmt.Errorf("do: %w", pace.ErrOpen), KindAuth},
		{"ErrTruncated", fmt.Errorf("query-all: %w", ErrTruncated), KindTransport},
		{"deadline", context.DeadlineExceeded, KindTransport},
		{"canceled", context.Canceled, KindTransport},
		{"plain error", errors.New("something else"), KindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := KindOf(tc.err); got != tc.want {
				t.Fatalf("KindOf = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAPIErrorError(t *testing.T) {
	e := &APIError{
		StatusCode: 400,
		Method:     "POST",
		Path:       "/ComponentMetadata/query",
		Body:       []byte("{\n  \"message\": \"bad filter\"\n}"),
	}

	msg := e.Error()
	for _, want := range []string{"POST", "/ComponentMetadata/query", "400", "bad filter"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, missing %q", msg, want)
		}
	}

	if strings.Contains(msg, "\n") {
		t.Errorf("Error() contains a newline: %q", msg)
	}
}

func TestIsBranchUnlicensed(t *testing.T) {
	body := []byte(`{"message":"Account acme does not have access rights to Branch"}`)
	if !IsBranchUnlicensed(&APIError{StatusCode: 403, Body: body}) {
		t.Error("403 with access-rights body not detected")
	}

	if !IsBranchUnlicensed(fmt.Errorf("wrap: %w", &APIError{StatusCode: 400, Body: body})) {
		t.Error("wrapped 400 with access-rights body not detected")
	}

	if IsBranchUnlicensed(&APIError{StatusCode: 404, Body: body}) {
		t.Error("404 wrongly detected")
	}

	if IsBranchUnlicensed(&APIError{StatusCode: 403, Body: []byte("forbidden")}) {
		t.Error("wrong body wrongly detected")
	}

	if IsBranchUnlicensed(errors.New("not an APIError")) {
		t.Error("non-APIError wrongly detected")
	}
}

func TestIsDuplicateDeploy(t *testing.T) {
	if !IsDuplicateDeploy(&APIError{StatusCode: 400, Body: []byte("Duplicate deployment exists")}) {
		t.Error("case-insensitive duplicate not detected")
	}

	if !IsDuplicateDeploy(&APIError{StatusCode: 400, Body: []byte("this is a duplicate")}) {
		t.Error("lowercase duplicate not detected")
	}

	if IsDuplicateDeploy(&APIError{StatusCode: 409, Body: []byte("duplicate")}) {
		t.Error("non-400 wrongly detected")
	}

	if IsDuplicateDeploy(&APIError{StatusCode: 400, Body: []byte("something else")}) {
		t.Error("wrong body wrongly detected")
	}

	if IsDuplicateDeploy(errors.New("nope")) {
		t.Error("non-APIError wrongly detected")
	}
}

func TestIsLogNotReady(t *testing.T) {
	if !IsLogNotReady(&APIError{StatusCode: 400, Body: []byte("The execution id supplied is invalid")}) {
		t.Error("log-not-ready shape not detected")
	}

	if IsLogNotReady(&APIError{StatusCode: 404, Body: []byte("is invalid")}) {
		t.Error("non-400 wrongly detected")
	}

	if IsLogNotReady(&APIError{StatusCode: 400, Body: []byte("no log yet")}) {
		t.Error("wrong body wrongly detected")
	}

	if IsLogNotReady(errors.New("nope")) {
		t.Error("non-APIError wrongly detected")
	}
}

func TestKindString(t *testing.T) {
	cases := map[Kind]string{
		KindUnknown:    "unknown",
		KindValidation: "validation",
		KindAuth:       "auth",
		KindConflict:   "conflict",
		KindTransport:  "transport",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}
