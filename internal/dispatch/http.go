package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
)

// HTTPDestination sends records to an HTTP endpoint via POST.
type HTTPDestination struct {
	name           string
	client         *http.Client
	endpoint       string
	eventTypes     map[string]bool
	bearerToken    string
	filterCriteria collections.FilterCriteria
}

// NewHTTPDestination creates an HTTP destination.
func NewHTTPDestination(endpoint string, bearerToken string, eventTypes []string, filterCriteria collections.FilterCriteria) (*HTTPDestination, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}

	eventTypeFilter := make(map[string]bool)
	for _, et := range eventTypes {
		eventTypeFilter[et] = true
	}

	if len(eventTypeFilter) == 0 {
		eventTypeFilter["INSERT"] = true
		eventTypeFilter["MODIFY"] = true
		eventTypeFilter["REMOVE"] = true
	}

	return &HTTPDestination{
		name:           "http:" + endpoint,
		client:         &http.Client{Timeout: 30 * time.Second},
		endpoint:       endpoint,
		bearerToken:    bearerToken,
		eventTypes:     eventTypeFilter,
		filterCriteria: filterCriteria,
	}, nil
}

func (h *HTTPDestination) Name() string {
	return h.name
}

func (h *HTTPDestination) Send(ctx context.Context, record streams.StreamRecord) error {
	if !h.eventTypes[string(record.RecordType)] {
		log.Printf("Skipping event type %s for HTTP destination", record.RecordType)
		return nil
	}

	if !collections.MatchImage(record.NewImage, h.filterCriteria.NewImage) || !collections.MatchImage(record.OldImage, h.filterCriteria.OldImage) {
		log.Printf("Event filtered out by image criteria for %s", h.name)
		return nil
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if h.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.bearerToken)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	log.Printf("Sent event to HTTP endpoint %s (status: %d)", h.endpoint, resp.StatusCode)
	return nil
}

func (h *HTTPDestination) Close() error {
	return nil
}
