#!/bin/bash
# Example: Monitor retry and DLQ queues

REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"

echo "=== CDC Queue Monitor ==="
echo ""

# Get all retry queues
echo "Retry Queues:"
redis-cli -h ${REDIS_ADDR%:*} -p ${REDIS_ADDR#*:} KEYS "cdc:retry:*" | while read key; do
  count=$(redis-cli -h ${REDIS_ADDR%:*} -p ${REDIS_ADDR#*:} ZCARD "$key")
  echo "  $key: $count events"
done

echo ""
echo "Dead Letter Queues:"
redis-cli -h ${REDIS_ADDR%:*} -p ${REDIS_ADDR#*:} KEYS "cdc:dlq:*" | while read key; do
  count=$(redis-cli -h ${REDIS_ADDR%:*} -p ${REDIS_ADDR#*:} LLEN "$key")
  echo "  $key: $count events"
done

echo ""
echo "Resume Tokens:"
redis-cli -h ${REDIS_ADDR%:*} -p ${REDIS_ADDR#*:} KEYS "cdc:resume:*" | while read key; do
  echo "  $key: $(redis-cli -h ${REDIS_ADDR%:*} -p ${REDIS_ADDR#*:} GET "$key" | head -c 50)..."
done
