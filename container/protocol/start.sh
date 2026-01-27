#!/bin/sh

# Start Protocol background process
/protocol &
PROTOCOL_PID=$!

# Start Python Sidecar
python3 server.py &
PYTHON_PID=$!

# Wait for any process to exit
wait -n $PROTOCOL_PID $PYTHON_PID

# Exit with status of process that exited first
exit $?
