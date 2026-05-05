// Package mongo provides MongoDB client wrapper for CDC operations.
//
// This package manages MongoDB connections and change stream configuration:
//   - Client: Wraps MongoDB client with application-specific methods
//   - Replica set initialization: Required for change streams
//   - TTL index creation: For automatic document expiration
//   - Change stream configuration: With fullDocument lookup support
//
// Key Features:
//   - Automatic replica set initialization on startup
//   - TTL index creation with expireAfterSeconds=0
//   - Change stream validation with fullDocument=updateLookup
//   - URI parsing for host extraction (handles credentials, database, params)
//
// Usage:
//
//	client, err := mongo.NewClient(ctx, mongo.Config{
//	    URI:      "mongodb://localhost:27017",
//	    Database: "conduit",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close(ctx)
//
//	// Initialize replica set (required for change streams)
//	if err := client.InitializeReplicaSet(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Create TTL index
//	if err := client.CreateTTLIndex(ctx, "events", "expiresAt"); err != nil {
//	    log.Fatal(err)
//	}
package mongo
