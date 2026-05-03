#!/bin/bash
# Example: Create a table with streaming enabled

API_URL="${API_URL:-http://localhost:8080}"

echo "Creating table 'users' with streaming enabled..."

curl -X POST "$API_URL/api/tables" \
  -H "Content-Type: application/json" \
  -d '{
    "table_name": "users",
    "stream_enabled": true,
    "old_image": true,
    "ttl_attribute": "expiresAt",
    "deletion_protection": true,
    "destinations": [
      {
        "type": "http",
        "endpoint": "http://localhost:3000/events",
        "event_types": ["INSERT", "MODIFY", "REMOVE"]
      }
    ]
  }' | jq .

echo ""
echo "Listing all tables..."
curl -s "$API_URL/api/tables" | jq .
