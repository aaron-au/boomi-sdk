package query

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// mustMarshal marshals f through the standard json.Marshal entry point,
// which exercises MarshalJSON the way a request encoder would.
func mustMarshal(t *testing.T, f Filter) string {
	t.Helper()

	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal filter: %v", err)
	}

	return string(b)
}

func envelope(expr string) string {
	return `{"QueryFilter":{"expression":` + expr + `}}`
}

func TestOperatorGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr Expression
		want string // expression JSON inside the envelope, byte-exact
	}{
		{
			name: "Eq",
			expr: Eq("name", "value"),
			want: `{"operator":"EQUALS","property":"name","argument":["value"]}`,
		},
		{
			name: "NotEq",
			expr: NotEq("type", "process"),
			want: `{"operator":"NOT_EQUALS","property":"type","argument":["process"]}`,
		},
		{
			name: "Like",
			expr: Like("name", "%Invoice%"),
			want: `{"operator":"LIKE","property":"name","argument":["%Invoice%"]}`,
		},
		{
			name: "Contains",
			expr: Contains("name", "Order"),
			want: `{"operator":"CONTAINS","property":"name","argument":["Order"]}`,
		},
		{
			name: "Gt",
			expr: Gt("executionTime", "1000"),
			want: `{"operator":"GREATER_THAN","property":"executionTime","argument":["1000"]}`,
		},
		{
			name: "Gte",
			expr: Gte("executionTime", "1000"),
			want: `{"operator":"GREATER_THAN_OR_EQUAL","property":"executionTime","argument":["1000"]}`,
		},
		{
			name: "Lt",
			expr: Lt("executionTime", "1000"),
			want: `{"operator":"LESS_THAN","property":"executionTime","argument":["1000"]}`,
		},
		{
			name: "Lte",
			expr: Lte("executionTime", "1000"),
			want: `{"operator":"LESS_THAN_OR_EQUAL","property":"executionTime","argument":["1000"]}`,
		},
		{
			name: "IsNull",
			expr: IsNull("folderId"),
			want: `{"operator":"IS_NULL","property":"folderId"}`,
		},
		{
			name: "IsNotNull",
			expr: IsNotNull("folderId"),
			want: `{"operator":"IS_NOT_NULL","property":"folderId"}`,
		},
		{
			name: "EqBool true",
			expr: EqBool("deleted", true),
			want: `{"operator":"EQUALS","property":"deleted","argument":["true"]}`,
		},
		{
			name: "EqBool false",
			expr: EqBool("deleted", false),
			want: `{"operator":"EQUALS","property":"deleted","argument":["false"]}`,
		},
		{
			name: "empty string value still emits argument",
			expr: Eq("name", ""),
			want: `{"operator":"EQUALS","property":"name","argument":[""]}`,
		},
		{
			name: "quote in value stays valid JSON",
			expr: Eq("name", `O"Brien`),
			want: `{"operator":"EQUALS","property":"name","argument":["O\"Brien"]}`,
		},
		{
			name: "And of two",
			expr: And(EqBool("deleted", false), Like("name", "%A%")),
			want: `{"operator":"and","nestedExpression":[` +
				`{"operator":"EQUALS","property":"deleted","argument":["false"]},` +
				`{"operator":"LIKE","property":"name","argument":["%A%"]}]}`,
		},
		{
			name: "Or of two",
			expr: Or(Eq("id", "a"), Eq("id", "b")),
			want: `{"operator":"or","nestedExpression":[` +
				`{"operator":"EQUALS","property":"id","argument":["a"]},` +
				`{"operator":"EQUALS","property":"id","argument":["b"]}]}`,
		},
		{
			name: "In of two is an or of Eq",
			expr: In("folderId", "a", "b"),
			want: `{"operator":"or","nestedExpression":[` +
				`{"operator":"EQUALS","property":"folderId","argument":["a"]},` +
				`{"operator":"EQUALS","property":"folderId","argument":["b"]}]}`,
		},
		{
			name: "And of one collapses",
			expr: And(Eq("name", "x")),
			want: `{"operator":"EQUALS","property":"name","argument":["x"]}`,
		},
		{
			name: "Or of one collapses",
			expr: Or(IsNull("folderId")),
			want: `{"operator":"IS_NULL","property":"folderId"}`,
		},
		{
			name: "In of one collapses to Eq",
			expr: In("folderId", "only"),
			want: `{"operator":"EQUALS","property":"folderId","argument":["only"]}`,
		},
		{
			name: "nested single-element group collapses in place",
			expr: And(Eq("a", "1"), Or(Eq("b", "2"))),
			want: `{"operator":"and","nestedExpression":[` +
				`{"operator":"EQUALS","property":"a","argument":["1"]},` +
				`{"operator":"EQUALS","property":"b","argument":["2"]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mustMarshal(t, Filter{Expression: tt.expr})
			if want := envelope(tt.want); got != want {
				t.Errorf("marshal mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestIsNullOmitsArgumentKey(t *testing.T) {
	t.Parallel()

	for _, expr := range []Expression{IsNull("p"), IsNotNull("p")} {
		got := mustMarshal(t, Filter{Expression: expr})
		if strings.Contains(got, "argument") {
			t.Errorf("null-check expression must omit the argument key entirely, got: %s", got)
		}
	}
}

func TestZeroFilter(t *testing.T) {
	t.Parallel()

	const want = `{"QueryFilter":{}}`

	if got := mustMarshal(t, Filter{}); got != want {
		t.Errorf("zero Filter = %s, want %s", got, want)
	}

	raw, err := Filter{}.JSON()
	if err != nil {
		t.Fatalf("zero Filter JSON(): %v", err)
	}

	if string(raw) != want {
		t.Errorf("zero Filter JSON() = %s, want %s", raw, want)
	}
}

func TestJSONMatchesMarshalJSON(t *testing.T) {
	t.Parallel()

	f := Filter{Expression: And(EqBool("deleted", false), Like("name", "%X%"))}

	viaMarshal := mustMarshal(t, f)

	raw, err := f.JSON()
	if err != nil {
		t.Fatalf("JSON(): %v", err)
	}

	if string(raw) != viaMarshal {
		t.Errorf("JSON() = %s, json.Marshal = %s", raw, viaMarshal)
	}
}

// TestRealWorldNestedShape reproduces a verified platform query body:
// currentVersion and deleted flags, an OR over two folder ids, and a LIKE
// on the name — byte-exact, then semantically via unmarshal-compare.
func TestRealWorldNestedShape(t *testing.T) {
	t.Parallel()

	f := Filter{Expression: And(
		EqBool("currentVersion", true),
		EqBool("deleted", false),
		Or(
			Eq("folderId", "Rjo1MjM0"),
			Eq("folderId", "Rjo1MjM1"),
		),
		Like("name", "%Invoice%"),
	)}

	want := envelope(`{"operator":"and","nestedExpression":[` +
		`{"operator":"EQUALS","property":"currentVersion","argument":["true"]},` +
		`{"operator":"EQUALS","property":"deleted","argument":["false"]},` +
		`{"operator":"or","nestedExpression":[` +
		`{"operator":"EQUALS","property":"folderId","argument":["Rjo1MjM0"]},` +
		`{"operator":"EQUALS","property":"folderId","argument":["Rjo1MjM1"]}]},` +
		`{"operator":"LIKE","property":"name","argument":["%Invoice%"]}]}`)

	got := mustMarshal(t, f)
	if got != want {
		t.Errorf("byte-exact mismatch\n got: %s\nwant: %s", got, want)
	}

	// Semantic comparison: field order within objects is irrelevant on
	// the wire, so also compare decoded structures.
	var gotDoc, wantDoc any
	if err := json.Unmarshal([]byte(got), &gotDoc); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}

	if err := json.Unmarshal([]byte(want), &wantDoc); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}

	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Errorf("semantic mismatch\n got: %#v\nwant: %#v", gotDoc, wantDoc)
	}
}

func TestMarshalErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expr    Expression
		wantSub string
	}{
		{name: "empty And", expr: And(), wantSub: "And requires at least one expression"},
		{name: "empty Or", expr: Or(), wantSub: "Or requires at least one expression"},
		{name: "empty In", expr: In("folderId"), wantSub: "In requires at least one expression"},
		{name: "empty property simple", expr: Eq("", "v"), wantSub: "empty property"},
		{name: "empty property null check", expr: IsNull(""), wantSub: "empty property"},
		{
			name:    "empty property nested in group",
			expr:    And(Eq("ok", "1"), Like("", "%x%")),
			wantSub: "empty property",
		},
		{
			name:    "empty group nested in group",
			expr:    And(Eq("ok", "1"), Or()),
			wantSub: "Or requires at least one expression",
		},
		{name: "nil expression in group", expr: And(Eq("ok", "1"), nil), wantSub: "nil expression"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := Filter{Expression: tt.expr}

			if _, err := json.Marshal(f); err == nil {
				t.Fatal("json.Marshal succeeded, want error")
			} else if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not mention %q", err, tt.wantSub)
			}

			if _, err := f.JSON(); err == nil {
				t.Error("JSON() succeeded, want error")
			}
		})
	}
}

// TestOutputAlwaysValidJSON marshals hostile fixed inputs — the exact bug
// class that breaks string-interpolated filters — and checks the body is
// valid JSON carrying the original value.
func TestOutputAlwaysValidJSON(t *testing.T) {
	t.Parallel()

	values := []string{
		`plain`,
		`He said "hello"`,
		`back\slash`,
		`{"operator":"EQUALS"}`,
		"tab\tnewline\ncarriage\r",
		"nul\x00byte",
		`名前 — Ürün ✓`,
		`<script>&amp;</script>`,
	}

	for _, v := range values {
		got := mustMarshal(t, Filter{Expression: Eq("name", v)})
		if !json.Valid([]byte(got)) {
			t.Fatalf("invalid JSON for value %q: %s", v, got)
		}

		var doc struct {
			QueryFilter struct {
				Expression struct {
					Argument []string `json:"argument"`
				} `json:"expression"`
			} `json:"QueryFilter"`
		}

		if err := json.Unmarshal([]byte(got), &doc); err != nil {
			t.Fatalf("round-trip for value %q: %v", v, err)
		}

		if len(doc.QueryFilter.Expression.Argument) != 1 || doc.QueryFilter.Expression.Argument[0] != v {
			t.Errorf("value %q did not round-trip: %#v", v, doc.QueryFilter.Expression.Argument)
		}
	}
}
