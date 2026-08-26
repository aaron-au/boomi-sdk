package objects

import (
	"context"
	"errors"
	"fmt"

	boomi "github.com/aaron-au/boomi-sdk"
)

// MergeRequest is one branch merge: source into destination, staged then
// executed. Stage moves through the platform's lifecycle (DRAFTING,
// REVIEWING, MERGING, MERGED, FAILED_TO_MERGE, ...); re-read with Get to
// follow it.
type MergeRequest struct {
	ID                  string `json:"id"`
	SourceBranchID      string `json:"sourceBranchId"`
	DestinationBranchID string `json:"destinationBranchId"`
	Strategy            string `json:"strategy"`
	PriorityBranch      string `json:"priorityBranch"`
	Stage               string `json:"stage"`
	PreviousStage       string `json:"previousStage"`
	CreatedBy           string `json:"createdBy"`
	CreatedDate         string `json:"createdDate"`
	ModifiedBy          string `json:"modifiedBy"`
	ModifiedDate        string `json:"modifiedDate"`
	Note                string `json:"note"`
}

// Merge strategies the platform accepts.
const (
	// MergeStrategyOverride takes the source branch's version of every
	// conflicting component.
	MergeStrategyOverride = "OVERRIDE"
	// MergeStrategyConflictResolve stages conflicts for per-component
	// resolution before the merge executes.
	MergeStrategyConflictResolve = "CONFLICT_RESOLVE"
)

// ErrBranchWriteUnconfirmed guards branch and merge writes: they change
// shared account state (a merge rewrites the destination branch), so the
// caller must set Confirmed.
var ErrBranchWriteUnconfirmed = errors.New(
	"objects: branch and merge writes change shared account state; set Confirmed",
)

// Create creates a branch forked from parent: POST Branch. Accounts
// without the branching feature refuse this behind a generic 400/403 —
// detect that with boomi.IsBranchUnlicensed. The platform builds the
// branch asynchronously: the returned Branch has Ready false until the
// fork completes; poll All or ByName until Ready.
func (b Branches) Create(ctx context.Context, name, parentID string) (Branch, error) {
	if name == "" {
		return Branch{}, errors.New("objects: branch name is empty")
	}

	if parentID == "" {
		return Branch{}, errEmptyID("parent branch")
	}

	body := struct {
		Name     string `json:"name"`
		ParentID string `json:"parentId"`
	}{Name: name, ParentID: parentID}

	return postJSON[Branch](ctx, b.c, body, "Branch")
}

// Delete deletes a branch: DELETE Branch/{id}. Requires Confirmed — a
// deleted branch takes its unmerged work with it.
func (b Branches) Delete(ctx context.Context, id string, confirmed bool) error {
	if !confirmed {
		return ErrBranchWriteUnconfirmed
	}

	if id == "" {
		return errEmptyID("branch")
	}

	return deleteReq(ctx, b.c, "Branch", id)
}

// MergeRequests accesses MergeRequest objects.
type MergeRequests struct {
	c *boomi.Client
}

// NewMergeRequests returns a MergeRequests service over c.
func NewMergeRequests(c *boomi.Client) MergeRequests {
	return MergeRequests{c: c}
}

// CreateMergeRequest stages a merge of one branch into another.
type CreateMergeRequest struct {
	SourceBranchID      string
	DestinationBranchID string
	// Strategy is MergeStrategyOverride or MergeStrategyConflictResolve.
	Strategy string
	// PriorityBranch names which side wins ("SOURCE" or "DESTINATION")
	// where the strategy calls for a winner.
	PriorityBranch string
}

// mergeRequestWire is the POST MergeRequest body.
type mergeRequestWire struct {
	SourceBranchID      string `json:"sourceBranchId"`
	DestinationBranchID string `json:"destinationBranchId"`
	Strategy            string `json:"strategy"`
	PriorityBranch      string `json:"priorityBranch,omitempty"`
}

// Create stages a merge request: POST MergeRequest. Staging does not
// change either branch — Execute does.
func (m MergeRequests) Create(ctx context.Context, req CreateMergeRequest) (MergeRequest, error) {
	if req.SourceBranchID == "" || req.DestinationBranchID == "" {
		return MergeRequest{}, errors.New("objects: a merge request needs both a source and a destination branch id")
	}

	if req.Strategy == "" {
		return MergeRequest{}, errors.New("objects: a merge request needs a strategy")
	}

	body := mergeRequestWire(req)

	return postJSON[MergeRequest](ctx, m.c, body, "MergeRequest")
}

// Get returns one merge request: GET MergeRequest/{id}. Use it to follow
// Stage after Execute.
func (m MergeRequests) Get(ctx context.Context, id string) (MergeRequest, error) {
	if id == "" {
		return MergeRequest{}, errEmptyID("merge request")
	}

	return getJSON[MergeRequest](ctx, m.c, "MergeRequest", id)
}

// executeMergeWire is the POST MergeRequest/execute/{id} body.
type executeMergeWire struct {
	ID     string `json:"id"`
	Action string `json:"mergeRequestAction"`
}

// Execute performs the staged merge: POST MergeRequest/execute/{id} with
// action MERGE. Requires Confirmed — this rewrites the destination
// branch. The merge completes asynchronously; follow Stage with Get.
func (m MergeRequests) Execute(ctx context.Context, id string, confirmed bool) (MergeRequest, error) {
	return m.execute(ctx, id, "MERGE", confirmed)
}

// Revert reverses an executed merge: POST MergeRequest/execute/{id} with
// action REVERT. Requires Confirmed — a revert is itself permanent.
func (m MergeRequests) Revert(ctx context.Context, id string, confirmed bool) (MergeRequest, error) {
	return m.execute(ctx, id, "REVERT", confirmed)
}

// execute sends one merge action.
func (m MergeRequests) execute(ctx context.Context, id, action string, confirmed bool) (MergeRequest, error) {
	if !confirmed {
		return MergeRequest{}, ErrBranchWriteUnconfirmed
	}

	if id == "" {
		return MergeRequest{}, errEmptyID("merge request")
	}

	body := executeMergeWire{ID: id, Action: action}

	out, err := postJSON[MergeRequest](ctx, m.c, body, "MergeRequest", "execute", id)
	if err != nil {
		return MergeRequest{}, fmt.Errorf("objects: executing merge %s (%s): %w", id, action, err)
	}

	return out, nil
}

// Delete removes a merge request: DELETE MergeRequest/{id}. Removing a
// staged request abandons it without touching either branch.
func (m MergeRequests) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errEmptyID("merge request")
	}

	return deleteReq(ctx, m.c, "MergeRequest", id)
}
