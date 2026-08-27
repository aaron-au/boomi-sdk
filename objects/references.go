package objects

import (
	"context"
	"strconv"

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

// Of returns what a component depends on at one saved version: every
// reference whose parent is this component id. The version is required
// because the platform rejects a parentComponentId filter that arrives
// without parentVersion (HTTP 400, "parentComponentId should always be
// accompanied by parentVersion").
func (r References) Of(ctx context.Context, componentID string, version int) ([]ComponentReference, error) {
	if componentID == "" {
		return nil, errEmptyID("component")
	}

	return QueryAll[ComponentReference](
		ctx, r.c, "ComponentReference", mustFilter(query.And(
			query.Eq("parentComponentId", componentID),
			query.Eq("parentVersion", strconv.Itoa(version)),
		)),
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
