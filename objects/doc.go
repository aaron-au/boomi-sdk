// Package objects provides typed access to Boomi Platform API objects:
// the generic query/queryMore pagination engine and thin services over
// the tier-1 endpoints (Component, ComponentMetadata, Folder, Branch).
//
// Pagination completes or it fails. QueryAll never returns partial
// results with a nil error: any queryMore failure aborts with zero
// results, and a final count that disagrees with the platform's own
// numberOfResults from the first page returns an error wrapping
// boomi.ErrTruncated.
//
// Component bodies are opaque. The Components service returns the raw
// XML stream and never parses it; parsing is the caller's business.
package objects
