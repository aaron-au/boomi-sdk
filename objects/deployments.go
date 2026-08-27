package objects

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// FlexInt is an integer the platform serves inconsistently: a bare JSON
// number in some responses and a quoted one in others. PackagedComponent's
// componentVersion arrives quoted from queries and bare from create.
// It marshals back as a bare number.
type FlexInt int

// UnmarshalJSON accepts 3, "3", null, and "".
func (n *FlexInt) UnmarshalJSON(b []byte) error {
	s := string(bytes.Trim(b, `"`))
	if s == "null" || s == "" {
		*n = 0

		return nil
	}

	v, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("objects: %q is not an integer: %w", string(b), err)
	}

	*n = FlexInt(v)

	return nil
}

// PackagedComponent is one packaged version of a component.
type PackagedComponent struct {
	PackageID        string  `json:"packageId"`
	PackageVersion   string  `json:"packageVersion"`
	ComponentID      string  `json:"componentId"`
	ComponentVersion FlexInt `json:"componentVersion"`
	ComponentType    string  `json:"componentType"`
	CreatedDate      string  `json:"createdDate"`
	CreatedBy        string  `json:"createdBy"`
	Notes            string  `json:"notes"`
	Shareable        bool    `json:"shareable"`
	Deleted          bool    `json:"deleted"`
	BranchName       string  `json:"branchName"`
}

// DeployedPackage is one packaged component deployed to an environment.
// ComponentType distinguishes process, webservice, processroute and
// customlibrary.
type DeployedPackage struct {
	DeploymentID     string  `json:"deploymentId"`
	Version          int     `json:"version"`
	PackageID        string  `json:"packageId"`
	PackageVersion   string  `json:"packageVersion"`
	EnvironmentID    string  `json:"environmentId"`
	ComponentID      string  `json:"componentId"`
	ComponentVersion FlexInt `json:"componentVersion"`
	ComponentType    string  `json:"componentType"`
	DeployedDate     string  `json:"deployedDate"`
	DeployedBy       string  `json:"deployedBy"`
	Notes            string  `json:"notes"`
	Active           bool    `json:"active"`
}

// ErrDeployUnconfirmed guards writes that change what runs in a live
// environment: deploy and undeploy refuse to act until the caller sets
// Confirmed, so neither can happen as a side effect of anything else.
var ErrDeployUnconfirmed = errors.New(
	"objects: deployment writes change what runs in a live environment; set Confirmed",
)

// PackagedComponents accesses PackagedComponent objects.
type PackagedComponents struct {
	c *boomi.Client
}

// NewPackagedComponents returns a PackagedComponents service over c.
func NewPackagedComponents(c *boomi.Client) PackagedComponents {
	return PackagedComponents{c: c}
}

// Get returns one packaged component: GET PackagedComponent/{packageId}.
// Use it to confirm a package still exists and read the component version
// it carries before deploying it.
func (p PackagedComponents) Get(ctx context.Context, packageID string) (PackagedComponent, error) {
	if packageID == "" {
		return PackagedComponent{}, errEmptyID("package")
	}

	return getJSON[PackagedComponent](ctx, p.c, "PackagedComponent", packageID)
}

// ForComponent returns every package of one component, newest first as
// the platform orders them.
func (p PackagedComponents) ForComponent(ctx context.Context, componentID string) ([]PackagedComponent, error) {
	if componentID == "" {
		return nil, errEmptyID("component")
	}

	return QueryAll[PackagedComponent](
		ctx, p.c, "PackagedComponent", mustFilter(query.Eq("componentId", componentID)),
	)
}

// CreatePackageRequest packages the current version of a component.
type CreatePackageRequest struct {
	ComponentID string
	// PackageVersion and Notes are optional; the platform assigns a
	// version when none is given.
	PackageVersion string
	Notes          string
}

