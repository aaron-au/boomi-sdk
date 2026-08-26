package objects_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaron-au/boomi-sdk/objects"
)

// Small wire tests for the query-and-get object families.

func TestEnvironmentForAtomResolvesAttachment(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/EnvironmentAtomAttachment/query"):
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"atomId"`) {
				t.Errorf("attachment query %q does not filter on atomId", raw)
			}

			_, _ = fmt.Fprint(w, `{"numberOfResults":1,"result":[{"atomId":"atom-1","environmentId":"env-1"}]}`)
		case strings.HasSuffix(r.URL.Path, "/Environment/env-1"):
			_, _ = fmt.Fprint(w, `{"id":"env-1","name":"Production","classification":"PROD"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	env, err := objects.NewEnvironments(c).ForAtom(context.Background(), "atom-1")
	if err != nil {
		t.Fatalf("ForAtom: %v", err)
	}

	if env.ID != "env-1" || env.Classification != "PROD" {
		t.Fatalf("env = %+v, want env-1 PROD", env)
	}
}

func TestEnvironmentForAtomUnattached(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"numberOfResults":0,"result":[]}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	_, err := objects.NewEnvironments(c).ForAtom(context.Background(), "atom-9")
	if err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("err = %v, want a not-attached failure", err)
	}
}

func TestFolderCreateSendsParent(t *testing.T) {
	requireDo(t)

	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"f-2","name":"Child","parentId":"f-1"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	folder, err := objects.NewFolders(c).Create(context.Background(), "Child", "f-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if folder.ID != "f-2" {
		t.Fatalf("folder = %+v, want f-2", folder)
	}

	if !strings.Contains(body, `"parentId":"f-1"`) || !strings.Contains(body, `"@type":"Folder"`) {
		t.Fatalf("body = %q, want the Folder envelope with parentId", body)
	}
}

func TestCertificatesAlwaysSendBoundary(t *testing.T) {
	requireDo(t)

	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"numberOfResults":0,"result":[]}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	if _, err := objects.NewCertificates(c).ExpiringWithin(context.Background(), 0); err != nil {
		t.Fatalf("ExpiringWithin: %v", err)
	}

	if !strings.Contains(body, "expirationBoundary") || !strings.Contains(body, "36500") {
		t.Fatalf("body = %q, want the wide expirationBoundary filter", body)
	}
}

func TestAccountsGetUsesClientAccountID(t *testing.T) {
	requireDo(t)

	var path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"accountId":"acct","name":"ACME","licensing":{"standard":{"purchased":10,"used":4}}}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	account, err := objects.NewAccounts(c).Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The path's final segment must be the client's own account id.
	if !strings.Contains(path, "/Account/") {
		t.Fatalf("path = %s, want .../Account/{accountId}", path)
	}

	licences := account.Licensing.Connections()
	if len(licences) != 1 || licences[0].Name != "Standard" || licences[0].License.Used != 4 {
		t.Fatalf("licences = %+v, want only the present Standard class", licences)
	}
}

