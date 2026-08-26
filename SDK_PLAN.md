# Companion SDK and CLI: build plan

Handover for the agent picking this up. Two new repos, both Go, both new work. Nothing here exists yet.

Read this whole file before writing code. Most of the design decisions below were made against measured evidence, and several of them reverse the obvious approach.

---

## 1. What we are building

Two projects.

**Repo 1, the SDK.** A Go module that talks to the Boomi Platform API. Transport, auth, rate limiting, retry, pagination, query filter building. Importable by anything.

**Repo 2, the CLI.** A binary named `companion` that wraps the SDK and produces output an AI agent can act on. This is the artefact we want Boomi to distribute.

A third project already exists and is not in scope: `ata` at `/Users/aaron/development/ata-cli`. It stays internal and private. Once the SDK exists, ata imports it instead of writing its own transport. Do not modify ata as part of this work, but do read its ADRs (section 10) because several decisions here were already made there.

Two repos rather than one module is the owner's call. The cost is real and worth naming: every SDK change that the CLI needs becomes a tagged release plus a `go.mod` bump, and the two versions can drift. Keep the SDK's public API small so that ceremony stays cheap.

---

## 2. Why this exists

The Boomi Companion plugin drives every platform call through 19 bash scripts and one Python file, about 4,300 lines. All HTTP goes through three functions in `boomi-common.sh`: `boomi_curl`, `boomi_api`, `paginate_query`.

Those three functions have no retry, no backoff, no rate limiting, and no handling of 429 or 503. The Boomi Platform API allows roughly 10 requests per second per account and answers 503 above that. A bulk pull is a loop with no pacing. This already locked an Atturra development account once.

An agent makes it worse. Told a command failed, its first instinct is to run it again.

Three more measured problems, all of which the SDK fixes for free:

- A `queryMore` failure mid-pagination prints a warning, breaks the loop, and returns exit code 0. Truncated results are indistinguishable from complete ones. See `boomi-common.sh:218`.
- Four queries never paginate at all and read page one only: `Environment/query`, `Branch/query`, `DeployedPackage/query`, `ExecutionRecord/query`. The last one sits in a file that already sources `paginate_query`.
- `boomi_api` assigns `RESPONSE_CODE=$(boomi_curl ...)` under `set -euo pipefail` with `curl -s`. A connect failure or timeout kills the script with no error message at all.

---

## 3. Repo 1: the SDK

Suggested layout. Adjust if something better emerges, but keep the package boundaries.

```
client.go        transport, auth, exit-code contract
pace/            limiter, retry, auth circuit breaker
query/           filter builder
objects/         endpoint definitions and response envelopes
progress/        observer interface
```

### What belongs in it

**Transport and auth.** Basic auth in Boomi's token form, `BOOMI_TOKEN.$username:$token`. Base URL is `{host}/api/rest/v1/{accountId}`. Partner accounts swap that for `{host}/partner/api/rest/v1/{accountId}` and append `?overrideAccount={subAccount}`. The existing `build_api_url` appends that `?` unconditionally, so it assumes no endpoint carries its own query string. Verify that assumption rather than inheriting it.

**Pacing.** Default 8 requests per second against the documented 10, per account. The limiter belongs to the account, not to a client value, so constructing a fresh client must not reset pacing. Refuse any configured value above 10. Serialise writes. Cap read concurrency inside the paced budget.

**Retry.** Retry 429, 503, other 5xx, and network timeouts with exponential backoff and jitter, honouring `Retry-After`, at most three attempts. Then fail.

**Never retry 401 or 403.** Stop immediately. After two auth rejections in a process, refuse further calls locally rather than sending them. Throttling costs nothing permanent. Repeatedly retrying a rejected credential locks the account and costs somebody an afternoon. This distinction is the single most important behaviour in the package.

**Pagination.** A `/query` call followed by `/queryMore` until the token runs out. It completes or it fails. Never return partial results with a success code. Read `numberOfResults` from the first page and check the collected count against it before returning.

