package objects

import (
	"context"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// ComponentReference is one edge of the component dependency graph: the
// component at ParentComponentID (at ParentVersion) references
// ComponentID.
type ComponentReference struct {
	ParentComponentID string `json:"parentComponentId"`
	ParentVersion     int    `json:"parentVersion"`
	ComponentID       string `json:"componentId"`
	ReferenceType     string `json:"type"`
	BranchID          string `json:"branchId"`
}

// References queries the component dependency graph.
type References struct {
	c *boomi.Client
}

// NewReferences returns a References service over c.
func NewReferences(c *boomi.Client) References {
	return References{c: c}
}

// Of returns what a component depends on: every reference whose parent is
// this component id.
func (r References) Of(ctx context.Context, componentID string) ([]ComponentReference, error) {
	if componentID == "" {
		return nil, errEmptyID("component")
	}

	return QueryAll[ComponentReference](
		ctx, r.c, "ComponentReference", mustFilter(query.Eq("parentComponentId", componentID)),
	)
}

// To returns what depends on a component: every reference pointing at
// this component id.
func (r References) To(ctx context.Context, componentID string) ([]ComponentReference, error) {
	if componentID == "" {
		return nil, errEmptyID("component")
	}

	return QueryAll[ComponentReference](
		ctx, r.c, "ComponentReference", mustFilter(query.Eq("componentId", componentID)),
	)
}
