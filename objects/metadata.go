package objects

import (
	"context"
	"encoding/json"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// ComponentMetadata is one row from a ComponentMetadata/query result.
type ComponentMetadata struct {
	ComponentID    string `json:"componentId"`
	Version        int    `json:"version"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	FolderID       string `json:"folderId"`
	FolderName     string `json:"folderName"`
	CurrentVersion bool   `json:"currentVersion"`
	Deleted        bool   `json:"deleted"`
	BranchID       string `json:"branchId"`
	BranchName     string `json:"branchName"`
	CreatedBy      string `json:"createdBy"`
	CreatedDate    string `json:"createdDate"`
	ModifiedBy     string `json:"modifiedBy"`
	ModifiedDate   string `json:"modifiedDate"`
}

// Metadata queries ComponentMetadata.
type Metadata struct {
	c *boomi.Client
}

// NewMetadata returns a Metadata service over c.
func NewMetadata(c *boomi.Client) Metadata {
	return Metadata{c: c}
}

// Query runs ComponentMetadata/query with a caller-supplied filter body
// and paginates to completion. It is the raw escape hatch; prefer the
// typed helpers below.
func (m Metadata) Query(ctx context.Context, filter json.RawMessage) ([]ComponentMetadata, error) {
	return QueryAll[ComponentMetadata](ctx, m.c, "ComponentMetadata", filter)
}

// currentNotDeleted returns the expressions selecting live current
// versions: currentVersion=true and deleted=false.
func currentNotDeleted() []query.Expression {
	return []query.Expression{query.EqBool("currentVersion", true), query.EqBool("deleted", false)}
}

// Current returns metadata for every current, non-deleted component
// version in the account.
func (m Metadata) Current(ctx context.Context) ([]ComponentMetadata, error) {
	return m.Query(ctx, mustFilter(query.And(currentNotDeleted()...)))
}

// folderBatchSize caps folder ids per request. The platform rejects
// oversized OR groups; the plugin's client-side cap is 200 per request.
const folderBatchSize = 200

// CurrentByFolders returns current, non-deleted component metadata within
// the given folders. More than 200 folder ids are batched into multiple
// requests of at most 200 each and the results concatenated in input
// order. An empty folderIDs returns an empty slice without touching the
// wire.
func (m Metadata) CurrentByFolders(ctx context.Context, folderIDs []string) ([]ComponentMetadata, error) {
	if len(folderIDs) == 0 {
		return []ComponentMetadata{}, nil
	}

	all := make([]ComponentMetadata, 0, len(folderIDs))
	for start := 0; start < len(folderIDs); start += folderBatchSize {
		end := min(start+folderBatchSize, len(folderIDs))
		chunk := folderIDs[start:end]
		expr := query.And(append(currentNotDeleted(), query.In("folderId", chunk...))...)

		batch, err := m.Query(ctx, mustFilter(expr))
		if err != nil {
			return nil, err
		}

		all = append(all, batch...)
	}

	return all, nil
}

// CurrentByNameLike returns current, non-deleted component metadata whose
// name matches the LIKE pattern (the platform's SQL-style wildcards, e.g.
// "Order%").
func (m Metadata) CurrentByNameLike(ctx context.Context, pattern string) ([]ComponentMetadata, error) {
	expr := query.And(append(currentNotDeleted(), query.Like("name", pattern))...)
	return m.Query(ctx, mustFilter(expr))
}