func TestRuntimeQueuesUnwrapsWrapper(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/response/") {
			_, _ = fmt.Fprint(w, `{"responseStatusCode":200,"numberOfResults":1,"result":[
				{"QueueRecord":[{"queueName":"q1","messagesCount":7,"deadLettersCount":"2"}]}
			]}`)

			return
		}

		_, _ = fmt.Fprint(w, `{"AsyncOperationTokenResult":{"token":"tok-q"}}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	queues, err := objects.NewRuntime(c).Queues(context.Background(), "atom-1", fastAsync())
	if err != nil {
		t.Fatalf("Queues: %v", err)
	}

	if len(queues) != 1 || queues[0].QueueName != "q1" {
		t.Fatalf("queues = %+v, want q1 unwrapped", queues)
	}

	// Number and string counts both decode.
	if queues[0].MessagesCount.String() != "7" || queues[0].DeadLettersCount.String() != "2" {
		t.Fatalf("counts = %s/%s, want 7 and 2", queues[0].MessagesCount, queues[0].DeadLettersCount)
	}
}

func TestMergeExecuteRequiresConfirmation(t *testing.T) {
	c := newClient(t, "http://unused.invalid")

	_, err := objects.NewMergeRequests(c).Execute(context.Background(), "merge-1", false)
	if !errors.Is(err, objects.ErrBranchWriteUnconfirmed) {
		t.Fatalf("err = %v, want ErrBranchWriteUnconfirmed", err)
	}

	if delErr := objects.NewBranches(c).
		Delete(context.Background(), "branch-1", false); !errors.Is(
		delErr,
		objects.ErrBranchWriteUnconfirmed,
	) {
		t.Fatalf("delete err = %v, want ErrBranchWriteUnconfirmed", delErr)
	}
}

func TestMergeExecuteSendsAction(t *testing.T) {
	requireDo(t)

	var path, body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		path, body = r.URL.Path, string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"merge-1","stage":"MERGING"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	out, err := objects.NewMergeRequests(c).Execute(context.Background(), "merge-1", true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if out.Stage != "MERGING" {
		t.Fatalf("out = %+v, want MERGING", out)
	}

	if !strings.HasSuffix(path, "/MergeRequest/execute/merge-1") {
		t.Fatalf("path = %s, want .../MergeRequest/execute/merge-1", path)
	}

	var wire map[string]string
	if unmarshalErr := json.Unmarshal([]byte(body), &wire); unmarshalErr != nil {
		t.Fatalf("body %q: %v", body, unmarshalErr)
	}

	if wire["mergeRequestAction"] != "MERGE" || wire["id"] != "merge-1" {
		t.Fatalf("wire = %v, want a MERGE action for merge-1", wire)
	}
}

func TestExtensionsUpdateGuardsAndPartialDefault(t *testing.T) {
	requireDo(t)

	guardClient := newClient(t, "http://unused.invalid")

	_, err := objects.NewExtensions(guardClient).Update(context.Background(), objects.UpdateExtensionsRequest{
		EnvironmentID: "env-1",
		Properties:    []objects.ExtensionProperty{{ID: "p", Value: "v"}},
	})
	if !errors.Is(err, objects.ErrExtensionWriteUnconfirmed) {
		t.Fatalf("err = %v, want ErrExtensionWriteUnconfirmed", err)
	}

	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"environmentId":"env-1"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	_, err = objects.NewExtensions(c).Update(context.Background(), objects.UpdateExtensionsRequest{
		EnvironmentID: "env-1",
		Properties:    []objects.ExtensionProperty{{ID: "p", Value: "v"}},
		Confirmed:     true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// FullReplace unset must write partial:true — the safe default.
	if !strings.Contains(body, `"partial":true`) {
		t.Fatalf("body = %q, want partial:true by default", body)
	}
}

func TestRuntimePropsGuards(t *testing.T) {
	c := newClient(t, "http://unused.invalid")

	_, err := objects.NewRuntimeProps(c).UpdatePersisted(context.Background(), objects.UpdatePersistedRequest{
		Payload: objects.PersistedProcessProperties{AtomID: "atom-1"},
	})
	if !errors.Is(err, objects.ErrFullReplaceUnconfirmed) {
		t.Fatalf("err = %v, want ErrFullReplaceUnconfirmed", err)
	}
}

func TestPersistedPropertiesUpsert(t *testing.T) {
	var p objects.PersistedProcessProperties

	p.Upsert("proc-1", "cursor", "100")
	p.Upsert("proc-1", "cursor", "200")
	p.Upsert("proc-2", "mode", "full")

	if len(p.Process) != 2 {
		t.Fatalf("processes = %d, want 2", len(p.Process))
	}

	props := p.Process[0].Properties()
	if len(props) != 1 || props[0].Value != "200" {
		t.Fatalf("props = %+v, want cursor updated in place to 200", props)
	}
}
