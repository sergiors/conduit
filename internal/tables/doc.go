// Package tables implements table configuration management for CDC.
//
// This package manages table configurations stored in MongoDB:
//   - Table: Represents a MongoDB collection with CDC settings
//   - DestinationConfig: Configures where events are sent
//   - Store: CRUD operations for table configurations
//
// Key Features:
//   - Per-table stream enable/disable
//   - Multiple destinations per table (HTTP, EventBridge)
//   - Event type filtering per destination
//   - Deletion protection flag
//   - TTL attribute configuration
//
// Table Schema:
//
//	{
//	  "table_name": "users",
//	  "stream_enabled": true,
//	  "old_image": true,
//	  "ttl_attribute": "expiresAt",
//	  "deletion_protection": true,
//	  "destinations": [...]
//	}
//
// Usage:
//
//	store := tables.NewStore(mongoClient, database)
//
//	// Create table
//	table := tables.Table{
//	    TableName:       "users",
//	    StreamEnabled:   true,
//	    OldImage:        true,
//	    Destinations:    []tables.DestinationConfig{...},
//	}
//	if err := store.Create(ctx, table); err != nil {
//	    log.Fatal(err)
//	}
//
//	// List enabled tables
//	tables, err := store.ListStreamEnabled(ctx)
package tables
