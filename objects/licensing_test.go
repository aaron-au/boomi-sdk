package objects_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aaron-au/boomi-sdk/objects"
)

func TestConnectionReportDownloadsCSV(t *testing.T) {
	requireDo(t)

	var serverURL string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ConnectionLicensingReport"):
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), "QueryFilter") {
				t.Errorf("report request %q lacks the QueryFilter envelope", raw)
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"url":"`+serverURL+`/report.csv","statusCode":202,"message":"generating"}`)
		case strings.HasSuffix(r.URL.Path, "/report.csv"):
			_, _ = fmt.Fprint(w, "Connector Class,Component\nStandard,\"Orders, Inc\"\n")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	serverURL = srv.URL
	c := newClient(t, srv.URL)

	rows, err := objects.NewLicensing(c).ConnectionReport(context.Background(), fastAsync())
	if err != nil {
		t.Fatalf("ConnectionReport: %v", err)
	}

	if len(rows) != 1 || rows[0]["Connector Class"] != "Standard" || rows[0]["Component"] != "Orders, Inc" {
		t.Fatalf("rows = %+v, want the quoted-comma row parsed", rows)
	}
}

func TestConnectionReportClearsStuckReport(t *testing.T) {
	requireDo(t)

	var (
		serverURL string
		posts     atomic.Int64
		cleared   atomic.Bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ConnectionLicensingReport"):
			if posts.Add(1) == 1 {
				http.Error(
					w,
					`previous report did not download; retrieve it at `+serverURL+`/stuck.csv before requesting another`,
					http.StatusBadRequest,
				)

				return
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"url":"`+serverURL+`/fresh.csv","statusCode":202}`)
		case strings.HasSuffix(r.URL.Path, "/stuck.csv"):
			cleared.Store(true)

			_, _ = fmt.Fprint(w, "stale,report\n")
		case strings.HasSuffix(r.URL.Path, "/fresh.csv"):
			_, _ = fmt.Fprint(w, "Connector Class\nEnterprise\n")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	serverURL = srv.URL
	c := newClient(t, srv.URL)

	rows, err := objects.NewLicensing(c).ConnectionReport(context.Background(), fastAsync())
	if err != nil {
		t.Fatalf("ConnectionReport: %v", err)
	}

	if !cleared.Load() {
		t.Fatal("the stuck report was never fetched to clear it")
	}

	if len(rows) != 1 || rows[0]["Connector Class"] != "Enterprise" {
		t.Fatalf("rows = %+v, want the fresh report, never the stale one", rows)
	}
}

func TestConnectionReportFallsBackToClasses(t *testing.T) {
	requireDo(t)

	var (
		serverURL string
		posts     atomic.Int64
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ConnectionLicensingReport"):
			n := posts.Add(1)

			raw, _ := io.ReadAll(r.Body)
			body := string(raw)

			w.Header().Set("Content-Type", "application/json")

			// The first, unfiltered request gets a header-only report;
			// per-class requests get one row each.
			if n == 1 && !strings.Contains(body, "connectorClass") {
				_, _ = fmt.Fprint(w, `{"url":"`+serverURL+`/empty.csv","statusCode":202}`)
				return
			}

			_, _ = fmt.Fprint(w, `{"url":"`+serverURL+`/class.csv","statusCode":202}`)
		case strings.HasSuffix(r.URL.Path, "/empty.csv"):
			_, _ = fmt.Fprint(w, "Connector Class,Component\n")
		case strings.HasSuffix(r.URL.Path, "/class.csv"):
			_, _ = fmt.Fprint(w, "Connector Class,Component\nX,Y\n")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	serverURL = srv.URL
	c := newClient(t, srv.URL)

	rows, err := objects.NewLicensing(c).ConnectionReport(context.Background(), fastAsync())
	if err != nil {
		t.Fatalf("ConnectionReport: %v", err)
	}

	// One row per connector class from the fallback sweep.
	if len(rows) != len(objects.ConnectorClasses) {
		t.Fatalf("rows = %d, want one per class (%d)", len(rows), len(objects.ConnectorClasses))
	}
}
