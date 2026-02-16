#!/bin/sh

# Usage: ./example.sh <round> <address> <team_id> <username> <password>
# Exit 0 for success, non-zero for failure

ROUND="$1"
ADDRESS="$2"
TEAM_ID="$3"
USERNAME="$4"
PASSWORD="$5"

# Log inputs for debugging (visible in admin interface)
echo "Round: $ROUND, Target: $ADDRESS, Team: $TEAM_ID"

# Perform the check
if ping -c2 -W2 -w2 "$ADDRESS" > /dev/null 2>&1; then
    echo "SUCCESS: Host $ADDRESS is reachable"
    exit 0
fi

echo "FAILED: Host $ADDRESS is not reachable"
exit 1
