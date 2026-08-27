// Package objects provides typed access to Boomi Platform API objects:
// the generic engines (query/queryMore pagination, bulk GET, the async
// token-poll API) and thin services over the object families — build
// (Component, ComponentMetadata, Folder, Branch, MergeRequest,
// ComponentReference, ComponentDiffRequest), deployment
// (PackagedComponent, DeployedPackage, Environment,
// EnvironmentExtensions), execution (ExecutionRequest, ExecutionRecord,
// ProcessLog), runtime operations (Atom, ProcessSchedules,
// PersistedProcessProperties, RuntimeProperties, ListQueues,
// ListenerStatus, SharedWebServer, map extensions, certificates), and
// account administration (Account, AccountUserRole, Role, the connection
// licensing report).
//
// Pagination completes or it fails. QueryAll never returns partial
// results with a nil error: any queryMore failure aborts with zero
// results, and a final count that disagrees with the platform's own
// numberOfResults from the first page returns an error wrapping
// boomi.ErrTruncated. BulkGet holds the same line: per-id rejections come
// back as explicit misses, and a response that answers fewer entries than
// were asked for is an error, not a shorter list.
//
// Component bodies are opaque. The Components service returns the raw
// XML stream and never parses it; parsing is the caller's business.
// The typed services speak JSON, matching the platform's own content
// negotiation — and every object is also reachable untyped through the
// Raw service, which streams any object path in XML or JSON exactly as
// the caller wrote it, for tooling (the Boomi Companion plugin among it)
// that reads and writes platform documents rather than structs.
//
// Writes that change a live environment or runtime — deploy and
// undeploy, schedule updates, extension updates, web server updates,
// branch and merge writes, the persisted-property full replace — carry a
// Confirmed field (or argument) and refuse to act until it is set, so
// none of them can happen as a side effect of anything else.
package objects
