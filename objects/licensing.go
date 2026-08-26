package objects

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
	"github.com/aaron-au/boomi-sdk/progress"
)

// ConnectorClasses are the licence classes the platform meters
// connections by — the documented arguments to the
// ConnectionLicensingReport filter.
//
//nolint:gochecknoglobals // immutable, exported reference data, part of the package API
var ConnectorClasses = []string{"Standard", "Small Business", "Enterprise", "Trading Partner"}

// licensingDownload is what a ConnectionLicensingReport POST answers
// with: not the report, but where the report will be.
type licensingDownload struct {
	URL     string `json:"url"`
	Message string `json:"message"`
}

// Licensing produces the deployed-connection licence report.
type Licensing struct {
	c *boomi.Client
}

// NewLicensing returns a Licensing service over c.
func NewLicensing(c *boomi.Client) Licensing {
	return Licensing{c: c}
}

// ConnectionReport asks for the deployed-connection licence report and
// returns its rows, waiting for the platform to generate it.
//
// This object is unlike every other one: the POST answers with a URL
// rather than a body, that URL answers 202 until the report exists, and
// the report itself is CSV. An unfiltered request means the whole account
// — but some accounts answer it with a header-only report, so when that
// happens the documented per-class filter is asked for instead and the
// results merged.
//
// The returned rows are the CSV's own header keys mapped to values, so a
// column the platform renames costs one blank field rather than the whole
// row.
func (l Licensing) ConnectionReport(ctx context.Context, opts *AsyncOptions) ([]map[string]string, error) {
	rows, err := l.report(ctx, nil, opts)
	if err != nil {
		return nil, err
	}

	if len(rows) > 0 {
		return rows, nil
	}

	return l.reportByClass(ctx, opts)
}

// reportByClass runs one report per connector class and merges the rows.
func (l Licensing) reportByClass(ctx context.Context, opts *AsyncOptions) ([]map[string]string, error) {
	var (
		merged   []map[string]string
		failures []error
	)

	for _, class := range ConnectorClasses {
		classRows, classErr := l.report(ctx, mustFilter(query.Eq("connectorClass", class)), opts)
		if classErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", class, classErr))
			continue
		}

		merged = append(merged, classRows...)
	}

	if len(merged) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("objects: the licensing report was empty unfiltered and every class failed: %w",
			errors.Join(failures...))
	}

	return merged, nil
}

// report runs one start-await-parse cycle.
func (l Licensing) report(
	ctx context.Context,
	filter json.RawMessage,
	opts *AsyncOptions,
) ([]map[string]string, error) {
	start, err := l.startReport(ctx, filter)
	if err != nil {
		return nil, err
	}

	raw, err := l.awaitDownload(ctx, start.URL, opts)
	if err != nil {
		return nil, err
	}

	return parseLicensingCSV(raw)
}

// pendingReportURL finds the download location the platform names when it
// refuses a new report because the last one was never collected.
var pendingReportURL = regexp.MustCompile(`https?://[^\s"']+`)

// startReport asks for a report, clearing a stuck one first when the
// platform says there is one.
//
// The platform allows ONE outstanding report per account: a request that
// is never downloaded blocks every later request with a 400,
// indefinitely. The refusal names the pending URL, so the recovery is to
// fetch it (which clears it) and ask again. Its content is deliberately
// discarded — it was generated for whatever filter the abandoned request
// used, at whatever time, and reporting a stale report as this call's
// would be worse than failing.
func (l Licensing) startReport(ctx context.Context, filter json.RawMessage) (licensingDownload, error) {
	start, err := l.postReport(ctx, filter)
	if err != nil {
		stuck := pendingReportURL.FindString(pendingReportMessage(err))
		if stuck == "" {
			return start, err
		}

		if clearErr := l.discardDownload(ctx, stuck); clearErr != nil {
			return start, fmt.Errorf(
				"objects: a previous licensing report is blocking new ones and could not be cleared: %w",
				errors.Join(err, clearErr),
			)
		}

		start, err = l.postReport(ctx, filter)
		if err != nil {
			return start, err
		}
	}

	if start.URL == "" {
		return start, fmt.Errorf("objects: the licensing report gave no download location (%s)", start.Message)
	}

	return start, nil
}

