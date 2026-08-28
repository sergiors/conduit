package streams

import (
	"encoding/json"
	"fmt"
	"time"
)

// RecordType represents the type of stream record
type RecordType string

const (
	InsertRecord RecordType = "INSERT"
	ModifyRecord RecordType = "MODIFY"
	RemoveRecord RecordType = "REMOVE"
)

// StreamRecord represents a DynamoDB-style stream record
type StreamRecord struct {
	TableName  string      `json:"tableName"`
	RecordType RecordType  `json:"recordType"`
	NewImage   interface{} `json:"newImage,omitempty"`
	OldImage   interface{} `json:"oldImage,omitempty"`
	Timestamp  time.Time   `json:"timestamp"`

	// EventID is a stable identifier for the underlying MongoDB change event,
	// derived exclusively from change-stream data (resume token, with
	// clusterTime and documentKey as fallback). It is deterministic across
	// process restarts and is used as the idempotency key for delivery.
	EventID string `json:"eventId,omitempty"`
}

// ParseStreamRecord parses raw JSON data into a StreamRecord
// This is needed when deserializing from Redis where types are lost
func ParseStreamRecord(data []byte) (*StreamRecord, error) {
	// First parse into a generic map to handle the JSON
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal stream record: %w", err)
	}

	record := &StreamRecord{}

	// Parse table name
	if tn, ok := raw["tableName"].(string); ok {
		record.TableName = tn
	}

	// Parse record type
	if rt, ok := raw["recordType"].(string); ok {
		record.RecordType = RecordType(rt)
	}

	// Parse timestamp
	if ts, ok := raw["timestamp"].(string); ok {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			record.Timestamp = t
		}
	}

	// Parse new image (keep as interface{})
	if ni, ok := raw["newImage"]; ok && ni != nil {
		record.NewImage = ni
	}

	// Parse old image (keep as interface{})
	if oi, ok := raw["oldImage"]; ok && oi != nil {
		record.OldImage = oi
	}

	return record, nil
}
