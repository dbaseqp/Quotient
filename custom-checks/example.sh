#!/bin/sh

# Usage: ./example.sh <round> <address> <team_id> <username> <password>

ROUND="$1"
ADDRESS="$2"
TEAM_ID="$3"
USERNAME="$4"
PASSWORD="$5"

echo "$ROUND" "$ADDRESS" "$TEAM_ID" "$USERNAME" "$PASSWORD"

ping -c2 -W2 -w2 "$ADDRESS" && return 0

return 1
