package objects

import (
	"context"
	"encoding/json"
	"errors"

	boomi "github.com/aaron-au/boomi-sdk"
)

// typeProcessProperty is the wire @type for a persisted process
// property.
const typeProcessProperty = "ProcessProperty"

// ProcessProperty is one name/value pair persisted against a process.
type ProcessProperty struct {
	Type  string `json:"@type,omitempty"`
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// processPropertyList wraps the property slice; the wrapper is a wire
// format detail, reached through DeployedProcess.Properties and
// NewDeployedProcess.
type processPropertyList struct {
	Type            string            `json:"@type,omitempty"`
	ProcessProperty []ProcessProperty `json:"ProcessProperty"`
}

// DeployedProcess pairs a process with its persisted properties on a
// runtime.
type DeployedProcess struct {
	Type              string              `json:"@type,omitempty"`
	ProcessID         string              `json:"processId"`
	ProcessProperties processPropertyList `json:"ProcessProperties"`
}

// Properties returns the persisted properties for this process.
func (d DeployedProcess) Properties() []ProcessProperty { return d.ProcessProperties.ProcessProperty }

// NewDeployedProcess builds a process's property set for a write.
func NewDeployedProcess(processID string, props []ProcessProperty) DeployedProcess {
	return DeployedProcess{
		Type:      "DeployedProcess",
		ProcessID: processID,
		ProcessProperties: processPropertyList{
			Type:            "ProcessProperties",
			ProcessProperty: props,
		},
	}
}

// PersistedProcessProperties is the runtime-local property state that is
// otherwise only on the runtime's disk. Readable over the API even for
// cloud runtimes.
//
// The update operation is a FULL REPLACE for the whole runtime: any
// property not present in the payload is deleted. Always read, modify
// with Upsert, then write the complete set back.
type PersistedProcessProperties struct {
	Type    string            `json:"@type,omitempty"`
	AtomID  string            `json:"atomId"`
	Process []DeployedProcess `json:"Process"`
}

// Upsert sets one property on one process, adding the process if absent.
//
// This is the modify half of the read-modify-write the full-replace
// update demands. Nothing is ever removed here — a property that
// disappears from the payload is deleted on the runtime, and that must be
// a deliberate act, not a side effect of merging.
func (p *PersistedProcessProperties) Upsert(processID, name, value string) {
	for i, proc := range p.Process {
		if proc.ProcessID != processID {
			continue
		}

		props := proc.Properties()
		for j := range props {
			if props[j].Name == name {
				props[j].Value = value
				return
			}
		}

		p.Process[i] = NewDeployedProcess(processID,
			append(props, ProcessProperty{Type: typeProcessProperty, Name: name, Value: value}))

		return
	}

	p.Process = append(p.Process, NewDeployedProcess(processID,
		[]ProcessProperty{{Type: typeProcessProperty, Name: name, Value: value}}))
}

// RuntimeProperties is container.properties for a local Atom or Molecule.
// Cloud attachments reject this object — check Atom.IsCloud first and use
// CloudAttachmentProperties instead. The property groups are kept raw so
// every field the platform sends is captured.
type RuntimeProperties struct {
	RuntimeID               string          `json:"runtimeId"`
	StandardProperties      json.RawMessage `json:"standardProperties"`
	SystemProperties        json.RawMessage `json:"systemProperties"`
	CustomRuntimeProperties json.RawMessage `json:"customRuntimeProperties"`
	CustomSystemProperties  json.RawMessage `json:"customSystemProperties"`
}

// AccountCloudAttachmentProperties is the cloud-attachment analogue of
// RuntimeProperties. The schema does not line up with RuntimeProperties —
// carrying settings from one to the other is a translation, not a copy.
type AccountCloudAttachmentProperties struct {
	NumberOfAtomWorkers        int    `json:"numberofAtomWorkers"`
	MinNumberOfAtomWorkers     int    `json:"minNumberofAtomWorkers"`
	AtomInputSize              int64  `json:"atomInputSize"`
	WorkerMaxExecutionTime     int64  `json:"workerMaxExecutionTime"`
	WorkerMaxGeneralExecTime   int64  `json:"workerMaxGeneralExecutionTime"`
	WorkerMaxRunningExecutions int    `json:"workerMaxRunningExecutions"`
	WorkerMaxQueuedExecutions  int    `json:"workerMaxQueuedExecutions"`
	CloudAccountExecutionLimit int    `json:"cloudAccountExecutionLimit"`
	ListenerMaxConcurrentExecs int    `json:"listenerMaxConcurrentExecutions"`
	QueueIncomingMsgRateLimit  int    `json:"queueIncomingMessageRateLimit"`
	HTTPWorkload               string `json:"httpWorkload"`
	AS2Workload                string `json:"as2Workload"`
	AccountDiskUsage           int64  `json:"accountDiskUsage"`
}

// ErrFullReplaceUnconfirmed guards the most destructive write in the API:
// a PersistedProcessProperties update replaces the runtime's entire
// property set, deleting anything omitted.
var ErrFullReplaceUnconfirmed = errors.New(
	"objects: a PersistedProcessProperties update is a FULL REPLACE for the runtime — " +
		"any property omitted is deleted; pass the complete read-modify-write set and set Confirmed",
)

// RuntimeProps accesses runtime property objects. The reads are
// runtime-backed async operations and can take minutes on a live cloud
// runtime; progress surfaces as AsyncPollEvents on the client's observer.
type RuntimeProps struct {
	c *boomi.Client
}

// NewRuntimeProps returns a RuntimeProps service over c.
func NewRuntimeProps(c *boomi.Client) RuntimeProps {
	return RuntimeProps{c: c}
}

// Persisted reads the runtime's persisted process properties. Reading is
// safe; see UpdatePersisted for the destructive counterpart.
func (r RuntimeProps) Persisted(
	ctx context.Context,
	runtimeID string,
	opts *AsyncOptions,
) (PersistedProcessProperties, error) {
	res, err := AsyncGet(ctx, r.c, "PersistedProcessProperties", runtimeID, opts)
	if err != nil {
		return PersistedProcessProperties{}, err
	}

	items, err := DecodeAsync[PersistedProcessProperties](res)
	if err != nil {
		return PersistedProcessProperties{}, err
	}

	if len(items) == 0 {
		return PersistedProcessProperties{AtomID: runtimeID}, nil
	}

	return items[0], nil
}

// UpdatePersistedRequest wraps the full-replace write so it cannot be
// made by accident. Payload must be the complete property set for the
// runtime, obtained by reading first and merging with Upsert — not a
// partial patch.
type UpdatePersistedRequest struct {
	Payload PersistedProcessProperties
	// Confirmed must be set by the caller after a read-modify-write; see
	// ErrFullReplaceUnconfirmed.
	Confirmed bool
}

// UpdatePersisted performs the full-replace write:
// POST PersistedProcessProperties/{atomId}.
func (r RuntimeProps) UpdatePersisted(
	ctx context.Context,
	req UpdatePersistedRequest,
) (PersistedProcessProperties, error) {
	if !req.Confirmed {
		return PersistedProcessProperties{}, ErrFullReplaceUnconfirmed
	}

	if req.Payload.AtomID == "" {
		return PersistedProcessProperties{}, errEmptyID("atom")
	}

	body := req.Payload
	body.Type = "PersistedProcessProperties"

	return postJSON[PersistedProcessProperties](ctx, r.c, body, "PersistedProcessProperties", req.Payload.AtomID)
}

// Runtime reads container.properties for a local runtime. Cloud
// attachments reject this with a not-found shape — check Atom.IsCloud
// first, or treat the failure as "use CloudAttachmentProperties instead".
func (r RuntimeProps) Runtime(ctx context.Context, runtimeID string, opts *AsyncOptions) (RuntimeProperties, error) {
	res, err := AsyncGet(ctx, r.c, "RuntimeProperties", runtimeID, opts)
	if err != nil {
		return RuntimeProperties{}, err
	}

	items, err := DecodeAsync[RuntimeProperties](res)
	if err != nil || len(items) == 0 {
		return RuntimeProperties{}, err
	}

	return items[0], nil
}

// customRuntimePropertyWire is the POST RuntimeProperties/{id} body for a
// single-property write. Both flags go as strings because that is how the
// platform's own example sends them.
type customRuntimePropertyWire struct {
	RuntimeID               string            `json:"runtimeId"`
	PartialUpdate           string            `json:"partialUpdate"`
	ShouldRestartRuntime    string            `json:"shouldRestartRuntime"`
	CustomRuntimeProperties map[string]string `json:"customRuntimeProperties"`
}

// SetCustom writes ONE custom runtime property, leaving every other
// property alone: partialUpdate is what makes that true — without it the
// platform treats the request as the whole property set and everything
// absent is lost. The runtime is not restarted.
func (r RuntimeProps) SetCustom(ctx context.Context, runtimeID, name, value string) error {
	if runtimeID == "" || name == "" {
		return errors.New("objects: a custom runtime property needs a runtime id and a name")
	}

	body := customRuntimePropertyWire{
		RuntimeID:               runtimeID,
		PartialUpdate:           "true",
		ShouldRestartRuntime:    "false",
		CustomRuntimeProperties: map[string]string{name: value},
	}

	_, err := postJSON[json.RawMessage](ctx, r.c, body, "RuntimeProperties", runtimeID)

	return err
}

// CloudAttachment reads the cloud-attachment property set:
// async AccountCloudAttachmentProperties/{runtimeId}.
func (r RuntimeProps) CloudAttachment(
	ctx context.Context,
	runtimeID string,
	opts *AsyncOptions,
) (AccountCloudAttachmentProperties, error) {
	res, err := AsyncGet(ctx, r.c, "AccountCloudAttachmentProperties", runtimeID, opts)
	if err != nil {
		return AccountCloudAttachmentProperties{}, err
	}

	items, err := DecodeAsync[AccountCloudAttachmentProperties](res)
	if err != nil || len(items) == 0 {
		return AccountCloudAttachmentProperties{}, err
	}

	return items[0], nil
}
