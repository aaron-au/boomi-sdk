package objects

import (
	"context"
	"fmt"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// Environment is one deployment environment.
type Environment struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Classification string `json:"classification"`
}

// EnvironmentAtomAttachment links a runtime to an environment.
type EnvironmentAtomAttachment struct {
	ID            string `json:"id"`
	AtomID        string `json:"atomId"`
	EnvironmentID string `json:"environmentId"`
}

// Environments accesses Environment objects and their runtime
// attachments.
type Environments struct {
	c *boomi.Client
}

// NewEnvironments returns an Environments service over c.
func NewEnvironments(c *boomi.Client) Environments {
	return Environments{c: c}
}

// All returns every environment in the account.
func (e Environments) All(ctx context.Context) ([]Environment, error) {
	return QueryAll[Environment](ctx, e.c, "Environment", nil)
}

// Get returns one environment: GET Environment/{id}.
func (e Environments) Get(ctx context.Context, id string) (Environment, error) {
	if id == "" {
		return Environment{}, errEmptyID("environment")
	}

	return getJSON[Environment](ctx, e.c, "Environment", id)
}

// ByName returns the environment with exactly this name, or nil when no
// such environment exists.
func (e Environments) ByName(ctx context.Context, name string) (*Environment, error) {
	return QueryOne[Environment](ctx, e.c, "Environment", mustFilter(query.Eq("name", name)))
}

// ForAtom resolves the environment a runtime is attached to, via
// EnvironmentAtomAttachment.
func (e Environments) ForAtom(ctx context.Context, atomID string) (Environment, error) {
	if atomID == "" {
		return Environment{}, errEmptyID("atom")
	}

	link, err := QueryOne[EnvironmentAtomAttachment](
		ctx, e.c, "EnvironmentAtomAttachment", mustFilter(query.Eq("atomId", atomID)),
	)
	if err != nil {
		return Environment{}, err
	}

	if link == nil {
		return Environment{}, fmt.Errorf("objects: runtime %s is not attached to an environment", atomID)
	}

	return e.Get(ctx, link.EnvironmentID)
}

// Attachments returns every runtime-to-environment attachment in the
// account.
func (e Environments) Attachments(ctx context.Context) ([]EnvironmentAtomAttachment, error) {
	return QueryAll[EnvironmentAtomAttachment](ctx, e.c, "EnvironmentAtomAttachment", nil)
}
