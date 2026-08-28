// Package streams implements MongoDB change stream record handling.
//
// This package defines the core data structures for CDC events:
//   - StreamRecord: Represents a change stream event with full document data
//   - RecordType: Enum for INSERT, MODIFY, REMOVE operations
//   - Operation mapping: Converts MongoDB change stream ops to DynamoDB-style types
//
// Key Features:
//   - DynamoDB-aligned naming: newImage, oldImage, tableName, recordType
//   - EventID: deterministic idempotency key derived from change-stream data
//     (resume token, with clusterTime + documentKey as fallback) - stable
//     across process restarts; never derived from application time
//   - Full document support: Optional oldImage via fullDocumentBeforeChange
//
// Record Types:
//
//	InsertRecord  - New document inserted (newImage only)
//	ModifyRecord  - Document updated (newImage + optional oldImage)
//	RemoveRecord  - Document deleted (newImage contains _id, optional oldImage)
//
// Usage:
//
//	record := streams.StreamRecord{
//	    TableName:  "users",
//	    RecordType: streams.InsertRecord,
//	    NewImage:   bson.M{"_id": "123", "name": "John"},
//	    OldImage:   nil,
//	    Timestamp:  time.Now(),
//	    EventID:    "users:826A91ADB6000000022B04...",
//	}
package streams
