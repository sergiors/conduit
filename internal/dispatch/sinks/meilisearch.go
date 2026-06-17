package sinks

import (
	"context"
	"fmt"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// init registers the Meilisearch sink builder automatically when this package is imported.
// This is triggered by the blank import in cmd/worker/main.go:
//
//	import _ "github.com/sergiors/conduit/internal/dispatch/sinks"

// MeilisearchSink sends records to Meilisearch for full-text indexing.
type MeilisearchSink struct {
	name      string
	host      string
	apiKey    string
	indexName string
	// TODO: Add Meilisearch client when integration is configured
}

// NewMeilisearchSink creates a Meilisearch sink.
func NewMeilisearchSink(host, apiKey, indexName string) (*MeilisearchSink, error) {
	if host == "" {
		return nil, fmt.Errorf("host is required for Meilisearch sink")
	}
	if indexName == "" {
		return nil, fmt.Errorf("index_name is required for Meilisearch sink")
	}

	return &MeilisearchSink{
		name:      "meilisearch:" + host + "/" + indexName,
		host:      host,
		apiKey:    apiKey,
		indexName: indexName,
	}, nil
}

func (m *MeilisearchSink) Name() string {
	return m.name
}

func (m *MeilisearchSink) Send(ctx context.Context, record streams.StreamRecord) error {
	// TODO: Use Meilisearch client
	// INSERT  -> addDocument
	// MODIFY  -> updateDocument
	// REMOVE  -> deleteDocument

	log.Printf("Would send to Meilisearch %s (index: %s): %+v", m.host, m.indexName, record)
	return nil
}

func (m *MeilisearchSink) Close() error {
	return nil
}

func init() {
	dispatch.RegisterSink("meilisearch", func(ctx context.Context, collectionName string, sink collections.SinkConfig) dispatch.Sink {
		host := sink.Endpoint
		if host == "" {
			log.Printf("Meilisearch sink for %s missing required 'endpoint' (host)", collectionName)
			return nil
		}
		indexName := sink.IndexName
		if indexName == "" {
			indexName = collectionName
		}
		meiliSink, err := NewMeilisearchSink(host, sink.BearerToken, indexName)
		if err != nil {
			log.Printf("Failed to create Meilisearch sink for %s: %v", collectionName, err)
			return nil
		}
		return meiliSink
	})
}
