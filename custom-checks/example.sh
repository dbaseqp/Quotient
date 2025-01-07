#!/bin/sh

# Usage: ./example.sh <address> <team_id> <username> <password>

ADDRESS="$1"
TEAM_ID="$2"
USERNAME="$3"
PASSWORD="$4"

echo "$ADDRESS" "$TEAM_ID" "$USERNAME" "$PASSWORD"

ping -c2 -W2 -w2 "$ADDRESS" && return 0

return 1
