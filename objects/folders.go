package objects

import (
	"context"
	"errors"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// Folder is one row from a Folder/query result. FullPath is returned by
// the platform but is NOT queryable server-side: filter on id, name, or
// parentId and match paths client-side.
type Folder struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	FullPath string `json:"fullPath"`
	ParentID string `json:"parentId"`
	Deleted  bool   `json:"deleted"`
}

// Folders queries Folder objects.
type Folders struct {
	c *boomi.Client
}

// NewFolders returns a Folders service over c.
func NewFolders(c *boomi.Client) Folders {
	return Folders{c: c}
}

// All returns every folder in the account (empty filter), paginated to
// completion. Note that fullPath comes back on each row but cannot be
// used in a server-side filter.
func (f Folders) All(ctx context.Context) ([]Folder, error) {
	return QueryAll[Folder](ctx, f.c, "Folder", nil)
}

// Roots returns the folders with no parent: IS_NULL parentId.
func (f Folders) Roots(ctx context.Context) ([]Folder, error) {
	return QueryAll[Folder](ctx, f.c, "Folder", mustFilter(query.IsNull("parentId")))
}

// folderWire is the POST Folder body.
type folderWire struct {
	Type     string `json:"@type"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
}

// Create creates a folder: POST Folder. An empty parentID creates a root
// folder.
func (f Folders) Create(ctx context.Context, name, parentID string) (Folder, error) {
	if name == "" {
		return Folder{}, errors.New("objects: folder name is empty")
	}

	body := folderWire{Type: "Folder", Name: name, ParentID: parentID}

	return postJSON[Folder](ctx, f.c, body, "Folder")
}