**Query filter builder.** Typed construction of `GroupingExpression` and `SimpleExpression`. This is the highest value part of the SDK and it is small, maybe 200 lines. Today `boomi-branch.sh` and `boomi-execution-query.sh` build request bodies by string interpolation, so a branch name containing a double quote produces invalid JSON, and the XML one escapes nothing.

**Progress observation.** An interface, not printing. The SDK never writes to stdout or stderr.

```go
type Observer interface {
    OnRequest(RequestEvent)
    OnPaced(PacedEvent)
    OnThrottled(ThrottledEvent)
    OnPage(PageEvent)
}
```

The CLI implements it as newline-delimited JSON on stderr. ata implements it with its own output package. Neither has to translate the other's format.

**Exit code contract.** Define the codes in the SDK as typed errors so callers map rather than invent. Match ata's existing contract: 3 for auth, 5 for transport, 7 for stale. Read ata's `SPEC.md` section 3 for the full list before choosing numbers.

### What does not belong in it

**Component XML.** Treat component bodies as opaque bytes. ata has a byte-span preserving XML package that is not transport's business, and keeping it out means Boomi can review the SDK in an afternoon. See section 9 for why that package exists and why you must not reinvent it.

**Boomi's GraphQL and Event Streams JWT endpoints.** Different auth, different shape, used by one script. Out of v0.1.

**Anything that reads or writes the customer workspace.** No `.env` parsing, no `.sync-state`, no `active-development/`. Configuration arrives as a struct. The CLI decides where it came from.

### Endpoint coverage

The plugin calls 28 distinct REST base paths, 30 URL forms counting the `~version` and `~branchId` variants of `Component/{id}`. Build them in tiers.

Tier 1, the bulk and paged calls that cause the pacing problem:

| Object | Verbs |
|---|---|
| `Component/{componentId}` and its `~version` / `~branchId` forms | GET, POST |
| `Component` | POST |
| `ComponentMetadata/query` and `/queryMore` | POST |
| `Folder/query` and `/queryMore` | POST |
| `Branch/query` | POST |

Tier 2, deployment:

`PackagedComponent`, `DeployedPackage` (POST, DELETE, query), `Environment/query`, `EnvironmentExtensions/{environmentId}` (GET, POST), `Folder` (POST).

Tier 3, everything else:

`ExecutionRequest`, `ExecutionRecord/query`, `ExecutionRecord/async/{requestId}`, `ProcessLog`, `MergeRequest` (POST, GET, DELETE), `MergeRequest/execute/{id}`, `Branch` (POST, DELETE), `Atom/query`, `ComponentReference/query`, `ComponentDiffRequest`, `SharedServerInformation/{atomId}`.

---

## 4. Repo 2: the CLI

Binary name `companion`. Product name Companion. No "Boomi" in the name of either.

Do not name it `bc`. That is the POSIX arbitrary precision calculator, present at `/usr/bin/bc` on macOS and every Linux. Installing over it changes the behaviour of any script doing `echo "2+2" | bc`. A user who wants the short form can alias it themselves.

### v0.1 is three commands, not twelve

This is the part most likely to be second-guessed, so here is the reasoning.

```
companion api       <method> <path> [--data -]
companion query     <object> --filter -
companion query-all <object> --filter -
```

Those three replace `boomi_curl`, `boomi_api` and `paginate_query`. One edit, one file, about 90 lines. Every one of the 20 plugin scripts then gets pacing, retry and correct pagination, and the four page-one-only queries become one word changes.

The pitch to Boomi is not "replace your tooling". It is "your `boomi-common.sh` calls ours for the three functions that touch the network". They keep the docs, the domain logic, the release cadence and the brand. It is a change they can read in ten minutes.

Higher level commands, `component push` and `deploy` and the rest, come in v0.2 and later once transport has earned trust. Do not lead with them.

### Agent-ready output

This is the requirement that justifies a separate CLI rather than just publishing the SDK, so get it right.

The problem: an agent shelling out to a command that pauses for 40 seconds while pacing sees a hang. It kills the process and retries, which is the exact loop the rate limiting exists to prevent.

Two streams, two audiences, never interleaved.

**stdout** carries the result. One JSON document, written at exit, with a schema version field.

**stderr** carries progress. Newline-delimited JSON, one object per event.

