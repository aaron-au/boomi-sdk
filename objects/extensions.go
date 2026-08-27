package objects

import (
	"context"
	"encoding/json"
	"errors"

	boomi "github.com/aaron-au/boomi-sdk"
)

// ExtensionField is one overridable connection field.
//
// UsesEncryption marks a field as encryptable; EncryptedValueSet marks it
// as actually holding a value. Encrypted values are never readable over
// the API — the pair identifies fields that genuinely need manual
// re-entry after a migration; see NeedsManualEntry.
type ExtensionField struct {
	ID                string `json:"id"`
	Value             string `json:"value"`
	EncryptedValueSet bool   `json:"encryptedValueSet"`
	UsesEncryption    bool   `json:"usesEncryption"`
	ComponentOverride bool   `json:"componentOverride"`
	UseDefault        bool   `json:"useDefault"`
}

// NeedsManualEntry reports a field whose value cannot be read out and so
// cannot cross an environment copy.
func (f ExtensionField) NeedsManualEntry() bool { return f.UsesEncryption && f.EncryptedValueSet }

// ExtensionConnection is one connection's environment overrides.
type ExtensionConnection struct {
	ID    string           `json:"id"`
	Name  string           `json:"name"`
	Field []ExtensionField `json:"field"`
}

// ExtensionProperty is an environment-level process property override.
type ExtensionProperty struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// EnvironmentExtensions carries every override applied to an environment.
// The blocks this SDK does not model (process properties, cross
// references, PGP certificates) are kept raw so nothing the platform
// sends is lost.
type EnvironmentExtensions struct {
	ID            string `json:"id"`
	EnvironmentID string `json:"environmentId"`
	// Partial applies only to an update: true writes just the supplied
	// fields, false (the platform's default) replaces the environment's
	// whole extension set. It is omitempty so a payload read back from a
	// GET cannot carry an accidental full-replace instruction into a
	// later write.
	Partial     bool `json:"partial,omitempty"`
	Connections struct {
		Connection []ExtensionConnection `json:"connection"`
	} `json:"connections"`
	Properties struct {
		Property []ExtensionProperty `json:"property"`
	} `json:"properties"`
	ProcessProperties json.RawMessage `json:"processProperties,omitempty"`
	CrossReferences   json.RawMessage `json:"crossReferences,omitempty"`
	PGPCertificates   json.RawMessage `json:"PGPCertificates,omitempty"`
}

// ErrExtensionWriteUnconfirmed guards environment extension writes: they
// change live connection settings, so the caller must set Confirmed.
var ErrExtensionWriteUnconfirmed = errors.New(
	"objects: environment extension writes change live connection settings; set Confirmed",
)

// Extensions accesses EnvironmentExtensions objects.
type Extensions struct {
	c *boomi.Client
}

// NewExtensions returns an Extensions service over c.
func NewExtensions(c *boomi.Client) Extensions {
	return Extensions{c: c}
}

// Get returns an environment's extension overrides:
// GET EnvironmentExtensions/{environmentId}. Encrypted values come back
// masked — see ExtensionField.
func (e Extensions) Get(ctx context.Context, environmentID string) (EnvironmentExtensions, error) {
	if environmentID == "" {
		return EnvironmentExtensions{}, errEmptyID("environment")
	}

	return getJSON[EnvironmentExtensions](ctx, e.c, "EnvironmentExtensions", environmentID)
}

// UpdateExtensionsRequest writes connection field and property overrides
// into an environment.
//
// The default is a partial write — only the supplied fields change.
// FullReplace asks the platform to treat the payload as the environment's
// entire extension set, deleting anything omitted; the zero value is the
// safe one deliberately.
type UpdateExtensionsRequest struct {
	EnvironmentID string
	Connections   []ExtensionConnection
	Properties    []ExtensionProperty
	FullReplace   bool
	// Confirmed must be set by the caller; see
	// ErrExtensionWriteUnconfirmed.
	Confirmed bool
}

// extensionsWire is the POST EnvironmentExtensions body.
type extensionsWire struct {
	Type          string `json:"@type"`
	EnvironmentID string `json:"environmentId"`
	Partial       bool   `json:"partial"`
	Connections   struct {
		Connection []ExtensionConnection `json:"connection"`
	} `json:"connections"`
	Properties struct {
		Property []ExtensionProperty `json:"property"`
	} `json:"properties"`
}

// Update applies extension overrides to an environment:
// POST EnvironmentExtensions/{environmentId}.
//
// Encrypted values cannot be carried: the platform never discloses them
// on read, so there is nothing to send. Filter those out before calling —
// see ExtensionField.NeedsManualEntry.
func (e Extensions) Update(ctx context.Context, req UpdateExtensionsRequest) (EnvironmentExtensions, error) {
	if !req.Confirmed {
		return EnvironmentExtensions{}, ErrExtensionWriteUnconfirmed
	}

	if req.EnvironmentID == "" {
		return EnvironmentExtensions{}, errEmptyID("environment")
	}

	if len(req.Connections) == 0 && len(req.Properties) == 0 {
		return EnvironmentExtensions{}, errors.New("objects: extension update has nothing to write")
	}

	body := extensionsWire{
		Type:          "EnvironmentExtensions",
		EnvironmentID: req.EnvironmentID,
		Partial:       !req.FullReplace,
	}
	body.Connections.Connection = req.Connections
	body.Properties.Property = req.Properties

	return postJSON[EnvironmentExtensions](ctx, e.c, body, "EnvironmentExtensions", req.EnvironmentID)
}
