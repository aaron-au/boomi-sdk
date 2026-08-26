package objects

import (
	"context"
	"encoding/json"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// MapExtensionSummary is one extensible map attached to an environment.
//
// Environment extensions cover connections and properties; map extensions
// are a separate object entirely, and the most commonly missed thing in a
// hand-run environment copy because nothing in the extensions view hints
// they exist. This is the discovery step: the platform will not serve an
// EnvironmentMapExtension without an id, and the ids only come from here.
type MapExtensionSummary struct {
	ID               string `json:"id"`
	EnvironmentID    string `json:"environmentId"`
	ExtensionGroupID string `json:"extensionGroupId"`
	ProcessID        string `json:"processId"`
	MapID            string `json:"mapId"`
	Name             string `json:"name"`
}

// MapExtension is one environment's overrides for a single map. The
// nested mapping and function shapes are kept raw: what most callers need
// is which maps carry overrides and how many, and the detail is preserved
// verbatim for anything that needs more.
type MapExtension struct {
	ID               string `json:"id"`
	EnvironmentID    string `json:"environmentId"`
	ProcessID        string `json:"processId"`
	MapID            string `json:"mapId"`
	Name             string `json:"name"`
	ExtendedMappings *struct {
		Mapping []json.RawMessage `json:"mapping"`
	} `json:"ExtendedMappings"`
	ExtendedFunctions *struct {
		Function []json.RawMessage `json:"function"`
	} `json:"ExtendedFunctions"`
}

// Overrides counts the mappings this environment overrides.
func (m MapExtension) Overrides() int {
	if m.ExtendedMappings == nil {
		return 0
	}

	return len(m.ExtendedMappings.Mapping)
}

// Functions counts the user-defined functions this environment overrides.
func (m MapExtension) Functions() int {
	if m.ExtendedFunctions == nil {
		return 0
	}

	return len(m.ExtendedFunctions.Function)
}

// MapExtensions accesses environment map extension objects.
type MapExtensions struct {
	c *boomi.Client
}

// NewMapExtensions returns a MapExtensions service over c.
func NewMapExtensions(c *boomi.Client) MapExtensions {
	return MapExtensions{c: c}
}

// Summaries lists the extensible maps attached to an environment:
// EnvironmentMapExtensionsSummary/query.
func (m MapExtensions) Summaries(ctx context.Context, environmentID string) ([]MapExtensionSummary, error) {
	if environmentID == "" {
		return nil, errEmptyID("environment")
	}

	return QueryAll[MapExtensionSummary](
		ctx, m.c, "EnvironmentMapExtensionsSummary", mustFilter(query.Eq("environmentId", environmentID)),
	)
}

// Get reads one environment's overrides for a single map:
// GET EnvironmentMapExtension/{id}. Ids come from Summaries.
func (m MapExtensions) Get(ctx context.Context, id string) (MapExtension, error) {
	if id == "" {
		return MapExtension{}, errEmptyID("map extension")
	}

	return getJSON[MapExtension](ctx, m.c, "EnvironmentMapExtension", id)
}
