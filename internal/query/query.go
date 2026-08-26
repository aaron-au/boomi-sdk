// Package query builds JSON QueryFilter bodies for the Boomi Platform
// API's query endpoints.
//
// The platform accepts a filter envelope of the form
// {"QueryFilter":{"expression":...}}. Building that body by string
// interpolation breaks the moment a property or value contains a quote
// character; this package builds it from typed expressions instead, so any
// value — quotes, backslashes, unicode, control characters — marshals to
// valid JSON.
//
// Wire grammar, verified against the platform:
//
//   - A simple expression is {"operator":"EQUALS","property":p,"argument":[v]}.
//     The operator is uppercase and the argument is always an array of
//     strings; IS_NULL and IS_NOT_NULL omit the argument key entirely.
//   - A grouping expression is {"operator":"and","nestedExpression":[...]}
//     with a lowercase "and" or "or" operator. The platform may reject a
//     one-element nestedExpression, so single-element groups collapse to
//     the bare inner expression.
//   - The zero Filter marshals to {"QueryFilter":{}} exactly — the
//     list-all filter carries no "expression" key.
package query

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Expression is one node of a query filter: a comparison built by Eq,
// Like, IsNull and friends, or a grouping built by And, Or and In. The
// interface is sealed — only this package's constructors produce values
// that satisfy it — so an Expression cannot hold an unrepresentable state.
type Expression interface {
	// encode renders the node as wire JSON. Being unexported, it seals
	// the interface to this package.
	encode() (json.RawMessage, error)
}

// Eq matches records whose property equals value exactly.
func Eq(property, value string) Expression {
	return simpleExpression{operator: "EQUALS", property: property, argument: []string{value}}
}

// NotEq matches records whose property differs from value.
func NotEq(property, value string) Expression {
	return simpleExpression{operator: "NOT_EQUALS", property: property, argument: []string{value}}
}

// Like matches records whose property matches a pattern with % wildcards
// (for example "%Invoice%"). Matching is case-insensitive on the platform
// side.
func Like(property, value string) Expression {
	return simpleExpression{operator: "LIKE", property: property, argument: []string{value}}
}

// Contains matches records whose property contains value as a substring.
func Contains(property, value string) Expression {
	return simpleExpression{operator: "CONTAINS", property: property, argument: []string{value}}
}

// Gt matches records whose property is greater than value. Numeric values
// are passed as decimal strings; the platform compares them by the
// property's type.
func Gt(property, value string) Expression {
	return simpleExpression{operator: "GREATER_THAN", property: property, argument: []string{value}}
}

// Gte matches records whose property is greater than or equal to value.
// Numeric values are passed as decimal strings.
func Gte(property, value string) Expression {
	return simpleExpression{operator: "GREATER_THAN_OR_EQUAL", property: property, argument: []string{value}}
}

// Lt matches records whose property is less than value. Numeric values
// are passed as decimal strings.
func Lt(property, value string) Expression {
	return simpleExpression{operator: "LESS_THAN", property: property, argument: []string{value}}
}

// Lte matches records whose property is less than or equal to value.
// Numeric values are passed as decimal strings.
func Lte(property, value string) Expression {
	return simpleExpression{operator: "LESS_THAN_OR_EQUAL", property: property, argument: []string{value}}
}

// IsNull matches records whose property has no value. The wire form
// carries no argument key.
func IsNull(property string) Expression {
	return simpleExpression{operator: "IS_NULL", property: property}
}

// IsNotNull matches records whose property has a value. The wire form
// carries no argument key.
func IsNotNull(property string) Expression {
	return simpleExpression{operator: "IS_NOT_NULL", property: property}
}

// EqBool matches records whose boolean property equals v, passing "true"
// or "false" as the platform expects.
func EqBool(property string, v bool) Expression {
	return Eq(property, strconv.FormatBool(v))
}

// In matches records whose property equals any of values — an OR of Eq
// comparisons. A single value collapses to a bare Eq; no values is an
// error at marshal time.
func In(property string, values ...string) Expression {
	nested := make([]Expression, len(values))
	for i, v := range values {
		nested[i] = Eq(property, v)
	}

	return groupingExpression{operator: "or", name: "In", nested: nested}
}

// And matches records satisfying every expression. A single expression
// collapses to that expression alone; none is an error at marshal time.
func And(exprs ...Expression) Expression {
	return groupingExpression{operator: "and", name: "And", nested: exprs}
}

