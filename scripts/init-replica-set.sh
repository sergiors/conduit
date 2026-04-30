#!/bin/bash
# Initialize MongoDB replica set for change streams

set -e

echo "=== MongoDB Replica Set Initialization ==="
echo "Waiting for MongoDB to be ready..."
sleep 3

echo "Initializing replica set..."
mongosh --eval '
  try {
    const status = rs.status();
    print("Replica set already initialized: " + status.ok);
  } catch (e) {
    if (e.codeName === "NotYetInitialized") {
      print("Initiating replica set...");
      rs.initiate({
        _id: "rs0",
        members: [
          { _id: 0, host: "mongo:27017" }
        ]
      });
      print("Replica set initiated successfully");
    } else if (e.codeName === "AlreadyInitialized") {
      print("Replica set already initialized");
    } else {
      print("Error: " + e);
      throw e;
    }
  }
'

echo "Waiting for replica set to be ready..."
sleep 5

echo "=== Initialization Complete ==="
