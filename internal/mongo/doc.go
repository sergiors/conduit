// Package mongo provides MongoDB client wrapper for CDC operations.
//
// This package manages MongoDB connections and change stream configuration:
//   - Client: Wraps MongoDB client with application-specific methods
//   - Readiness wait: Waits for a writable PRIMARY before returning, so startup
//     is robust across container restarts where MongoDB is still electing a
//     PRIMARY
//   - Change stream configuration: With fullDocument lookup support
//
// Key Features:
//   - Waits for MongoDB readiness (writable PRIMARY) on startup
//   - Never creates or modifies replica sets; topology is managed externally
//   - Change stream validation with fullDocument=updateLookup
//
// Usage:
//
//	client, err := mongo.NewClient(ctx, mongo.Config{
//	    URI:      "mongodb://localhost:27017/?replicaSet=rs0",
//	    Database: "conduit",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close(ctx)
//
// NewClient waits until MongoDB is ready (a writable PRIMARY is reachable)
// before it returns.
package mongo
