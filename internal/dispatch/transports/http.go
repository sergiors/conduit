package transports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

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

	return &HTTPTransport{
		HTTPSpec: spec,
		client:   &http.Client{Timeout: 30 * time.Second},
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
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
