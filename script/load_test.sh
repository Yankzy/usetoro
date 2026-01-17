#!/bin/bash

ENDPOINT="http://localhost:8082/hooks/n8n-agent-1"
COUNT=1000

echo "🚀 Starting Load Test: Sending $COUNT requests to $ENDPOINT..."

start_time=$(date +%s%N)

for i in $(seq 1 $COUNT)
do
   # Payload includes index to differentiate requests
   PAYLOAD="{\"message\": \"Load Test Message $i\", \"amount\": 100}"
   
   # Send request in background
   curl -s -X POST "$ENDPOINT" \
     -H "Content-Type: application/json" \
     -d "$PAYLOAD" > /dev/null &
done

# Wait for all background jobs to finish
wait

end_time=$(date +%s%N)
duration=$((($end_time - $start_time)/1000000))

echo "✅ Done! Sent $COUNT requests in ${duration}ms."
