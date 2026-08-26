package objects

import (
	"context"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// Branch is one row from a Branch/query result.
type Branch struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Deleted bool   `json:"deleted"`
}

// Branches queries Branch objects.
//
// Accounts without the branching feature refuse these calls behind a
// generic 400/403; detect that with boomi.IsBranchUnlicensed on the
// returned error — what to do about it is the caller's decision.
type Branches struct {
	c *boomi.Client
}

// NewBranches returns a Branches service over c.
func NewBranches(c *boomi.Client) Branches {
	return Branches{c: c}
}

// All returns every branch (empty filter). Branch/query supports
// queryMore like any other query, so this uses the same pagination
// engine even though branch counts rarely exceed one page.
func (b Branches) All(ctx context.Context) ([]Branch, error) {
	return QueryAll[Branch](ctx, b.c, "Branch", nil)
}

// ByName returns the branch with exactly this name, or nil when no such
// branch exists.
func (b Branches) ByName(ctx context.Context, name string) (*Branch, error) {
	return QueryOne[Branch](ctx, b.c, "Branch", mustFilter(query.Eq("name", name)))
}
