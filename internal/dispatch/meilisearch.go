package dispatch

import (
	"context"
	"fmt"
	"log"

	"github.com/sergiors/relay/internal/streams"
)

// MeilisearchDestination sends records to Meilisearch for full-text indexing.
type MeilisearchDestination struct {
	name      string
	host      string
	apiKey    string
	indexName string
	// TODO: Add Meilisearch client when integration is configured
}

// NewMeilisearchDestination creates a Meilisearch destination.
func NewMeilisearchDestination(host, apiKey, indexName string) (*MeilisearchDestination, error) {
	if host == "" {
		return nil, fmt.Errorf("host is required for Meilisearch destination")
	}
	if indexName == "" {
		return nil, fmt.Errorf("index_name is required for Meilisearch destination")
	}

	return &MeilisearchDestination{
		name:      "meilisearch:" + host + "/" + indexName,
		host:      host,
		apiKey:    apiKey,
		indexName: indexName,
	}, nil
}

func (m *MeilisearchDestination) Name() string {
	return m.name
}

func (m *MeilisearchDestination) Send(ctx context.Context, record streams.StreamRecord) error {
	// TODO: Use Meilisearch client
	// INSERT  -> addDocument
	// MODIFY  -> updateDocument
	// REMOVE  -> deleteDocument

	log.Printf("Would send to Meilisearch %s (index: %s): %+v", m.host, m.indexName, record)
	return nil
}

func (m *MeilisearchDestination) Close() error {
	return nil
}