```json
{"v":1,"ev":"paced","phase":"query-all","obj":"ComponentMetadata","done":340,"total":1200,"rps":8,"eta_s":108}
{"v":1,"ev":"throttled","cause":"http 503","retry_after_s":5,"attempt":2,"max":3}
{"v":1,"ev":"page","done":400,"total":1200,"more":true}
```

Four rules matter more than the exact schema.

**Answer the three questions.** Before an agent kills a run it wants to know whether the work is moving, how much is left, and when it ends. Every event carries all three.

**Name the cause.** "Throttled by the platform, honouring Retry-After 5s, attempt 2 of 3" tells the agent its request was fine and waiting is correct. A progress spinner invites a retry of something that is not broken.

**Heartbeat on a floor.** If nothing has happened for about five seconds, one slow request, emit an event anyway. Silence triggers the kill, not slowness.

**Repeat the summary in the result.** The agent may only ever read stdout. Put the pacing summary there too:

```json
"pacing": {"requests": 1200, "throttled": 3, "waited_ms": 148000}
```

Add `--progress=ndjson|human|none`, defaulting by TTY detection. Somebody running this in a terminal wants a line of text, not JSON.

### Distribution

Static binary, `CGO_ENABLED=0`, `-trimpath`, cross compiled for darwin amd64 and arm64, linux amd64 and arm64, windows amd64. Ship checksums, cosign signatures and an SBOM on every release. ata's goreleaser configuration already does all of this and is worth copying rather than rewriting.

No runtime prerequisites. The current toolchain needs bash, curl, jq and python3 on every machine, which is why a user onboarding guide exists and why Windows is impractical today.

---

## 5. Decisions already made

Do not relitigate these without new evidence.

| Decision | Reason |
|---|---|
| Two repos | Owner's call. Keep the SDK API small so releases stay cheap. |
| Binary named `companion` | `bc` collides with the POSIX calculator at `/usr/bin/bc`. |
| v0.1 is transport only | It is a 90 line change to one upstream file, which is the version Boomi is most likely to accept. |
| XML stays out of the SDK | Not transport's job, and it keeps the review small. |
| Pace at 8, not 10 | The allowance is per account and shared with the customer's production traffic and anyone in the Boomi UI. |
| Never retry 401 or 403 | Backoff makes a rejection slower, not safer. Retrying locks accounts. |
| stdout is the result, stderr is progress | An agent parses one and monitors the other. Interleaving breaks both. |

---

## 6. Decisions still open

Resolve these before or during the first week. Two of them change what gets built.

**The import path.** If the SDK later moves to `github.com/OfficialBoomi/...`, every importer breaks. Either agree the final home with Boomi before the first commit, or serve a vanity import path that we keep control of.

**Whether Boomi adopts.** ata currently intercepts plugin script calls with a `PreToolUse` hook because upstream is not ours to edit (ata ADR-0013). If Boomi ships `companion`, interception stops being the mechanism and becomes a fallback, and ata's Phase 3 changes shape. Settle this before building interception entries for a dozen more commands.

**Repo visibility.** The SDK probably wants to be public from day one if Boomi is going to depend on it. ata is staying private.

**Whether the SDK ships the query filter builder as its main public API or as an internal helper.** It is the most reusable thing in the package and also the part most likely to need breaking changes.

---

## 7. Build order

**Phase 0.** Repo scaffolding for both. Taskfile, golangci-lint, CI, cross platform matrix build, goreleaser. Copy ata's setup. Prove the matrix is green before writing features, because a cgo dependency discovered later sinks the distribution model.

**Phase 1, SDK.** Client, auth, pacing, retry, auth circuit breaker, the observer interface. Tier 1 endpoints. Pagination that completes or fails. Test against `httptest`, including a server that returns 503 with `Retry-After`, one that returns 401 twice, and one that truncates a `queryMore` chain.

**Phase 2, CLI.** The three commands. The stdout and stderr contract. `--progress` modes. Exit codes.

**Phase 3.** Test against a live account. Compare against the existing scripts for the same operations. Then write the patch to `boomi-common.sh` that Boomi would apply, and prove it works locally before proposing it.

