package objects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/progress"
)

// The async API is the platform's second request shape: GET
// async/{entity}/{id} or POST async/{entity}/query answers with a token,
// and GET async/{entity}/response/{token} answers 202 with an
// AsyncOperationStatus row until the payload settles. Runtime-backed
// entities (PersistedProcessProperties, ListQueues) can sit at
// "Connecting to runtime..." for minutes while the platform reaches the
// runtime; platform-backed ones settle in seconds.

// asyncSegment is the async API's leading path segment.
const asyncSegment = "async"

// Default poll pacing. MaxWait is generous on purpose — see above.
const (
	defaultAsyncInitialDelay = 2 * time.Second
	defaultAsyncMaxDelay     = 10 * time.Second
	defaultAsyncMaxWait      = 15 * time.Minute
)

// AsyncOptions tunes the token-polling loop. The zero value (and nil)
// selects the defaults: poll every 2s growing to 10s, give up after 15m.
// Each in-progress poll is reported to the client's observer as an
// AsyncPollEvent, so a long wait never reads as a hang.
type AsyncOptions struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	MaxWait      time.Duration
}

// withDefaults fills unset fields.
func (o *AsyncOptions) withDefaults() AsyncOptions {
	out := AsyncOptions{
		InitialDelay: defaultAsyncInitialDelay,
		MaxDelay:     defaultAsyncMaxDelay,
		MaxWait:      defaultAsyncMaxWait,
	}
	if o == nil {
		return out
	}

	if o.InitialDelay > 0 {
		out.InitialDelay = o.InitialDelay
	}

	if o.MaxDelay > 0 {
		out.MaxDelay = o.MaxDelay
	}

	if o.MaxWait > 0 {
		out.MaxWait = o.MaxWait
	}

	return out
}

// asyncTokenEnvelope covers the three token shapes the platform returns.
type asyncTokenEnvelope struct {
	AsyncOperationTokenResult *struct {
		Token string `json:"token"`
	} `json:"AsyncOperationTokenResult"`
	AsyncToken *struct {
		Token string `json:"token"`
	} `json:"asyncToken"`
	// ListQueues answers with a session id instead of a token.
	QueueMessageResponse *struct {
		SessionID string `json:"sessionId"`
	} `json:"QueueMessageResponse"`
}

// token returns whichever token the envelope carries, or "".
func (e asyncTokenEnvelope) token() string {
	switch {
	case e.AsyncOperationTokenResult != nil && e.AsyncOperationTokenResult.Token != "":
		return e.AsyncOperationTokenResult.Token
	case e.AsyncToken != nil && e.AsyncToken.Token != "":
		return e.AsyncToken.Token
	case e.QueueMessageResponse != nil && e.QueueMessageResponse.SessionID != "":
		return e.QueueMessageResponse.SessionID
	default:
		return ""
	}
}

// AsyncResult is the settled payload of an async operation. Decode the
// rows with DecodeAsync.
type AsyncResult struct {
	ResponseStatusCode int               `json:"responseStatusCode"`
	NumberOfResults    *int              `json:"numberOfResults"`
	Result             []json.RawMessage `json:"result"`
}

