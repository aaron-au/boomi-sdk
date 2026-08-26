package objects

import (
	"context"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// Atom is one runtime: a basic Atom, a Molecule, or a Cloud attachment.
//
// AtomType is topology (ATOM | MOLECULE | CLOUD | CLOUDMOLECULE), not API
// tier — API tier comes from SharedServerInformation.APIType.
type Atom struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	StatusDetail    string `json:"statusDetail"`
	AtomType        string `json:"type"`
	HostName        string `json:"hostName"`
	DateInstalled   string `json:"dateInstalled"`
	CurrentVersion  string `json:"currentVersion"`
	PurgeHistoryDay int    `json:"purgeHistoryDays"`
	CreatedBy       string `json:"createdBy"`

	// Cloud attachment fields — populated only when AtomType is
	// CLOUD or CLOUDMOLECULE.
	CloudID           string `json:"cloudId"`
	CloudName         string `json:"cloudName"`
	CloudOwnerName    string `json:"cloudOwnerName"`
	CloudMoleculeID   string `json:"cloudMoleculeId"`
	InstanceID        string `json:"instanceId"`
	IsCloudAttachment bool   `json:"isCloudAttachment"`
}

// IsCloud reports whether this runtime is hosted in a Boomi or account
// cloud. Cloud attachments do not support RuntimeProperties — use
// AccountCloudAttachmentProperties instead.
func (a Atom) IsCloud() bool {
	return a.IsCloudAttachment || a.AtomType == "CLOUD" || a.AtomType == "CLOUDMOLECULE"
}

// ConnectorVersion is one connector installed on a runtime, as listed on
// the Runtime Management page.
type ConnectorVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// AtomConnectorVersions is the set of connectors a runtime carries.
// Version parity between two runtimes explains behaviour differences
// nothing else accounts for.
type AtomConnectorVersions struct {
	ID               string             `json:"id"`
	ConnectorVersion []ConnectorVersion `json:"ConnectorVersion"`
}

// Atoms accesses runtime (Atom) objects.
type Atoms struct {
	c *boomi.Client
}

// NewAtoms returns an Atoms service over c.
func NewAtoms(c *boomi.Client) Atoms {
	return Atoms{c: c}
}

// All returns every runtime in the account.
func (a Atoms) All(ctx context.Context) ([]Atom, error) {
	return QueryAll[Atom](ctx, a.c, "Atom", nil)
}

// Get returns one runtime: GET Atom/{id}.
func (a Atoms) Get(ctx context.Context, id string) (Atom, error) {
	if id == "" {
		return Atom{}, errEmptyID("atom")
	}

	return getJSON[Atom](ctx, a.c, "Atom", id)
}

// ByName returns the runtime with exactly this name, or nil when no such
// runtime exists.
func (a Atoms) ByName(ctx context.Context, name string) (*Atom, error) {
	return QueryOne[Atom](ctx, a.c, "Atom", mustFilter(query.Eq("name", name)))
}

// ConnectorVersions lists the connectors installed on a runtime:
// GET AtomConnectorVersions/{id}.
func (a Atoms) ConnectorVersions(ctx context.Context, id string) (AtomConnectorVersions, error) {
	if id == "" {
		return AtomConnectorVersions{}, errEmptyID("atom")
	}

	return getJSON[AtomConnectorVersions](ctx, a.c, "AtomConnectorVersions", id)
}
