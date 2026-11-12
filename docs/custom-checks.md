# Writing Custom Score Checks

This guide explains how to create and run your own service checks. Custom checks are shell scripts or binaries executed by the runner container. They return an exit status of `0` when the service is considered up and non-zero when it is down.

## Command Format

Custom checks are defined under a `[[box.custom]]` section in `event.conf`. The `command` field contains the command to run. The runner replaces placeholders before executing the command:

| Placeholder | Description |
|-------------|-------------|
| `ROUND` | Current round number. During preflight rounds (while the competition is not started and no scored round has been stored, so also after `GET /api/engine/reset` while stopped) this is the preflight round number, which also starts at 1, so a script must not assume it is unique across preflight and scored rounds |
| `TARGET` | Hostname or IP address (with team identifier substituted) |
| `TEAMIDENTIFIER` | The team's unique identifier (e.g., "01") |
| `USERNAME` | A username from the configured credlists |
| `PASSWORD` | The corresponding password |

Example configuration:

```toml
[[box.custom]]
display = "myservice"
command = "/app/checks/example.sh ROUND TARGET TEAMIDENTIFIER USERNAME PASSWORD"
credlists = ["web01.credlist", "users.credlist"]
regex = "SUCCESS"
```

### Configuration Options

| Option | Description |
|--------|-------------|
| `command` | The command to execute (required) |
| `credlists` | Array of credlist names for USERNAME/PASSWORD substitution |
| `regex` | Regular expression to match against output for success (optional) |
| `display` | Check name suffix (default: "custom") |
| `points` | Points for this check (inherits from global if not set) |
| `timeout` | Check timeout in seconds (inherits from global if not set) |

The runner mounts the contents of `./custom-checks` at `/app/checks/` inside the container. Place your script in that directory and ensure it is executable (`chmod +x`).

## Writing the Script

A simple shell script might look like:

```sh
#!/bin/sh
# Usage: ./example.sh <round> <address> <team_id> <username> <password>
ROUND="$1"
ADDRESS="$2"
TEAM_ID="$3"
USERNAME="$4"
PASSWORD="$5"

# Implement your logic here
if ping -c2 -W2 -w2 "$ADDRESS" > /dev/null 2>&1; then
    echo "SUCCESS: Host $ADDRESS is reachable"
    exit 0
fi

echo "FAILED: Host $ADDRESS is not reachable"
exit 1
```

### Output Matching

If the `regex` option is set, the check only passes if:
1. The script exits with code 0, AND
2. The output matches the regular expression

Without `regex`, only the exit code matters.

Your script can print output to stdout or stderr for debugging. This output is captured and visible from the admin interface, and to blue teams when `ShowDebugToBlueTeam` is enabled. What they see starts with the command as run, credentials included.

### Failure Messages

When a check fails, its output is often all an admin (or a team, with `ShowDebugToBlueTeam` on) has to go on. Write each failure so the reader knows where to look:

- **One message per step.** A check that logs in, loads a page and reads data has three ways to fail. Give each its own message instead of one "login failed" for all of them.
- **Name the component and where it runs.** If the check depends on a service on another host (an identity provider, a database, a DNS server), say which one failed and its address. A team that sees only the app's name will troubleshoot or reset the app's box, even when the fault is elsewhere.
- **Say what was expected and what came back.** "returned HTTP 503" or "page missing 'Welcome'" beats "unexpected response".
- **Pass on the service's own error text** when it gives one, such as the message on a login page.
- **Keep one condition per message.** A message like "returned HTTP 200 or database error" reads as if a 200 were the failure.

Some examples, using the `FAILED:` prefix of the scripts on this page:

```
FAILED: wiki didn't redirect the login to the identity provider (HTTP 200)
FAILED: identity provider at 10.0.5.20 refused the login for jdoe: Invalid username or password.
FAILED: identity provider accepted jdoe but the wiki didn't show them logged in (HTTP 302)
FAILED: store can't connect to its database (MariaDB on the database host)
```

## Environment and Dependencies

Runner containers are built from `Dockerfile.runner`.

### Python Dependencies

List Python packages in `custom-checks/requirements.txt`:

```
requests
paramiko
```

These are automatically installed when the runner container builds.

### System Packages

For additional Alpine packages, edit `Dockerfile.runner`:

```dockerfile
RUN apk add --no-cache curl netcat-openbsd nmap
```

Then rebuild the runner:

```bash
docker-compose build runner
docker-compose up -d runner
```

## Recommendations

- **Keep checks fast.** The default timeout is half the round delay (e.g., 30s for a 60s delay). Checks that exceed the timeout are marked as failed.
- **Avoid infinite loops.** The runner forcibly kills the command when the timeout is reached.
- **Use meaningful output.** Print context on both success and failure (see [Failure Messages](#failure-messages)).
- **Handle escaped credentials.** USERNAME and PASSWORD are shell-escaped by the runner. If your script passes them to other tools, ensure those tools handle quoted strings correctly.
- **Test locally first.** Run your script manually before deploying to catch errors early.

## Examples

### HTTP Check with curl

```sh
#!/bin/sh
ADDRESS="$2"
USERNAME="$4"
PASSWORD="$5"

response=$(curl -s -o /dev/null -w "%{http_code}" -u "$USERNAME:$PASSWORD" "http://$ADDRESS/api/health")
if [ "$response" = "200" ]; then
    echo "SUCCESS: API returned 200"
    exit 0
fi

echo "FAILED: API returned $response"
exit 1
```

### Database Check

```sh
#!/bin/sh
ADDRESS="$2"
USERNAME="$4"
PASSWORD="$5"

if mysql -h "$ADDRESS" -u "$USERNAME" -p"$PASSWORD" -e "SELECT 1" > /dev/null 2>&1; then
    echo "SUCCESS: Database connection successful"
    exit 0
fi

echo "FAILED: Database connection failed"
exit 1
```

For more examples, see `custom-checks/example.sh` in this repository.
