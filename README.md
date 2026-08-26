# boomi-sdk

A Go SDK for the Boomi Platform API. It handles the parts every Boomi client
ends up rewriting: transport and authentication, per-account rate pacing,
retry with backoff, a circuit breaker on auth failures, complete-or-fail
pagination, typed query methods over the common objects, and an observer
interface for progress. Component XML passes through as opaque bytes by
design; the SDK moves it and never interprets it.

The SDK depends on the Go standard library and nothing else, in tests too.
`go.sum` is empty on purpose, so the supply chain is the Go team's and ours.

## Install

```
go get github.com/aaron-au/boomi-sdk
```

Requires Go 1.26 or later.

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/objects"
)

func main() {
	client, err := boomi.New(boomi.Config{
		Host:      "https://api.boomi.com",
		AccountID: os.Getenv("BOOMI_ACCOUNT"),
		Username:  os.Getenv("BOOMI_USER"),
		Token:     os.Getenv("BOOMI_TOKEN"),
	})
	if err != nil {
		log.Fatal(err)
	}

	rows, err := objects.NewMetadata(client).CurrentByNameLike(context.Background(), "%Invoice%")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(len(rows), "components")
}
```

Credentials come from wherever the caller wants. The SDK takes a struct and
never reads environment variables or files; the `os.Getenv` above is the
caller's choice, not the SDK's.

## Why the pacing is opinionated

The platform allows roughly 10 requests per second per account and answers
503 above that. The allowance is per account, not per process: the
customer's production integrations and anyone working in the Boomi UI draw
on the same budget. So the SDK paces at 8 by default, refuses any
configured rate above 10, serialises writes, and caps read concurrency
inside the paced budget.

The limiter is process-wide and belongs to the account, not to a `Client`
value. Constructing a fresh client never resets pacing, and configuration
can only tighten it.

Throttling and lockout are different mechanisms. Exceeding the rate limit
costs nothing permanent, so 429, 503, other 5xx, and network timeouts are
retried with jittered exponential backoff, honouring `Retry-After`, three
wire sends at most. Retrying a rejected credential locks the account, so
401 and 403 are never retried, at any level — and after two auth
rejections in a process the circuit opens and further calls fail locally
with `ErrCircuitOpen` instead of reaching the wire.

## Error handling

Errors carry a `Kind`, retrieved with `KindOf`. The numbers are exit codes
callers may map to; the SDK itself never calls `os.Exit`.

| Kind             | Covers                                       | Suggested exit |
| ---------------- | -------------------------------------------- | -------------- |
| `KindValidation` | 400, 404, other non-retryable 4xx            | 2              |
| `KindAuth`       | 401, 403, open circuit                       | 3              |
| `KindConflict`   | 409                                          | 4              |
| `KindTransport`  | network failure, timeout, 5xx, 429, truncation | 5            |

Sentinels, tested with `errors.Is`:

- `ErrCircuitOpen` — two auth rejections in this process; the SDK refuses
  further calls locally.
- `ErrAuth` — the platform rejected the credential (401/403). Never
  retried.
- `ErrTruncated` — a paginated query collected fewer results than the
  platform reported. Partial results are never returned with a nil error.

Quirk predicates detect platform behaviours that hide behind generic
status codes. They are detection only; what to do about a match is the
caller's decision.

- `IsBranchUnlicensed(err)` — the account lacks the branching feature; the
  platform refuses with a generic 400/403.
- `IsDuplicateDeploy(err)` — the deployment already exists; the platform
  rejects it as a 400.
- `IsLogNotReady(err)` — an execution log is still being assembled; the
  platform answers 400 until it is ready.

## Progress observation

The SDK never writes to stdout or stderr. Requests, pacing waits,
throttled retries, and collected pages are delivered to the
`progress.Observer` supplied in `Config`; the default `progress.Nop`
discards everything.

```go
type Observer interface {
	OnRequest(RequestEvent)
	OnPaced(PacedEvent)
	OnThrottled(ThrottledEvent)
	OnPage(PageEvent)
}
```

A CLI might render events as newline-delimited JSON on stderr, so an agent
watching a long run can tell a deliberate pacing wait from a hang:

```json
{"ev":"paced","waited_ms":125,"rps":8}
{"ev":"throttled","cause":"http 503","retry_after_s":5,"attempt":2,"max":3}
{"ev":"page","entity":"ComponentMetadata","done":400,"total":1200,"more":true}
```

## Stability

v0.x. The API may change between minor versions. Requests and responses
are XML today where the platform requires it and JSON for queries; broader
JSON support is planned. The import path may gain a vanity domain before
v1.

## License

Apache-2.0. See [LICENSE](LICENSE).
