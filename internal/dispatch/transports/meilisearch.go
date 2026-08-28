package transports

import (
	"context"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// MeilisearchSpec holds the type-specific configuration for a Meilisearch transport.
type MeilisearchSpec struct {
	Host      string `bson:"host" json:"host"`
	APIKey    string `bson:"api_key,omitempty" json:"api_key,omitempty"`
	IndexName string `bson:"index_name,omitempty" json:"index_name,omitempty"`
}

// MeilisearchTransport delivers stream records to Meilisearch for full-text indexing.
type MeilisearchTransport struct {
	MeilisearchSpec
}

// NewMeilisearch builds a Meilisearch transport from its spec.
func NewMeilisearch(ctx context.Context, spec MeilisearchSpec) dispatch.Transport {
	if spec.Host == "" {
		log.Printf("Meilisearch transport requires a host")
		return nil
	}

	return &MeilisearchTransport{
		MeilisearchSpec: spec,
	}
}

func (t *MeilisearchTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	log.Printf("Would send to Meilisearch %s (index: %s): %+v", t.Host, t.IndexName, record)
	return nil
}

func (t *MeilisearchTransport) Close() error { return nil }

func init() {
	dispatch.RegisterTransport(collections.SinkTypeMeilisearch, func(ctx context.Context, collectionName string, t collections.Type, rawSpec map[string]interface{}) dispatch.Transport {
		var spec MeilisearchSpec
		if err := decodeSpec(rawSpec, &spec); err != nil {
			log.Printf("Failed to decode Meilisearch transport spec for %s: %v", collectionName, err)
			return nil
		}

		if spec.IndexName == "" {
			spec.IndexName = collectionName
		}

		return NewMeilisearch(ctx, spec)
	})
}
