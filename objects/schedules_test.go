package objects_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaron-au/boomi-sdk/objects"
)

func TestScheduleWritesRequireConfirmation(t *testing.T) {
	c := newClient(t, "http://unused.invalid")
	s := objects.NewSchedules(c)

	_, err := s.Update(context.Background(), objects.UpdateSchedulesRequest{
		ScheduleID: "sch-1", AtomID: "atom-1", ProcessID: "proc-1",
	})
	if !errors.Is(err, objects.ErrScheduleWriteUnconfirmed) {
		t.Fatalf("Update err = %v, want ErrScheduleWriteUnconfirmed", err)
	}

	_, err = s.SetEnabled(context.Background(), objects.SetEnabledRequest{StatusID: "st-1"})
	if !errors.Is(err, objects.ErrScheduleWriteUnconfirmed) {
		t.Fatalf("SetEnabled err = %v, want ErrScheduleWriteUnconfirmed", err)
	}
}

func TestScheduleUpdateOmitsRetryWithoutPolicy(t *testing.T) {
	requireDo(t)

	var body, path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body, path = string(raw), r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"sch-1","atomId":"atom-1","processId":"proc-1"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	windows := []objects.Schedule{
		{Minutes: "0", Hours: "6", DaysOfWeek: "2", DaysOfMonth: "*", Months: "*", Years: "*"},
	}

	_, err := objects.NewSchedules(c).Update(context.Background(), objects.UpdateSchedulesRequest{
		ScheduleID: "sch-1",
		AtomID:     "atom-1",
		ProcessID:  "proc-1",
		Windows:    windows,
		Confirmed:  true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if !strings.HasSuffix(path, "/ProcessSchedules/sch-1") {
		t.Fatalf("path = %s, want .../ProcessSchedules/sch-1", path)
	}

	if strings.Contains(body, "maxRetry") && strings.Contains(body, `"maxRetry":0`) {
		t.Fatalf("body = %q, a zero MaxRetry must not send a Retry block", body)
	}
}

func TestScheduleUpdateSendsRetryWithPolicy(t *testing.T) {
	requireDo(t)

	var body string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"sch-1"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	_, err := objects.NewSchedules(c).Update(context.Background(), objects.UpdateSchedulesRequest{
		ScheduleID: "sch-1",
		AtomID:     "atom-1",
		ProcessID:  "proc-1",
		Windows:    []objects.Schedule{{Minutes: "0"}},
		MaxRetry:   3,
		Confirmed:  true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if !strings.Contains(body, `"maxRetry":3`) {
		t.Fatalf("body = %q, want a Retry block with maxRetry 3", body)
	}
}

func TestStatusForProcessNotFound(t *testing.T) {
	requireDo(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"numberOfResults":0,"result":[]}`)
	}))
	defer srv.Close()

	c := newClient(t, srv.URL)

	_, err := objects.NewSchedules(c).StatusForProcess(context.Background(), "atom-1", "proc-1")
	if err == nil || !strings.Contains(err.Error(), "no schedule status") {
		t.Fatalf("err = %v, want a no-status failure", err)
	}
}
