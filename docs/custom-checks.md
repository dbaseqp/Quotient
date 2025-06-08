# Writing Custom Score Checks

This guide explains how to create and run your own service checks. Custom checks are shell scripts or binaries executed by the runner container. They return an exit status of `0` when the service is considered up and non–zero when it is down.

## Command Format

Custom checks are defined under a `[[box.custom]]` section in `event.conf`. The `command` field contains the command to run. The runner replaces placeholders before executing the command:

- `ROUND` – the current round number
- `TARGET` – hostname or IP address for the service
- `TEAMIDENTIFIER` – the team's unique identifier
- `USERNAME` – a username pulled from any configured credlists
- `PASSWORD` – the corresponding password

Example configuration:

```toml
[[box.custom]]
command = "/app/checks/example.sh ROUND TARGET TEAMIDENTIFIER USERNAME PASSWORD"
credlists = ["web01.credlist", "users.credlist"]
regex = "example [Tt]ext"
```

The runner mounts the contents of `./custom-checks` at `/app/checks/` inside the container. Place your script in that directory and ensure it is executable.

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
ping -c2 -W2 -w2 "$ADDRESS" && exit 0
exit 1
```

Your script can print output to stdout or stderr to help with debugging. This output is captured and visible from the admin interface when a check fails.

## Environment and Dependencies

Runner containers are built from `Dockerfile.runner`. Python dependencies can be listed in `custom-checks/requirements.txt`. Additional packages may be installed by editing `Dockerfile.runner` and rebuilding the runner with `docker-compose build runner`.

## Recommendations

- Keep the check fast. Rounds may have short timeouts (default is 15 s).
- Avoid infinite loops. The runner will forcibly stop the command when the timeout is reached.
- Log helpful details on failure to ease troubleshooting.

For more advanced examples, see `custom-checks/example.sh` in this repository.
