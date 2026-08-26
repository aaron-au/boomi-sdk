package objects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
	"github.com/aaron-au/boomi-sdk/progress"
)

// ExecutionRecord is one process execution. ExecutionDuration is kept raw
// because the platform sends it as a typed tuple (["Long", 1234]).
type ExecutionRecord struct {
	ExecutionID       string          `json:"executionId"`
	OriginalID        string          `json:"originalExecutionId"`
	AccountID         string          `json:"account"`
	ProcessID         string          `json:"processId"`
	ProcessName       string          `json:"processName"`
	AtomID            string          `json:"atomId"`
	AtomName          string          `json:"atomName"`
	Status            string          `json:"status"`
	ExecutionType     string          `json:"executionType"`
	ExecutionTime     string          `json:"executionTime"`
	ExecutionDuration json.RawMessage `json:"executionDuration"`
	InboundDocs       int             `json:"inboundDocumentCount"`
	OutboundDocs      int             `json:"outboundDocumentCount"`
	ErrorDocs         int             `json:"inboundErrorDocumentCount"`
	Message           string          `json:"message"`
	ReportKey         string          `json:"reportKey"`
	LauncherID        string          `json:"launcherId"`
	NodeID            string          `json:"nodeId"`
	RecordedDate      string          `json:"recordedDate"`
}

// Running reports whether the execution has not yet finished.
func (r *ExecutionRecord) Running() bool {
	return r.Status == "INPROCESS" || r.Status == "STARTED"
}

// ExecutionPropertyValue is one dynamic process property handed to an
// execution.
type ExecutionPropertyValue struct {
	Key   string
	Value string
}

// ExecuteRequest starts one process execution on one runtime.
type ExecuteRequest struct {
	AtomID    string
	ProcessID string
	// Properties are dynamic process properties for this run, applied to
	// the process being executed.
	Properties []ExecutionPropertyValue
}

// ExecutionStarted is the platform's answer to an execution request. The
// record itself does not exist yet — await it with AwaitRecord.
type ExecutionStarted struct {
	RequestID string `json:"requestId"`
	RecordURL string `json:"recordUrl"`
}

// Executions runs processes and reads their records and logs.
type Executions struct {
	c *boomi.Client
}

// NewExecutions returns an Executions service over c.
func NewExecutions(c *boomi.Client) Executions {
	return Executions{c: c}
}

// The POST ExecutionRequest envelope, innermost first.
type executionValueWire struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type executionPropertyWire struct {
	Type                 string               `json:"@type"`
	ComponentID          string               `json:"componentId"`
	ProcessPropertyValue []executionValueWire `json:"ProcessPropertyValue"`
}

type executionPropertiesWire struct {
	Type            string                  `json:"@type"`
	ProcessProperty []executionPropertyWire `json:"ProcessProperty"`
}

type executionRequestWire struct {
	Type              string                   `json:"@type"`
	AtomID            string                   `json:"atomId"`
	ProcessID         string                   `json:"processId"`
	ProcessProperties *executionPropertiesWire `json:"ProcessProperties,omitempty"`
}

// buildExecutionBody renders req as the wire envelope, attaching dynamic
// properties to the process being executed when any are given.
func buildExecutionBody(req ExecuteRequest) executionRequestWire {
	body := executionRequestWire{Type: "ExecutionRequest", AtomID: req.AtomID, ProcessID: req.ProcessID}
	if len(req.Properties) == 0 {
		return body
	}

	values := make([]executionValueWire, len(req.Properties))
	for i, p := range req.Properties {
		values[i] = executionValueWire(p)
	}

	body.ProcessProperties = &executionPropertiesWire{
		Type: "ProcessProperties",
		ProcessProperty: []executionPropertyWire{{
			Type:                 typeProcessProperty,
			ComponentID:          req.ProcessID,
			ProcessPropertyValue: values,
		}},
	}

	return body
}

// Execute starts a process on a runtime: POST ExecutionRequest. The
// execution is asynchronous — the answer carries the request id to await
// the record with.
func (e Executions) Execute(ctx context.Context, req ExecuteRequest) (ExecutionStarted, error) {
	if req.AtomID == "" || req.ProcessID == "" {
		return ExecutionStarted{}, errors.New("objects: an execution needs both an atom id and a process id")
	}

	return postJSON[ExecutionStarted](ctx, e.c, buildExecutionBody(req), "ExecutionRequest")
}

