# Quotient

Quotient is a cybersecurity competition scoring platform designed for CCDC-style events. It automatically scores defensive service checks while providing infrastructure for teams to submit inject solutions and make password change requests (PCRs).

Used by [WRCCDC](https://wrccdc.org) (Western Regional CCDC) and [PRCCDC](http://prccdc.com) (Pacific Rim CCDC).

## Prerequisites

Ensure you have the following installed on your system:
- Docker
- Docker Compose

## Quick Start

```bash
git clone --recurse-submodules https://github.com/dbaseqp/Quotient
cd Quotient
# Edit .env with your database and Redis passwords
cp config/event.conf.example config/event.conf
# Edit config/event.conf with your competition settings
docker-compose up --build --detach
```

## Architecture

The system is designed as a group of Docker components using Docker Compose:

| Component | Description |
|-----------|-------------|
| **Server** | Scoring engine, web frontend/API, configuration parser, and check coordinator |
| **Database** | PostgreSQL database for persisting checks, rounds, scores, and submissions |
| **Redis** | Message queue passing tasks between the server and runners |
| **Runner** | Alpine containers (5 replicas by default) that execute service checks. Customize via `Dockerfile.runner` for additional packages |
| **Divisor** | Optional IP rotation container - assigns unique source IPs from a subnet pool to runners, preventing target systems from blocking based on IP. See [Divisor](https://github.com/dbaseqp/Divisor) |

## Environment Variables

Create a `.env` file with the following required variables:

```bash
POSTGRES_USER=engineuser
POSTGRES_PASSWORD=<your-db-password>
POSTGRES_DB=engine
POSTGRES_HOST=quotient_database
REDIS_PASSWORD=<your-redis-password>
REDIS_HOST=quotient_redis
```

Optional variables:
- `LDAP_BIND_PASSWORD` - LDAP bind password (alternative to config file)

## Troubleshooting

- Check logs: `docker-compose logs` or `docker-compose logs <service>`
- Verify `.env` file has all required variables set
- Ensure `config/event.conf` exists and is valid TOML
- For Redis memory warnings: `sudo sysctl vm.overcommit_memory=1` (or add `vm.overcommit_memory = 1` to `/etc/sysctl.conf`)
- Rebuild runners after modifying `Dockerfile.runner`: `docker-compose build runner && docker-compose up -d runner`

## Web Setup

After starting the engine:

1. Log in as admin
2. Navigate to the Admin UI
3. Set the **Identifier** for each team (the unique part of target addresses, e.g., `01` for team 1)
4. Mark teams as **Active** to begin scoring

## Configuration

1. How to Create Configuration File
2. Configuration Sections

### How to Create Configuration File

#### Basics

The `config` directory contains the configurations for the scoring engine. The primarily uses a TOML file to configure the engine. The configuration file is broken up into sections.

```
/quotient
└── config
    ├── certs/
    ├── credlists/
    ├── injects/
    ├── scoredfiles/
    ├── COOKIEKEY
    └── event.conf
```

#### Configuration File

The configuration file is a TOML file that is used to configure the scoring engine. The configuration file is located in the `./config` directory and is named `event.conf`. `COOKIEKEY` is auto-generated and is used to encrypt the session cookie. The `certs` directory is used to store any SSL certificates that are used by the scoring engine such as potential LDAPS certificates for the Docker container (since it won't inherit from the system). The `injects` directory is used to store any files that are uploaded for inject definitions (note: inject submissions will go in `/submissions`). The `scoredfiles` directory is used to store any files that are uploaded for scoring purposes (like SSH private keys). 

The configuration file is broken up into sections. Only the `RequiredSettings` section is required. The other sections are optional and can be omitted if not needed.

#### Credential Lists

Cred lists need to be CSVs specified in the `./config/credlists` directory with a `.credlist` extension. When password change requests (PCRs) get processed, credlists will only be mutated by changing the password column of an existing user in the defined list. This means submitting a PCR with a user that does not exist will ignore that specific entry. Below is an example. See the below configuration examples to specify credlists for checks. You will have to map the files to a credlist name in a top-level config section for each credlist.

```
# example contents of a .credlist file
joe,s3cret
robby,mypass
johndoe,helloworld
```

```
# Example top-level credlist config
[CredlistSettings]
  [[CredlistSettings.Credlist]]
    CredlistName = "DomainUsersWindows"
    CredlistPath = "domain_users_windows.credlist"
    CredlistExplainText = "username,password"
  [[CredlistSettings.Credlist]]
    CredlistName = "DomainUsersLinux"
    CredlistPath = "domain_users_linux.credlist"
    CredlistExplainText = "username,password"
  [[CredlistSettings.Credlist]]
    CredlistName = "SQLUsers"
    CredlistPath = "sql_users.credlist"
    CredlistExplainText = "username,password"
```

They should be specified for each check that requires a credlist. The `credlists` field expects an array of strings of the exact file name of credlist to be used.

```
[[box]]
name = "example"
ip = "10.100.1_.2"

    [[box.ssh]]
    credlists = ["web01.credlist",]

    [[box.custom]]
    command = "/app/checks/example.sh ROUND TARGET TEAMIDENTIFIER USERNAME PASSWORD"
    credlists = ["web01.credlist","users.credlist"]
    regex = "example [Tt]ext"

```

### Configuration Sections

#### Required Settings

```toml
[RequiredSettings]
EventName = "Name of my Competition"
EventType = "rvb"  # Use "rvb" for Red vs Blue (CCDC-style)
DBConnectURL = "postgres://engineuser:password@quotient_database:5432/engine"
BindAddress = "0.0.0.0"
```

- `EventType`: Use `rvb` for Red vs Blue competitions (CCDC-style). The `koth` option exists but is not fully implemented.
- `DBConnectURL`: Can be omitted if using environment variables (`POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_HOST`, `POSTGRES_DB`).
- `BindAddress`: Use `0.0.0.0` when deploying in Docker.

#### LDAP Settings

```toml
[LdapSettings]
LdapConnectUrl = "ldap://ldap.yournet.org:389"
LdapBindDn = "CN=Scoring Engine Service Account,OU=service accounts,DC=yournet,DC=org"
LdapBindPassword = "password"  # Can also use LDAP_BIND_PASSWORD env var
LdapSearchBaseDn = "OU=Users,DC=yournet,DC=org"
LdapAdminGroupDn = "CN=YourAdmins,OU=Groups,DC=yournet,DC=org"
LdapTeamGroupDn = "CN=YourBlueTeam,OU=Groups,DC=yournet,DC=org"
LdapRedGroupDn = "CN=YourRedTeam,OU=Groups,DC=yournet,DC=org"
LdapInjectGroupDn = "CN=YourInjectManagers,OU=Groups,DC=yournet,DC=org"
```

If using LDAPS for the Docker deployment, add the certificate to `./config/certs` so it gets added to the container's certificate store.

#### OIDC Settings

```toml
[OIDCSettings]
OIDCEnabled = true
OIDCIssuerURL = "https://your-idp.example.com"
OIDCClientID = "quotient"
OIDCClientSecret = "your-client-secret"
OIDCRedirectURL = "https://quotient.example.com/auth/oidc/callback"

# Optional settings with defaults shown
OIDCScopes = ["openid", "profile", "email", "groups", "offline_access"]
OIDCGroupClaim = "groups"

# Group mappings
OIDCAdminGroups = ["quotient-admins"]
OIDCRedGroups = ["quotient-red"]
OIDCTeamGroups = ["quotient-teams"]
OIDCInjectGroups = ["quotient-inject-managers"]

# Token expiry in seconds (defaults shown)
OIDCRefreshTokenExpiryTeam = 86400     # 1 day
OIDCRefreshTokenExpiryAdmin = 2592000  # 30 days
OIDCRefreshTokenExpiryRed = 172800     # 2 days
OIDCRefreshTokenExpiryInject = 86400   # 1 day

# UI option
OIDCDisableLocalLogin = false
```

#### SSL Settings

```toml
[SslSettings]
HttpsCert = "/app/config/certs/server.crt"
HttpsKey = "/app/config/certs/server.key"
```

When SSL is configured, the default port changes to 443.

#### Misc Settings

```toml
[MiscSettings]
EasyPCR = true              # Simplified PCR interface
ShowDebugToBlueTeam = false # Show check debug info to teams
Port = 80                   # Server port (443 default with SSL)
LogoImage = "/static/assets/quotient.svg"
LogFile = ""                # Optional log file path
StartPaused = true          # Start with scoring paused

# Round timing
Delay = 60                  # Seconds between rounds (default: 60)
Jitter = 10                 # Random jitter in seconds (default: 5, must be < Delay)

# Scoring defaults (can be overridden per-check)
Points = 5                  # Points per successful check (default: 1)
Timeout = 30                # Check timeout in seconds (default: Delay/2)
SlaThreshold = 5            # Consecutive failures before SLA penalty (default: 5)
SlaPenalty = 50             # Points deducted for SLA violation (default: SlaThreshold * Points)
```

#### UI Settings

```toml
[UISettings]
EnablePublicGraphs = false                    # Allow unauthenticated graph viewing
DisableGraphsForBlueTeam = true               # Hide graphs from teams
AllowNonAnonymizedGraphsForBlueTeam = false   # Show team names on graphs
ShowAnnouncementsForRedTeam = true            # Red team sees announcements
```

#### Local Auth

Define local users for each role:

```toml
# Admin users - full engine control
[[admin]]
name = "admin"
pw = "password"

# Team users - blue team competitors
[[team]]
name = "Team01"
pw = "password"

[[team]]
name = "Team02"
pw = "password"

# Red team users - vulnerability tracking
[[red]]
name = "red01"
pw = "password"

# Inject managers - create injects and announcements
[[inject]]
name = "inject01"
pw = "password"
```

#### Environment Configuration

The IP address of the target box should be the IP the scoring engine will use. To templatize the IP address, use an underscore `_` in place of the part of the IP address that will be unique per team. This is the "Identifier" that you must specify through the Admin UI per team. The scoring engine will replace the underscore with the "Identifier" to create the unique target address for each team. If the target should use a DNS name, you can specify that by setting `ip` field to the DNS name (which will be used for all checks under the box) or using the `target` field at the individual check level. Template the DNS name with an underscore `_` in place of the part of the DNS name that will be unique per team.

It is recommended to use Quotient with [aweful-dns](https://github.com/wrccdc-org/aweful-dns) running on the same host.

```toml
[[box]]
name = "web01"
ip = "10.100.1_.2"
# ip = "team_.example.tld"
```

Each service check is defined beneath a box.

```toml
[[box]]
name = "web01"
ip = "10.100.1_.2"

    [[box.web]] # type of check you want
    display = "web01" # name of the check that gets appended to the box name
    target = "example.team_.tld" # e.g. this will resolve to example.team01.tld with aweful-dns
    port = 8080

        [[box.web.url]] # some checks have components you need to include
        path = "/index.html"

        [[box.web.url]]
        path = "/admin"
        status = 403
```

Custom checks can be added to the `./custom-checks/` directory. The script should exit with code 0 if the service is up and non-zero if down. Scripts are mounted at `/app/checks/` in the runner container.

For a detailed walkthrough of writing custom checks, see [docs/custom-checks.md](docs/custom-checks.md).

### Service Check Reference

All checks support these common properties:

| Property | Description | Default |
|----------|-------------|---------|
| `display` | Check name suffix (e.g., "web" creates "boxname-web") | Check type |
| `target` | Override the box IP/hostname for this check | Box IP |
| `port` | Service port | Type-specific |
| `points` | Points awarded for success | Global default |
| `timeout` | Check timeout in seconds | Global default |
| `slathreshold` | Failures before SLA penalty | Global default |
| `slapenalty` | Points deducted on SLA violation | Global default |
| `credlists` | Array of credlist names for authentication | None |
| `disabled` | Disable this check | false |
| `launchtime` | Start checking at this time | Immediate |
| `stoptime` | Stop checking at this time | Never |

#### Ping Check

Simple ICMP ping check.

```toml
[[box.ping]]
display = "ping"
# No additional options required
```

**Default port:** N/A

#### TCP Check

Verify TCP port connectivity.

```toml
[[box.tcp]]
display = "ssh-port"
port = 22
```

**Default port:** None (required)

#### DNS Check

Query DNS records and verify answers.

```toml
[[box.dns]]
display = "dns"
port = 53

    [[box.dns.record]]
    kind = "A"
    domain = "www.team_.example.com"
    answer = ["10.100.1_.10"]

    [[box.dns.record]]
    kind = "MX"
    domain = "team_.example.com"
    answer = ["mail.team_.example.com"]
```

**Default port:** 53
**Supported record types:** A, MX

#### Web Check

HTTP/HTTPS request with optional status code and content matching.

```toml
[[box.web]]
display = "web"
port = 8080
scheme = "https"  # "http" or "https"

    [[box.web.url]]
    path = "/index.html"
    status = 200     # Expected status code (optional)
    regex = "Welcome"  # Content regex (optional)

    [[box.web.url]]
    path = "/admin"
    status = 403
```

**Default port:** 80 (http) or 443 (https)
**Default scheme:** http

#### SSH Check

SSH login with optional command execution.

```toml
[[box.ssh]]
display = "ssh"
port = 22
credlists = ["linux_users.credlist"]
privkey = "id_rsa"        # Private key file in config/scoredfiles/ (optional)
badattempts = 3           # Failed login attempts before real attempt (optional)

    [[box.ssh.command]]
    command = "whoami"
    output = "root"        # Exact match (optional)
    useregex = false
    contains = false       # Check if output contains the string
```

**Default port:** 22

#### WinRM Check

Windows Remote Management check with optional PowerShell commands.

```toml
[[box.winrm]]
display = "winrm"
port = 5985
credlists = ["windows_users.credlist"]
encrypted = false         # Use HTTPS
badattempts = 2

    [[box.winrm.command]]
    command = "hostname"
    output = "DC01"
    useregex = false
```

**Default port:** 80 (unencrypted) or 443 (encrypted)

#### RDP Check

Remote Desktop Protocol connectivity check.

```toml
[[box.rdp]]
display = "rdp"
port = 3389
```

**Default port:** 3389

#### VNC Check

VNC connectivity check.

```toml
[[box.vnc]]
display = "vnc"
port = 5900
```

**Default port:** 5900

#### SMB Check

SMB share access with optional file verification.

```toml
[[box.smb]]
display = "smb"
port = 445
credlists = ["domain_users.credlist"]
domain = "MYDOMAIN"
share = "\\\\server\\share"

    [[box.smb.file]]
    name = "important.txt"
    regex = "secret data"    # Content regex (optional)
    hash = "abc123..."       # SHA256 hash (optional, mutually exclusive with regex)
```

**Default port:** 445
**Note:** If no credlists specified, uses guest authentication.

#### FTP Check

FTP login with optional file retrieval.

```toml
[[box.ftp]]
display = "ftp"
port = 21
credlists = ["ftp_users.credlist"]

    [[box.ftp.file]]
    name = "/pub/readme.txt"
    regex = "Welcome"        # Content regex (optional)
    hash = "abc123..."       # SHA256 hash (optional)
```

**Default port:** 21
**Note:** If no credlists specified, uses anonymous login.

#### SMTP Check

Send test email via SMTP.

```toml
[[box.smtp]]
display = "smtp"
port = 25
credlists = ["mail_users.credlist"]
domain = "@example.com"    # Appended to usernames
encrypted = false          # Use TLS
requireauth = false        # Force authentication even if not advertised
```

**Default port:** 25

#### IMAP Check

IMAP mailbox access check.

```toml
[[box.imap]]
display = "imap"
port = 143
credlists = ["mail_users.credlist"]
encrypted = false          # Use TLS
```

**Default port:** 143

#### POP3 Check

POP3 mailbox access check.

```toml
[[box.pop3]]
display = "pop3"
port = 110
credlists = ["mail_users.credlist"]
encrypted = false          # Use TLS
```

**Default port:** 110

#### LDAP Check

LDAP authentication check.

```toml
[[box.ldap]]
display = "ldap"
port = 636
credlists = ["domain_users.credlist"]
domain = "example.com"     # Domain for user@domain format
encrypted = true           # Use LDAPS
```

**Default port:** 636

#### SQL Check

MySQL database connectivity and query verification.

```toml
[[box.sql]]
display = "mysql"
port = 3306
credlists = ["db_users.credlist"]
kind = "mysql"             # Database type

    [[box.sql.query]]
    database = "production"
    command = "SELECT version()"
    output = "8.0"         # Expected output (optional)
    useregex = false
```

**Default port:** 3306
**Default kind:** mysql

#### Custom Check

Execute custom scripts or binaries.

```toml
[[box.custom]]
display = "mycheck"
command = "/app/checks/mycheck.sh ROUND TARGET TEAMIDENTIFIER USERNAME PASSWORD"
credlists = ["users.credlist"]
regex = "SUCCESS"          # Output regex for success (optional)
```

**Placeholders:** ROUND, TARGET, TEAMIDENTIFIER, USERNAME, PASSWORD

## Contributing

Please fork the repository and submit a pull request. For major changes, please open an issue first to discuss what you would like to change.

## License

This project is licensed under the GNU General Public License v3.0 - see the LICENSE file for details.

## Contact

For support or questions, please open a GitHub issue.

