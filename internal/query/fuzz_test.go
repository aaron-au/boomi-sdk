package query

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// jsonCoerce mirrors what encoding/json does to a Go string on the wire:
// valid UTF-8 passes through, and each invalid byte becomes U+FFFD.
// Ranging over a string yields exactly that replacement per invalid byte.
func jsonCoerce(s string) string {
	if utf8.ValidString(s) {
		return s
	}

	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
	}

	return b.String()
}

// FuzzFilterJSON asserts the core guarantee of this package: whatever
// bytes appear in a property or value — quotes, backslashes, unicode,
// control characters — the marshalled filter is valid JSON and the value
// round-trips intact. This is exactly the bug class that breaks
// string-interpolated filter bodies (a quote in a branch name producing
// invalid JSON).
func FuzzFilterJSON(f *testing.F) {
	f.Add("name", "value")
	f.Add("name", `He said "hello"`)
	f.Add(`pro"perty`, `back\slash "quote"`)
	f.Add("branch", `feature/"quoted" name`)
	f.Add("名前", "Ürün — ✓   ")
	f.Add("p", "tab\tnewline\nnul\x00")
	f.Add("", "empty property must error")
	f.Add("p", "")

	f.Fuzz(func(t *testing.T, property, value string) {
		filter := Filter{Expression: And(
			Eq(property, value),
			Or(
				Like(property, value),
				IsNull(property),
			),
			NotEq(property, value),
		)}

		data, err := json.Marshal(filter)
		if property == "" {
			if err == nil {
				t.Fatal("empty property must fail to marshal")
			}

			return
		}

		if err != nil {
			t.Fatalf("marshal property=%q value=%q: %v", property, value, err)
		}

		if !json.Valid(data) {
			t.Fatalf("invalid JSON for property=%q value=%q: %s", property, value, data)
		}

		assertRoundTrip(t, data, jsonCoerce(property), jsonCoerce(value))
	})
}

// fuzzNode is the decoded shape of one expression in the fuzz filter.
type fuzzNode struct {
	Operator         string     `json:"operator"`
	Property         string     `json:"property"`
	Argument         *[]string  `json:"argument"`
	NestedExpression []fuzzNode `json:"nestedExpression"`
}

// checkSimple asserts one decoded comparison node: operator, property,
// and either a single-element argument equal to *wantValue or — when
// wantValue is nil — no argument key at all.
func checkSimple(t *testing.T, node fuzzNode, wantOp, wantProperty string, wantValue *string, data []byte) {
	t.Helper()

	if node.Operator != wantOp || node.Property != wantProperty {
		t.Errorf("%s node mismatch for property=%q: %s", wantOp, wantProperty, data)
	}

	if wantValue == nil {
		if node.Argument != nil {
			t.Errorf("%s must omit the argument key: %s", wantOp, data)
		}

		return
	}

	if node.Argument == nil || len(*node.Argument) != 1 || (*node.Argument)[0] != *wantValue {
		t.Errorf("%s value did not round-trip for value=%q: %s", wantOp, *wantValue, data)
	}
}

// assertRoundTrip decodes the marshalled fuzz filter and checks every
// node carries the expected property and value unchanged.
func assertRoundTrip(t *testing.T, data []byte, wantProperty, wantValue string) {
	t.Helper()

	var doc struct {
		QueryFilter struct {
			Expression fuzzNode `json:"expression"`
		} `json:"QueryFilter"`
	}

	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("round-trip: %v", err)
	}

	expr := doc.QueryFilter.Expression
	if expr.Operator != "and" || len(expr.NestedExpression) != 3 {
		t.Fatalf("unexpected top-level shape: %s", data)
	}

	checkSimple(t, expr.NestedExpression[0], "EQUALS", wantProperty, &wantValue, data)

	or := expr.NestedExpression[1]
	if or.Operator != "or" || len(or.NestedExpression) != 2 {
		t.Fatalf("unexpected or-group shape: %s", data)
	}

	checkSimple(t, or.NestedExpression[0], "LIKE", wantProperty, &wantValue, data)
	checkSimple(t, or.NestedExpression[1], "IS_NULL", wantProperty, nil, data)
	checkSimple(t, expr.NestedExpression[2], "NOT_EQUALS", wantProperty, &wantValue, data)
}
