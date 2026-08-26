package objects

import (
	"context"
	"errors"
	"fmt"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// Schedule is one execution window. Fields are cron-like strings;
// daysOfWeek is 1-indexed from Sunday. A process may carry several
// windows — carry all of them.
type Schedule struct {
	Type        string `json:"@type,omitempty"`
	Minutes     string `json:"minutes"`
	Hours       string `json:"hours"`
	DaysOfWeek  string `json:"daysOfWeek"`
	DaysOfMonth string `json:"daysOfMonth"`
	Months      string `json:"months"`
	Years       string `json:"years"`
}

// ScheduleRetry is the retry policy attached to a process schedule.
type ScheduleRetry struct {
	Type     string     `json:"@type,omitempty"`
	MaxRetry int        `json:"maxRetry"`
	Schedule []Schedule `json:"Schedule"`
}

// ProcessSchedules is the schedule record for one (process, runtime)
// pair. The platform creates one for every deployed process, so an empty
// Schedule slice means "deployed but not scheduled".
type ProcessSchedules struct {
	Type      string     `json:"@type,omitempty"`
	ID        string     `json:"id"`
	ProcessID string     `json:"processId"`
	AtomID    string     `json:"atomId"`
	Schedule  []Schedule `json:"Schedule"`
	// Retry is a pointer so a record without a retry policy omits the
	// block entirely on a write — the platform rejects a Retry block
	// with no retry windows.
	Retry *ScheduleRetry `json:"Retry,omitempty"`
}

// Scheduled reports whether any window is defined.
func (p ProcessSchedules) Scheduled() bool { return len(p.Schedule) > 0 }

// ProcessScheduleStatus is the enable/disable switch for one process's
// schedule on one runtime — the control that decides whether a schedule
// that exists will actually fire.
type ProcessScheduleStatus struct {
	Type      string `json:"@type,omitempty"`
	ID        string `json:"id"`
	ProcessID string `json:"processId"`
	AtomID    string `json:"atomId"`
	Enabled   bool   `json:"enabled"`
}

// ErrScheduleWriteUnconfirmed guards writes that change when a live
// runtime fires processes; the caller must set Confirmed.
var ErrScheduleWriteUnconfirmed = errors.New(
	"objects: schedule writes change when a live runtime executes processes; set Confirmed",
)

// Schedules accesses ProcessSchedules and ProcessScheduleStatus objects.
type Schedules struct {
	c *boomi.Client
}

// NewSchedules returns a Schedules service over c.
func NewSchedules(c *boomi.Client) Schedules {
	return Schedules{c: c}
}

// ForAtom returns one schedule record per deployed process on the
// runtime. Records with no windows are deployed-but-not-scheduled.
func (s Schedules) ForAtom(ctx context.Context, atomID string) ([]ProcessSchedules, error) {
	if atomID == "" {
		return nil, errEmptyID("atom")
	}

	return QueryAll[ProcessSchedules](ctx, s.c, "ProcessSchedules", mustFilter(query.Eq("atomId", atomID)))
}

// ForProcess returns the schedule record for one process on one runtime.
func (s Schedules) ForProcess(ctx context.Context, atomID, processID string) (ProcessSchedules, error) {
	rec, err := QueryOne[ProcessSchedules](ctx, s.c, "ProcessSchedules",
		mustFilter(query.And(query.Eq("atomId", atomID), query.Eq("processId", processID))))
	if err != nil {
		return ProcessSchedules{}, err
	}

	if rec == nil {
		return ProcessSchedules{}, fmt.Errorf(
			"objects: no schedule record for process %s on runtime %s", processID, atomID,
		)
	}

	return *rec, nil
}

// StatusForAtom returns the enable/disable state per process on the
// runtime.
func (s Schedules) StatusForAtom(ctx context.Context, atomID string) ([]ProcessScheduleStatus, error) {
	if atomID == "" {
		return nil, errEmptyID("atom")
	}

	return QueryAll[ProcessScheduleStatus](
		ctx, s.c, "ProcessScheduleStatus", mustFilter(query.Eq("atomId", atomID)),
	)
}

// StatusForProcess returns the enable/disable record for one process on
// one runtime. The record's own ID is what SetEnabled is addressed to.
func (s Schedules) StatusForProcess(ctx context.Context, atomID, processID string) (ProcessScheduleStatus, error) {
	rec, err := QueryOne[ProcessScheduleStatus](ctx, s.c, "ProcessScheduleStatus",
		mustFilter(query.And(query.Eq("atomId", atomID), query.Eq("processId", processID))))
	if err != nil {
		return ProcessScheduleStatus{}, err
	}

	if rec == nil {
		return ProcessScheduleStatus{}, fmt.Errorf(
			"objects: no schedule status for process %s on runtime %s", processID, atomID,
		)
	}

	return *rec, nil
}

// UpdateSchedulesRequest replaces the schedule windows for one process on
// one runtime.
//
// The write is a replace for that (process, runtime) pair: the windows
// sent are the windows the process will have. It does not affect any
// other process, and it does not enable a disabled schedule — that is
// SetEnabled.
type UpdateSchedulesRequest struct {
	// ScheduleID is the platform's id for the target's own record —
	// resolve it with ForProcess against the target first, never carried
	// over from another runtime.
	ScheduleID string
	AtomID     string
	ProcessID  string
	Windows    []Schedule
	// MaxRetry carries a retry policy. Zero sends no Retry block: the
	// platform rejects a Retry block with no retry windows.
	MaxRetry int
	// Confirmed must be set by the caller; see
	// ErrScheduleWriteUnconfirmed.
	Confirmed bool
}

// Update writes the schedule windows for a process on a runtime:
// POST ProcessSchedules/{scheduleId}.
func (s Schedules) Update(ctx context.Context, req UpdateSchedulesRequest) (ProcessSchedules, error) {
	if !req.Confirmed {
		return ProcessSchedules{}, ErrScheduleWriteUnconfirmed
	}

	if req.ScheduleID == "" || req.AtomID == "" || req.ProcessID == "" {
		return ProcessSchedules{}, errors.New(
			"objects: a schedule update needs the target's schedule id, atom id and process id",
		)
	}

	body := ProcessSchedules{
		Type:      "ProcessSchedules",
		ID:        req.ScheduleID,
		AtomID:    req.AtomID,
		ProcessID: req.ProcessID,
		Schedule:  req.Windows,
	}
	if req.MaxRetry > 0 {
		body.Retry = &ScheduleRetry{Type: "ScheduleRetry", MaxRetry: req.MaxRetry, Schedule: req.Windows}
	}

	return postJSON[ProcessSchedules](ctx, s.c, body, "ProcessSchedules", req.ScheduleID)
}

// SetEnabledRequest flips one process's schedule on or off on one
// runtime. Disabling on one runtime is what stops a migrated process
// running in two places at once; enabling it is the cutover.
type SetEnabledRequest struct {
	// StatusID is the platform's id for the target's own status record —
	// resolve it with StatusForProcess.
	StatusID  string
	AtomID    string
	ProcessID string
	Enabled   bool
	// Confirmed must be set by the caller; see
	// ErrScheduleWriteUnconfirmed.
	Confirmed bool
}

// SetEnabled enables or disables a process's schedule:
// POST ProcessScheduleStatus/{statusId}.
func (s Schedules) SetEnabled(ctx context.Context, req SetEnabledRequest) (ProcessScheduleStatus, error) {
	if !req.Confirmed {
		return ProcessScheduleStatus{}, ErrScheduleWriteUnconfirmed
	}

	if req.StatusID == "" {
		return ProcessScheduleStatus{}, errEmptyID("schedule status")
	}

	body := ProcessScheduleStatus{
		Type:      "ProcessScheduleStatus",
		ID:        req.StatusID,
		AtomID:    req.AtomID,
		ProcessID: req.ProcessID,
		Enabled:   req.Enabled,
	}

	return postJSON[ProcessScheduleStatus](ctx, s.c, body, "ProcessScheduleStatus", req.StatusID)
}
