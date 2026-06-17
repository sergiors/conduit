#!/bin/bash
# Example: Create a collection with streaming enabled

API_URL="${API_URL:-http://localhost:8080}"

echo "Creating collection 'users' with streaming enabled..."

curl -X POST "$API_URL/api/collections" \
  -H "Content-Type: application/json" \
  -d '{
    "collection_name": "users",
    "stream_enabled": true,
    "old_image": true,
    "ttl_attribute": "expiresAt",
    "deletion_protection": true,
    "sinks": [
      {
        "type": "http",
        "endpoint": "http://localhost:3000/events",
        "event_types": ["INSERT", "MODIFY", "REMOVE"]
      }
    ]
  }' | jq .

echo ""
echo "Listing all collections..."
curl -s "$API_URL/api/collections" | jq .