// statusMessage returns the platform's in-progress message while the
// operation is still working, and ok=false once the payload has settled.
//
// "Still working" is 202 with a single AsyncOperationStatus row. The
// platform also answers 202 for a settled-but-empty result
// (numberOfResults 0 with no rows) — polling that forever is the obvious
// trap, so a present numberOfResults counts as settled.
func (r AsyncResult) statusMessage() (message string, working bool) {
	if r.ResponseStatusCode != http.StatusAccepted {
		return "", false
	}

	if len(r.Result) == 0 {
		if r.NumberOfResults != nil {
			return "", false
		}

		return "waiting for platform", true
	}

	var probe struct {
		Type    string `json:"@type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(r.Result[0], &probe); err != nil || probe.Type != "AsyncOperationStatus" {
		return "", false
	}

	if probe.Message == "" {
		return "in progress", true
	}

	return probe.Message, true
}

// DecodeAsync unmarshals an async result's rows into a typed slice.
func DecodeAsync[T any](r AsyncResult) ([]T, error) {
	out := make([]T, 0, len(r.Result))

	for _, raw := range r.Result {
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("objects: decoding async result row: %w", err)
		}

		out = append(out, item)
	}

	return out, nil
}

// AsyncGet runs the async GET-by-id flow: GET async/{entity}/{id} for a
// token, then poll GET async/{entity}/response/{token} until the payload
// settles.
func AsyncGet(
	ctx context.Context,
	c *boomi.Client,
	entity, id string,
	opts *AsyncOptions,
) (AsyncResult, error) {
	if err := checkAsyncArgs(c, entity); err != nil {
		return AsyncResult{}, err
	}

	if id == "" {
		return AsyncResult{}, errors.New("objects: empty id")
	}

	env, err := doJSON[asyncTokenEnvelope](ctx, c, boomi.Request{
		Method: http.MethodGet,
		Path:   []string{asyncSegment, entity, id},
		Accept: contentTypeJSON,
		Class:  boomi.ClassRead,
	})
	if err != nil {
		return AsyncResult{}, err
	}

	return pollAsync(ctx, c, entity, env, opts)
}

// AsyncQuery runs the async query flow: POST async/{entity}/query with
// the given filter body for a token, then poll the same response
// endpoint. filter is the full request body ({"QueryFilter":{…}}); nil or
// empty selects the match-everything filter.
func AsyncQuery(
	ctx context.Context,
	c *boomi.Client,
	entity string,
	filter json.RawMessage,
	opts *AsyncOptions,
) (AsyncResult, error) {
	if err := checkAsyncArgs(c, entity); err != nil {
		return AsyncResult{}, err
	}

	body := []byte(filter)
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte(emptyFilter)
	}

	env, err := doJSON[asyncTokenEnvelope](ctx, c, boomi.Request{
		Method:      http.MethodPost,
		Path:        []string{asyncSegment, entity, querySegment},
		Body:        bytes.NewReader(body),
		ContentType: contentTypeJSON,
		Accept:      contentTypeJSON,
		Class:       boomi.ClassRead,
	})
	if err != nil {
		return AsyncResult{}, err
	}

	return pollAsync(ctx, c, entity, env, opts)
}

// checkAsyncArgs rejects the argument states every async entry point
// shares.
func checkAsyncArgs(c *boomi.Client, entity string) error {
	if c == nil {
		return errors.New("objects: nil client")
	}

	if entity == "" {
		return errors.New("objects: empty entity")
	}

	return nil
}

// pollAsync follows the response endpoint until the payload settles, the
// options' MaxWait elapses, or ctx ends.
func pollAsync(
	ctx context.Context,
	c *boomi.Client,
	entity string,
	env asyncTokenEnvelope,
	opts *AsyncOptions,
) (AsyncResult, error) {
	o := opts.withDefaults()

	token := env.token()
	if token == "" {
		return AsyncResult{}, fmt.Errorf("objects: %s async request returned no token", entity)
	}

	obs := observerOf(c)
	start := time.Now()
	delay := o.InitialDelay

	for {
		res, err := doJSON[AsyncResult](ctx, c, boomi.Request{
			Method: http.MethodGet,
			Path:   []string{asyncSegment, entity, "response", token},
			Accept: contentTypeJSON,
			Class:  boomi.ClassRead,
		})
		if err != nil {
			return AsyncResult{}, err
		}

		msg, working := res.statusMessage()
		if !working {
			return res, nil
		}

		elapsed := time.Since(start)
		if elapsed > o.MaxWait {
			return res, fmt.Errorf(
				"objects: %s async operation still %q after %s",
				entity,
				msg,
				elapsed.Round(time.Second),
			)
		}

		obs.OnAsyncPoll(progress.AsyncPollEvent{Entity: entity, Message: msg, Elapsed: elapsed, Wait: delay})

		if sleepErr := sleepCtx(ctx, delay); sleepErr != nil {
			return AsyncResult{}, sleepErr
		}

		if delay < o.MaxDelay {
			delay += time.Second
		}
	}
}

// sleepCtx pauses for d or until ctx is done, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
