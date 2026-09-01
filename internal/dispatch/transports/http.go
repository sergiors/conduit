package transports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// maxDrainBytes is the bounded budget of response-body bytes drained on a
// successful delivery. Reading a small prefix of the body lets the underlying
// HTTP keep-alive connection be reused by the client for the next request. This
// is pure hygiene: the body is never inspected, so the budget is intentionally
// tiny and a body that exceeds it (or errors mid-read) is not a delivery
// failure — draining is best-effort.
const maxDrainBytes = 4 << 10 // 4 KiB

// HTTPSpec holds the type-specific configuration for an HTTP transport.
type HTTPSpec struct {
	Endpoint    string `bson:"endpoint" json:"endpoint"`
	BearerToken string `bson:"bearerToken,omitempty" json:"bearerToken,omitempty"`
}

// HTTPTransport delivers stream records to an HTTP endpoint via POST.
type HTTPTransport struct {
	HTTPSpec

	client *http.Client
}

// NewHTTP builds an HTTP transport from its spec.
func NewHTTP(ctx context.Context, spec HTTPSpec) dispatch.Transport {
	if spec.Endpoint == "" {
		log.Printf("HTTP transport requires an endpoint")
		return nil
	}

	// Redirects are rejected, never followed. The default client policy follows
	// up to 10 redirects, silently handing a final 3xx/2xx back to the caller —
	// that can silently retarget delivery away from the configured endpoint or,
	// worse, mask a failed delivery as success. Returning a hard error here makes
	// any 3xx flow into the non-2xx branch of Send, so a redirect is always
	// surfaced as a delivery failure and fed to the retry pipeline, never mistaken
	// for durable success.
	return &HTTPTransport{
		HTTPSpec: spec,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return fmt.Errorf("redirect to %s rejected: events must be delivered only to the configured endpoint", req.URL)
			},
		},
	}
}

func (t *HTTPTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.BearerToken)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// Any non-2xx (including a 3xx that reached us via a rejected redirect) is a
	// delivery failure. Send returning nil is treated by the dispatcher as
	// "durably delivered", so we must never return nil for anything other than a
	// confirmed 2xx — doing otherwise would break at-least-once semantics.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Best-effort drain: reading a bounded prefix lets the keep-alive connection
	// be reused. The budget-limit case returns nil from CopyN and a body fully
	// consumed returns io.EOF — neither indicates a delivery problem, so ignore
	// all drain errors. The body is never read into memory.
	if _, err := io.CopyN(io.Discard, resp.Body, maxDrainBytes); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("HTTP transport: drain response body: %v", err)
	}
	return nil
}

func (t *HTTPTransport) Close() error { return nil }

func init() {
	dispatch.RegisterTransport(collections.SinkTypeHTTP, func(ctx context.Context, collectionName string, t collections.Type, rawSpec map[string]interface{}) dispatch.Transport {
		var spec HTTPSpec
		if err := decodeSpec(rawSpec, &spec); err != nil {
			log.Printf("Failed to decode HTTP transport spec for %s: %v", collectionName, err)
			return nil
		}

		return NewHTTP(ctx, spec)
	})
}