// Or matches records satisfying at least one expression. A single
// expression collapses to that expression alone; none is an error at
// marshal time.
func Or(exprs ...Expression) Expression {
	return groupingExpression{operator: "or", name: "Or", nested: exprs}
}

// Filter is the QueryFilter envelope a query endpoint accepts as its
// request body. The zero value marshals to {"QueryFilter":{}}, the
// list-all filter.
type Filter struct {
	Expression Expression
}

// emptyFilterJSON is the exact list-all body: no "expression" key at all.
const emptyFilterJSON = `{"QueryFilter":{}}`

// MarshalJSON renders the filter as a Platform API request body. It
// returns an error for a group or In with no members or an expression
// with an empty property.
func (f Filter) MarshalJSON() ([]byte, error) {
	if f.Expression == nil {
		return []byte(emptyFilterJSON), nil
	}

	expr, err := f.Expression.encode()
	if err != nil {
		return nil, err
	}

	b, err := json.Marshal(envelopeWire{QueryFilter: filterWire{Expression: expr}})
	if err != nil {
		return nil, fmt.Errorf("query: marshal filter envelope: %w", err)
	}

	return b, nil
}

// JSON marshals the filter and returns it as a json.RawMessage, ready to
// hand to a query call as its request body.
func (f Filter) JSON() (json.RawMessage, error) {
	b, err := f.MarshalJSON()
	if err != nil {
		return nil, err
	}

	return json.RawMessage(b), nil
}

// simpleExpression is a single property comparison. A nil argument means
// the operator takes no argument key at all (IS_NULL, IS_NOT_NULL); every
// other operator carries at least one argument string, so an empty-string
// value still marshals as [""].
type simpleExpression struct {
	operator string
	property string
	argument []string
}

// simpleWire is the wire form of a simpleExpression. Argument is a
// pointer so that a present-but-single-empty-string argument [""] is
// emitted while an absent argument omits the key entirely.
type simpleWire struct {
	Operator string    `json:"operator"`
	Property string    `json:"property"`
	Argument *[]string `json:"argument,omitempty"`
}

func (e simpleExpression) encode() (json.RawMessage, error) {
	if e.property == "" {
		return nil, fmt.Errorf("query: %s expression has an empty property", e.operator)
	}

	w := simpleWire{Operator: e.operator, Property: e.property}
	if e.argument != nil {
		w.Argument = &e.argument
	}

	b, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("query: marshal %s expression: %w", e.operator, err)
	}

	return b, nil
}

// groupingExpression combines nested expressions with a lowercase "and"
// or "or" operator. name records the constructor (And, Or, In) for error
// messages.
type groupingExpression struct {
	operator string
	name     string
	nested   []Expression
}

// groupingWire is the wire form of a groupingExpression.
type groupingWire struct {
	Operator         string            `json:"operator"`
	NestedExpression []json.RawMessage `json:"nestedExpression"`
}

func (e groupingExpression) encode() (json.RawMessage, error) {
	switch len(e.nested) {
	case 0:
		return nil, fmt.Errorf("query: %s requires at least one expression", e.name)
	case 1:
		// The platform may reject a one-element nestedExpression, so a
		// single-element group collapses to its inner expression.
		return encodeMember(e.nested[0], e.name)
	}

	nested := make([]json.RawMessage, len(e.nested))

	for i, member := range e.nested {
		raw, err := encodeMember(member, e.name)
		if err != nil {
			return nil, err
		}

		nested[i] = raw
	}

	b, err := json.Marshal(groupingWire{Operator: e.operator, NestedExpression: nested})
	if err != nil {
		return nil, fmt.Errorf("query: marshal %s group: %w", e.name, err)
	}

	return b, nil
}

// encodeMember encodes one member of a group, rejecting a nil Expression
// with an error naming the enclosing constructor.
func encodeMember(member Expression, group string) (json.RawMessage, error) {
	if member == nil {
		return nil, fmt.Errorf("query: %s contains a nil expression", group)
	}

	return member.encode()
}

// envelopeWire is the outer request body: {"QueryFilter":{...}}.
type envelopeWire struct {
	QueryFilter filterWire `json:"QueryFilter"`
}

// filterWire carries the expression inside the envelope. Expression is
// never empty here — the no-expression case short-circuits to
// emptyFilterJSON before this type is used.
type filterWire struct {
	Expression json.RawMessage `json:"expression"`
}