**Phase 4.** Tier 2 and 3 endpoints. Higher level commands, if they are still wanted by then.

**Phase 5.** Point ata at the SDK. Delete nothing from ata until the replacement is proven.

---

## 8. Traps

Each of these has already caught somebody.

**Do not use `encoding/xml` or `beevik/etree` to round trip component XML.** Measured against 240 real components: etree with default settings reproduced 78 byte for byte, etree with `CanonicalEndTags: true` reproduced 0, a byte span index over the original buffer reproduced all 240. Boomi emits both empty element forms adjacent in one document, `<bns:encryptedValues/><bns:description></bns:description>`, and a DOM's control over that is document wide. The SDK avoids the whole problem by treating bodies as opaque bytes. If you ever need to edit component XML, use ata's package, do not write a second one.

**Do not put wrapper scripts inside the plugin cache.** The scripts live in a version pinned directory. Editing them edits a versioned install, and the next release lands alongside with every wrapper silently absent. This is not hypothetical: the `resolve_branch_name` patch carried in that cache died between plugin versions 0.5.49 and 1.0.48, exactly as predicted, and nothing announced it.

**Do not retry an auth failure at any level.** Not in the client, not in the CLI, not in a shell loop around either.

**Do not build request bodies by string interpolation.** Use the filter builder. Two existing scripts do it the other way and both break on a quote character in a name.

**Do not let the SDK print.** It has an observer for a reason. A library that writes to stderr is unusable inside a hook that must stay silent on success.

**Do not assume the public plugin repo accepts pull requests.** `github.com/OfficialBoomi/bc-integration` is a CI mirror, rsynced from a private monorepo on merge. There is no issue tracker and feedback goes to an email address. Any upstream change is a conversation, not a PR.

---

## 9. Facts you do not need to re-derive

Measured against plugin version 1.0.48 at
`~/.claude/plugins/cache/boomi-companion/bc-integration/1.0.48/`.

| Fact | Value |
|---|---|
| Plugin scripts | 19 shell, 1 Python, 4,335 lines |
| Plugin markdown | 119 files, 27,831 lines, about 90% of the install |
| Markdown files encoding the script contract | 18, plus `template/.claude/settings.json` |
| Script name mentions in markdown | 326 |
| Distinct REST base paths | 28, or 30 URL forms |
| Non-REST endpoints | 2, `/auth/jwt/generate/{accountId}` and `/graphql`, both Event Streams |
| Platform rate limit | About 10 requests per second per account, 503 above it |
| Tests or CI in any of the five plugin repos | None |

The five plugins in the marketplace are `bc-integration`, `bc-marketplace`, `bc-datahub`, `bc-bdi` and `bc-agentstudio`. Only `bc-integration` uses the Platform API. The other four talk to Data Hub, Rivery and AgentStudio, roughly 3,850 more lines of shell, and they are out of scope.

The plugin's real output contract is side effect files, not stdout: `active-development/.sync-state/*.json`, `inventories/`, `feedback/execution-results/`, and in place mutation of the component XML the agent passed in. A transport only CLI touches none of that, which is another reason to keep v0.1 narrow.

---

## 10. Reference material

Read these before designing. They are short.

In `/Users/aaron/development/ata-cli/docs/adr/`:

- `0004-binary-distribution.md`. The distribution model, already implemented in goreleaser.
- `0008-parse-and-preserve-xml.md`. Why the XML trap above exists, with the measurements.
- `0012-rate-limiting.md`. The pacing and retry design. The SDK should implement this ADR almost verbatim.
- `0013-migration-by-interception.md`. Why we do not edit the plugin, and what changes if Boomi adopts.

Also in that repo: `SPEC.md` section 3 for the exit code contract, `ENGINEERING.md` for the lint and CI setup worth copying.

The plugin itself, for endpoint shapes and the query body formats the platform actually accepts:
`~/.claude/plugins/cache/boomi-companion/bc-integration/1.0.48/skills/boomi-integration/scripts/`.

Start with `boomi-common.sh`, then `boomi-component-search.sh`. The second one is the best written script in the plugin and shows the query patterns done properly.