// postReport sends one POST ConnectionLicensingReport. The filter
// envelope is sent even when empty: an unfiltered request means the whole
// account, and the platform expects the envelope to be present.
func (l Licensing) postReport(ctx context.Context, filter json.RawMessage) (licensingDownload, error) {
	body := []byte(filter)
	if len(body) == 0 {
		body = []byte(emptyFilter)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return licensingDownload{}, fmt.Errorf("objects: licensing report filter is not valid JSON: %w", err)
	}

	envelope["@type"] = json.RawMessage(`"ConnectionLicensingReport"`)

	return postJSON[licensingDownload](ctx, l.c, envelope, "ConnectionLicensingReport")
}

// discardDownload fetches and discards a stuck report so a new one can be
// requested. A still-generating answer counts as cleared.
func (l Licensing) discardDownload(ctx context.Context, url string) error {
	stream, err := l.c.Download(ctx, url)
	if err != nil {
		if errors.Is(err, boomi.ErrNotReady) {
			return nil
		}

		return err
	}

	_, _ = io.Copy(io.Discard, stream)
	_ = stream.Close()

	return nil
}

// awaitDownload polls the generated file until it exists and returns its
// bytes. Progress surfaces as AsyncPollEvents — the report can take
// minutes, and the wait is otherwise indistinguishable from a hang.
func (l Licensing) awaitDownload(ctx context.Context, url string, opts *AsyncOptions) ([]byte, error) {
	o := opts.withDefaults()
	obs := observerOf(l.c)
	start := time.Now()
	delay := o.InitialDelay

	for {
		raw, err := l.tryDownload(ctx, url)
		if err == nil {
			return raw, nil
		}

		if !errors.Is(err, boomi.ErrNotReady) {
			return nil, err
		}

		elapsed := time.Since(start)
		if elapsed > o.MaxWait {
			return nil, fmt.Errorf("objects: the licensing report was still generating after %s",
				elapsed.Round(time.Second))
		}

		obs.OnAsyncPoll(progress.AsyncPollEvent{
			Entity:  "ConnectionLicensingReport",
			Message: "waiting for the platform to generate the report",
			Elapsed: elapsed,
			Wait:    delay,
		})

		if sleepErr := sleepCtx(ctx, delay); sleepErr != nil {
			return nil, sleepErr
		}

		if delay < o.MaxDelay {
			delay += time.Second
		}
	}
}

// tryDownload makes one download attempt and reads it fully.
func (l Licensing) tryDownload(ctx context.Context, url string) ([]byte, error) {
	stream, err := l.c.Download(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()

	raw, readErr := io.ReadAll(stream)
	if readErr != nil {
		return nil, fmt.Errorf("objects: reading the licensing report: %w", readErr)
	}

	return raw, nil
}

// pendingReportMessage returns the platform's own text for a blocked
// request, and empty for anything else — so an unrelated 400 is never
// mistaken for one.
func pendingReportMessage(err error) string {
	var apiErr *boomi.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return ""
	}

	body := string(apiErr.Body)
	if !strings.Contains(strings.ToLower(body), "did not download") {
		return ""
	}

	return body
}

// parseLicensingCSV turns the report into rows keyed by its own header.
func parseLicensingCSV(raw []byte) ([]map[string]string, error) {
	reader := csv.NewReader(strings.NewReader(string(raw)))
	// A value containing a comma is quoted, and a strict field count
	// would reject a legitimate folder path.
	reader.FieldsPerRecord = -1

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("objects: reading the licensing report: %w", err)
	}

	// Header only, or nothing at all: an account with no deployed
	// connections. Not an error.
	const headerRow = 1
	if len(records) <= headerRow {
		return nil, nil
	}

	header := records[0]
	out := make([]map[string]string, 0, len(records)-1)

	for _, record := range records[1:] {
		row := make(map[string]string, len(header))

		for i, key := range header {
			if i < len(record) {
				row[strings.TrimSpace(key)] = record[i]
			}
		}

		out = append(out, row)
	}

	return out, nil
}