// packagedComponentWire is the POST PackagedComponent body.
type packagedComponentWire struct {
	Type           string `json:"@type"`
	ComponentID    string `json:"componentId"`
	PackageVersion string `json:"packageVersion,omitempty"`
	Notes          string `json:"notes,omitempty"`
}

// Create packages the component's current version: POST PackagedComponent.
func (p PackagedComponents) Create(ctx context.Context, req CreatePackageRequest) (PackagedComponent, error) {
	if req.ComponentID == "" {
		return PackagedComponent{}, errEmptyID("component")
	}

	body := packagedComponentWire{
		Type:           "PackagedComponent",
		ComponentID:    req.ComponentID,
		PackageVersion: req.PackageVersion,
		Notes:          req.Notes,
	}

	return postJSON[PackagedComponent](ctx, p.c, body, "PackagedComponent")
}

// DeployedPackages accesses DeployedPackage objects.
type DeployedPackages struct {
	c *boomi.Client
}

// NewDeployedPackages returns a DeployedPackages service over c.
func NewDeployedPackages(c *boomi.Client) DeployedPackages {
	return DeployedPackages{c: c}
}

// In returns what is deployed to an environment. activeOnly excludes
// superseded deployments.
func (d DeployedPackages) In(ctx context.Context, environmentID string, activeOnly bool) ([]DeployedPackage, error) {
	if environmentID == "" {
		return nil, errEmptyID("environment")
	}

	expr := []query.Expression{query.Eq("environmentId", environmentID)}
	if activeOnly {
		expr = append(expr, query.EqBool("active", true))
	}

	return QueryAll[DeployedPackage](ctx, d.c, "DeployedPackage", mustFilter(query.And(expr...)))
}

// DeployRequest asks for an existing package version to be deployed into
// an environment — the API behind the platform's own "deploy to
// environment".
type DeployRequest struct {
	// EnvironmentID is the target environment.
	EnvironmentID string
	// PackageID identifies the exact version to deploy. Deploy by
	// package id, never by component id: given a component id the
	// platform picks the globally latest version across all branches.
	PackageID string
	Notes     string
	// Confirmed must be set by the caller; see ErrDeployUnconfirmed.
	Confirmed bool
}

// deployedPackageWire is the POST DeployedPackage body.
type deployedPackageWire struct {
	Type          string `json:"@type"`
	EnvironmentID string `json:"environmentId"`
	PackageID     string `json:"packageId"`
	Notes         string `json:"notes,omitempty"`
}

// Deploy deploys an existing package version into an environment. It
// does not undeploy anything. Deploying a version already present in the
// target is a no-op from the runtime's point of view but still creates a
// deployment record; detect the platform's duplicate rejection with
// boomi.IsDuplicateDeploy.
func (d DeployedPackages) Deploy(ctx context.Context, req DeployRequest) (DeployedPackage, error) {
	if !req.Confirmed {
		return DeployedPackage{}, ErrDeployUnconfirmed
	}

	if req.EnvironmentID == "" || req.PackageID == "" {
		return DeployedPackage{}, errors.New("objects: deployment needs both an environment id and a package id")
	}

	body := deployedPackageWire{
		Type:          "DeployedPackage",
		EnvironmentID: req.EnvironmentID,
		PackageID:     req.PackageID,
		Notes:         req.Notes,
	}

	return postJSON[DeployedPackage](ctx, d.c, body, "DeployedPackage")
}

// UndeployRequest removes one deployment record from its environment.
type UndeployRequest struct {
	DeploymentID string
	// Confirmed must be set by the caller; see ErrDeployUnconfirmed.
	Confirmed bool
}

// Undeploy removes a deployment: DELETE DeployedPackage/{deploymentId}.
func (d DeployedPackages) Undeploy(ctx context.Context, req UndeployRequest) error {
	if !req.Confirmed {
		return ErrDeployUnconfirmed
	}

	if req.DeploymentID == "" {
		return errEmptyID("deployment")
	}

	return deleteReq(ctx, d.c, "DeployedPackage", req.DeploymentID)
}
