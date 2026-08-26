package objects

import (
	"context"
	"encoding/json"

	boomi "github.com/aaron-au/boomi-sdk"
	"github.com/aaron-au/boomi-sdk/internal/query"
)

// QueueRecord is one message queue on a runtime, with its depth and
// dead-letter count — the drain check before cutting a queue-consuming
// process over.
//
// The counts arrive as JSON numbers from a live runtime and as strings in
// some responses; json.Number accepts both.
type QueueRecord struct {
	QueueName         string      `json:"queueName"`
	QueueType         string      `json:"queueType"`
	MessagesCount     json.Number `json:"messagesCount"`
	DeadLettersCount  json.Number `json:"deadLettersCount"`
	SubscriberName    string      `json:"subscriberName"`
	TopicSubscription bool        `json:"topicSubscription"`
}

// listQueuesResult is what the async ListQueues call settles to: a
// wrapper around the records, not the records themselves. Decoding each
// result element as a QueueRecord produces empty objects.
type listQueuesResult struct {
	QueueRecord []QueueRecord `json:"QueueRecord"`
}

// ListenerStatus is one active listener on a runtime. Entries do not
// expose the listener's HTTP path — join ListenerID (the process
// component id) back to the process to resolve a route.
type ListenerStatus struct {
	ListenerID    string `json:"listenerId"`
	ContainerID   string `json:"containerId"`
	Status        string `json:"status"`
	ConnectorType string `json:"connectorType"`
}

// Runtime reads runtime-backed operational state: queues and listeners.
// Both are async operations against a live runtime; progress surfaces as
// AsyncPollEvents on the client's observer.
type Runtime struct {
	c *boomi.Client
}

// NewRuntime returns a Runtime service over c.
func NewRuntime(c *boomi.Client) Runtime {
	return Runtime{c: c}
}

// Queues reads the runtime's message queues: async ListQueues/{id}. A
// runtime with no queues settles to an empty result.
func (r Runtime) Queues(ctx context.Context, runtimeID string, opts *AsyncOptions) ([]QueueRecord, error) {
	res, err := AsyncGet(ctx, r.c, "ListQueues", runtimeID, opts)
	if err != nil {
		return nil, err
	}

	wrappers, err := DecodeAsync[listQueuesResult](res)
	if err != nil {
		return nil, err
	}

	var out []QueueRecord
	for _, w := range wrappers {
		out = append(out, w.QueueRecord...)
	}

	return out, nil
}

// Listeners reads active listeners on a runtime: async
// ListenerStatus/query. The filter property is containerId — atomId is
// rejected.
func (r Runtime) Listeners(ctx context.Context, runtimeID string, opts *AsyncOptions) ([]ListenerStatus, error) {
	if runtimeID == "" {
		return nil, errEmptyID("runtime")
	}

	res, err := AsyncQuery(ctx, r.c, "ListenerStatus", mustFilter(query.Eq("containerId", runtimeID)), opts)
	if err != nil {
		return nil, err
	}

	return DecodeAsync[ListenerStatus](res)
}
