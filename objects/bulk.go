package objects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/progress"
)

// bulkChunk is the platform's per-request ceiling for bulk GET: at most
// 100 ids per call.
const bulkChunk = 100

// BulkMiss records one id the platform rejected inside an otherwise
// successful bulk GET — typically a deleted or never-existing object
// answered with a per-entry 404.
type BulkMiss struct {
	ID         string
	StatusCode int
}

// bulkRequest is the POST {entity}/bulk envelope.
type bulkRequest struct {
	Type    string            `json:"type"`
	Request []bulkRequestItem `json:"request"`
}

type bulkRequestItem struct {
	ID string `json:"id"`
}

// bulkResponse is the platform's reply: one response entry per requested
// id, in request order.
type bulkResponse struct {
	Response []struct {
		Result     json.RawMessage `json:"Result"`
		StatusCode int             `json:"statusCode"`
	} `json:"response"`
}

// BulkGet fetches many objects by id via POST {entity}/bulk, chunking at
// the platform's 100-id ceiling. Results come back in request order.
//
// Entries the platform rejects individually are returned as BulkMisses,
// never silently dropped: len(results)+len(misses) always equals len(ids)
// on a nil error. A response whose entry count disagrees with the request
// is an error wrapping boomi.ErrTruncated — partial results are never
// passed off as complete.
//
// A PageEvent is emitted per chunk.
func BulkGet[T any](ctx context.Context, c *boomi.Client, entity string, ids []string) ([]T, []BulkMiss, error) {
	if c == nil {
		return nil, nil, errors.New("objects: nil client")
	}

	if entity == "" {
		return nil, nil, errors.New("objects: empty entity")
	}

	obs := observerOf(c)
	results := make([]T, 0, len(ids))

	var misses []BulkMiss

	for start := 0; start < len(ids); start += bulkChunk {
		end := min(start+bulkChunk, len(ids))

		chunkResults, chunkMisses, err := bulkChunkGet[T](ctx, c, entity, ids[start:end])
		if err != nil {
			return nil, nil, err
		}

		results = append(results, chunkResults...)
		misses = append(misses, chunkMisses...)

		obs.OnPage(progress.PageEvent{Entity: entity, Done: end, Total: len(ids), More: end < len(ids)})
	}

	return results, misses, nil
}

// bulkChunkGet sends one bulk call of at most bulkChunk ids.
func bulkChunkGet[T any](ctx context.Context, c *boomi.Client, entity string, ids []string) ([]T, []BulkMiss, error) {
	req := bulkRequest{Type: "GET", Request: make([]bulkRequestItem, len(ids))}
	for i, id := range ids {
		req.Request[i] = bulkRequestItem{ID: id}
	}

	resp, err := postJSON[bulkResponse](ctx, c, req, entity, "bulk")
	if err != nil {
		return nil, nil, err
	}

	// Miss attribution relies on the platform answering one entry per
	// requested id, in request order. A count mismatch breaks that
	// mapping, so it fails rather than guessing.
	if len(resp.Response) != len(ids) {
		return nil, nil, fmt.Errorf("objects: %s bulk answered %d entries for %d ids: %w",
			entity, len(resp.Response), len(ids), boomi.ErrTruncated)
	}

	results := make([]T, 0, len(ids))

	var misses []BulkMiss

	for i, entry := range resp.Response {
		if entry.StatusCode != http.StatusOK || len(entry.Result) == 0 {
			misses = append(misses, BulkMiss{ID: ids[i], StatusCode: entry.StatusCode})
			continue
		}

		var item T
		if decodeErr := json.Unmarshal(entry.Result, &item); decodeErr != nil {
			return nil, nil, fmt.Errorf("objects: decoding %s bulk entry for %q: %w", entity, ids[i], decodeErr)
		}

		results = append(results, item)
	}

	return results, misses, nil
}
