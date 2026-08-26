package objects_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aaron-au/boomi-sdk/objects"
)

// metadataQueryBody is the decoded shape of one ComponentMetadata/query
// request, loose enough to walk any nesting of the filter expression.
type metadataQueryBody struct {
	QueryFilter struct {
		Expression json.RawMessage `json:"expression"`
	} `json:"QueryFilter"`
}

// walkExpression collects every (property, first-argument) pair from a
// filter expression tree.
func walkExpression(t *testing.T, raw json.RawMessage, visit func(property, argument string)) {
	t.Helper()

	var node struct {
		Operator         string            `json:"operator"`
		Property         string            `json:"property"`
		Argument         []string          `json:"argument"`
		NestedExpression []json.RawMessage `json:"nestedExpression"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("decoding filter expression: %v", err)
	}

	if node.Property != "" {
		arg := ""
		if len(node.Argument) > 0 {
			arg = node.Argument[0]
		}

		visit(node.Property, arg)
	}

	for _, nested := range node.NestedExpression {
		walkExpression(t, nested, visit)
	}
}

func TestCurrentByFoldersBatchesAt200(t *testing.T) {
	requireDo(t)

	const total = 450

	folderIDs := make([]string, total)
	for i := range folderIDs {
		folderIDs[i] = fmt.Sprintf("f-%03d", i)
	}

	type batchCall struct {
		folderIDs []string
		flags     map[string]string // currentVersion / deleted arguments
	}

	var (
		mu    sync.Mutex
		calls []batchCall
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ComponentMetadata/query") {
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)

			return
		}

		var body metadataQueryBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request body: %v", err)
			http.Error(w, "bad body", http.StatusBadRequest)

			return
		}

		call := batchCall{flags: map[string]string{}}

		walkExpression(t, body.QueryFilter.Expression, func(property, argument string) {
			switch property {
			case "folderId":
				call.folderIDs = append(call.folderIDs, argument)
			case "currentVersion", "deleted":
				call.flags[property] = argument
			default:
				t.Errorf("unexpected filter property %q", property)
			}
		})
		mu.Lock()

		calls = append(calls, call)
		mu.Unlock()

		// One row per requested folder, echoing the folder id, so
		// concatenation order is observable client-side.
		rows := make([]map[string]any, len(call.folderIDs))
		for i, id := range call.folderIDs {
			rows[i] = map[string]any{"componentId": "c-" + id, "folderId": id}
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"numberOfResults": len(rows),
			"result":          rows,
		}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	defer srv.Close()

	got, err := objects.NewMetadata(newClient(t, srv.URL)).CurrentByFolders(context.Background(), folderIDs)
	if err != nil {
		t.Fatalf("CurrentByFolders: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 3 {
		t.Fatalf("server saw %d requests, want 3", len(calls))
	}

	wantSizes := []int{200, 200, 50}
	next := 0

	for i, call := range calls {
		if len(call.folderIDs) != wantSizes[i] {
			t.Errorf("request %d carried %d folder ids, want %d", i, len(call.folderIDs), wantSizes[i])
		}

		for j, id := range call.folderIDs {
			if want := folderIDs[next+j]; id != want {
				t.Fatalf("request %d folderId[%d] = %q, want %q (input order)", i, j, id, want)
			}
		}

		next += len(call.folderIDs)
		if call.flags["currentVersion"] != "true" || call.flags["deleted"] != "false" {
			t.Errorf("request %d flags = %v, want currentVersion=true deleted=false", i, call.flags)
		}
	}

	if len(got) != total {
		t.Fatalf("got %d results, want %d", len(got), total)
	}

	for i, meta := range got {
		if want := folderIDs[i]; meta.FolderID != want {
			t.Fatalf("result[%d].FolderID = %q, want %q (batches concatenated in input order)", i, meta.FolderID, want)
		}
	}
}

func TestCurrentByFoldersEmptyInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s for empty folder list", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	got, err := objects.NewMetadata(newClient(t, srv.URL)).CurrentByFolders(context.Background(), nil)
	if err != nil {
		t.Fatalf("CurrentByFolders(nil): %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("got %d results for empty input, want 0", len(got))
	}
}
