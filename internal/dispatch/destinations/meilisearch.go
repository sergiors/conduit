package destinations

import (
	"context"
	"fmt"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// init registers the Meilisearch destination builder automatically when this package is imported.
// This is triggered by the blank import in cmd/worker/main.go:
//
//	import _ "github.com/sergiors/conduit/internal/dispatch/destinations"

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

func init() {
	dispatch.RegisterDestination("meilisearch", func(ctx context.Context, collectionName string, dest collections.DestinationConfig) dispatch.Destination {
		host := dest.Endpoint
		if host == "" {
			log.Printf("Meilisearch destination for %s missing required 'endpoint' (host)", collectionName)
			return nil
		}
		indexName := dest.IndexName
		if indexName == "" {
			indexName = collectionName
		}
		meiliDest, err := NewMeilisearchDestination(host, dest.BearerToken, indexName)
		if err != nil {
			log.Printf("Failed to create Meilisearch destination for %s: %v", collectionName, err)
			return nil
		}
		return meiliDest
	})
}