// AwaitRecord polls GET ExecutionRecord/async/{requestId} until the
// execution finishes and returns its record. This endpoint has its own
// shape — 202 (or a still-running record) while executing, the settled
// record page on completion — distinct from the async/{entity} token
// flow.
func (e Executions) AwaitRecord(
	ctx context.Context,
	requestID string,
	opts *AsyncOptions,
) (ExecutionRecord, error) {
	if requestID == "" {
		return ExecutionRecord{}, errEmptyID("execution request")
	}

	o := opts.withDefaults()
	obs := observerOf(e.c)
	start := time.Now()
	delay := o.InitialDelay

	for {
		record, settled, err := e.pollRecord(ctx, requestID)
		if err != nil {
			return ExecutionRecord{}, err
		}

		if settled {
			return record, nil
		}

		elapsed := time.Since(start)
		if elapsed > o.MaxWait {
			return ExecutionRecord{}, fmt.Errorf(
				"objects: execution %s still running after %s", requestID, elapsed.Round(time.Second),
			)
		}

		obs.OnAsyncPoll(progress.AsyncPollEvent{
			Entity: "ExecutionRecord", Message: "execution in progress", Elapsed: elapsed, Wait: delay,
		})

		if sleepErr := sleepCtx(ctx, delay); sleepErr != nil {
			return ExecutionRecord{}, sleepErr
		}

		if delay < o.MaxDelay {
			delay += time.Second
		}
	}
}

// pollRecord reads the async record endpoint once.
func (e Executions) pollRecord(ctx context.Context, requestID string) (ExecutionRecord, bool, error) {
	res, err := doJSON[AsyncResult](ctx, e.c, boomi.Request{
		Method: http.MethodGet,
		Path:   []string{"ExecutionRecord", asyncSegment, requestID},
		Accept: contentTypeJSON,
		Class:  boomi.ClassRead,
	})
	if err != nil {
		return ExecutionRecord{}, false, err
	}

	if res.ResponseStatusCode == http.StatusAccepted || len(res.Result) == 0 {
		return ExecutionRecord{}, false, nil
	}

	records, err := DecodeAsync[ExecutionRecord](res)
	if err != nil {
		return ExecutionRecord{}, false, err
	}

	if records[0].Running() {
		return ExecutionRecord{}, false, nil
	}

	return records[0], true, nil
}

// Query runs ExecutionRecord/query with a caller-supplied filter body and
// paginates to completion. It is the raw escape hatch; prefer the typed
// helpers.
func (e Executions) Query(ctx context.Context, filter json.RawMessage) ([]ExecutionRecord, error) {
	return QueryAll[ExecutionRecord](ctx, e.c, "ExecutionRecord", filter)
}

// ByID returns the execution record with this execution id, or nil when
// none exists.
func (e Executions) ByID(ctx context.Context, executionID string) (*ExecutionRecord, error) {
	return QueryOne[ExecutionRecord](ctx, e.c, "ExecutionRecord", mustFilter(query.Eq("executionId", executionID)))
}

// ForProcessSince returns executions of one process recorded on or after
// since (platform timestamp format, e.g. "2026-08-26T00:00:00Z").
func (e Executions) ForProcessSince(ctx context.Context, processID, since string) ([]ExecutionRecord, error) {
	expr := query.And(query.Eq("processId", processID), query.Gte("executionTime", since))

	return QueryAll[ExecutionRecord](ctx, e.c, "ExecutionRecord", mustFilter(expr))
}

// processLogWire is the POST ProcessLog body.
type processLogWire struct {
	Type        string `json:"@type"`
	ExecutionID string `json:"executionId"`
	LogLevel    string `json:"logLevel"`
}

// processLogAnswer is the platform's reply: where the log archive will
// be, not the log itself.
type processLogAnswer struct {
	URL string `json:"url"`
}

// Log requests the execution's log archive and downloads it once the
// platform has assembled it, returning the raw zip stream. The caller
// owns the stream and must close it.
//
// While the log is still being assembled the platform answers the
// ProcessLog request itself with a 400 "is invalid" (boomi.IsLogNotReady)
// and the download URL with 202 — both are waited out within opts'
// budget.
func (e Executions) Log(ctx context.Context, executionID string, opts *AsyncOptions) (io.ReadCloser, error) {
	if executionID == "" {
		return nil, errEmptyID("execution")
	}

	o := opts.withDefaults()
	obs := observerOf(e.c)
	start := time.Now()
	body := processLogWire{Type: "ProcessLog", ExecutionID: executionID, LogLevel: "ALL"}

	for {
		stream, err := e.tryLog(ctx, body)
		if err == nil {
			return stream, nil
		}

		if !errors.Is(err, boomi.ErrNotReady) && !boomi.IsLogNotReady(err) {
			return nil, err
		}

		elapsed := time.Since(start)
		if elapsed > o.MaxWait {
			return nil, fmt.Errorf("objects: log for execution %s still not ready after %s: %w",
				executionID, elapsed.Round(time.Second), err)
		}

		obs.OnAsyncPoll(progress.AsyncPollEvent{
			Entity: "ProcessLog", Message: "waiting for the log archive", Elapsed: elapsed, Wait: o.InitialDelay,
		})

		if sleepErr := sleepCtx(ctx, o.InitialDelay); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

// tryLog makes one request-then-download attempt.
func (e Executions) tryLog(ctx context.Context, body processLogWire) (io.ReadCloser, error) {
	answer, err := postJSON[processLogAnswer](ctx, e.c, body, "ProcessLog")
	if err != nil {
		return nil, err
	}

	if answer.URL == "" {
		return nil, errors.New("objects: the ProcessLog request gave no download location")
	}

	return e.c.Download(ctx, answer.URL)
}
