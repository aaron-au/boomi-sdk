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

func TestDeployRequiresConfirmation(t *testing.T) {
	c := newClient(t, "http://unused.invalid")

	_, err := objects.NewDeployedPackages(c).Deploy(context.Background(), objects.DeployRequest{
		EnvironmentID: "env-1",
		PackageID:     "pkg-1",
	})
	if !errors.Is(err, objects.ErrDeployUnconfirmed) {
		t.Fatalf("err = %v, want ErrDeployUnconfirmed", err)
	}
}

func TestUndeployRequiresConfirmation(t *testing.T) {
	c := newClient(t, "http://unused.invalid")

	err := objects.NewDeployedPackages(c).Undeploy(context.Background(), objects.UndeployRequest{
		DeploymentID: "dep-1",
	})
	if !errors.Is(err, objects.ErrDeployUnconfirmed) {
		t.Fatalf("err = %v, want ErrDeployUnconfirmed", err)
	}
}

func TestDeploySendsPackageEnvelope(t *testing.T) {
	requireDo(t)

	var method, path, body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path

		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"deploymentId":"dep-9","packageId":"pkg-1","environmentId":"env-1","active":true}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	out, err := objects.NewDeployedPackages(c).Deploy(context.Background(), objects.DeployRequest{
		EnvironmentID: "env-1",
		PackageID:     "pkg-1",
		Notes:         "cutover",
		Confirmed:     true,
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if out.DeploymentID != "dep-9" || !out.Active {
		t.Fatalf("out = %+v, want dep-9 active", out)
	}

	if method != http.MethodPost || !strings.HasSuffix(path, "/DeployedPackage") {
		t.Fatalf("call = %s %s, want POST .../DeployedPackage", method, path)
	}

	var wire map[string]any
	if unmarshalErr := json.Unmarshal([]byte(body), &wire); unmarshalErr != nil {
		t.Fatalf("body %q: %v", body, unmarshalErr)
	}

	if wire["@type"] != "DeployedPackage" || wire["packageId"] != "pkg-1" || wire["environmentId"] != "env-1" {
		t.Fatalf("wire = %v, want DeployedPackage envelope with package and environment", wire)
	}
}

func TestUndeploySendsDelete(t *testing.T) {
	requireDo(t)

	var method, path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"result":"deleted"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	err := objects.NewDeployedPackages(c).Undeploy(context.Background(), objects.UndeployRequest{
		DeploymentID: "dep-1",
		Confirmed:    true,
	})
	if err != nil {
		t.Fatalf("Undeploy: %v", err)
	}

	if method != http.MethodDelete || !strings.HasSuffix(path, "/DeployedPackage/dep-1") {
		t.Fatalf("call = %s %s, want DELETE .../DeployedPackage/dep-1", method, path)
	}
}

func TestPackagedComponentCreate(t *testing.T) {
	requireDo(t)

	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"packageId":"pkg-7","componentId":"comp-1","packageVersion":"1.0"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	out, err := objects.NewPackagedComponents(c).Create(context.Background(), objects.CreatePackageRequest{
		ComponentID: "comp-1",
		Notes:       "release",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if out.PackageID != "pkg-7" {
		t.Fatalf("out = %+v, want pkg-7", out)
	}

	if !strings.Contains(body, `"componentId":"comp-1"`) || !strings.Contains(body, `"@type":"PackagedComponent"`) {
		t.Fatalf("body = %q, want the PackagedComponent envelope", body)
	}

	if strings.Contains(body, "packageVersion") {
		t.Fatalf("body = %q, empty packageVersion must be omitted", body)
	}
}
