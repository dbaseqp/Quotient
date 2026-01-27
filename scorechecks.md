# Score Checks Configuration Guide

This document provides detailed configuration instructions for all service checks available in Quotient.

## Table of Contents

- [Common Properties](#common-properties)
- [Check Types](#check-types)
  - [Custom](#custom)
  - [DNS](#dns)
  - [FTP](#ftp)
  - [IMAP](#imap)
  - [LDAP](#ldap)
  - [Ping](#ping)
  - [POP3](#pop3)
  - [RDP](#rdp)
  - [SMB](#smb)
  - [SMTP](#smtp)
  - [SQL](#sql)
  - [SSH](#ssh)
  - [TCP](#tcp)
  - [VNC](#vnc)
  - [Web](#web)
  - [WinRM](#winrm)

---

## Common Properties

All service checks inherit these common properties from the base `Service` type:

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Display` | string | No | Check type name (lowercase) | Display name shown in the UI |
| `CredLists` | string[] | No | - | List of credential list names to use for authentication |
| `Port` | int | No | Varies by check type | Port number to connect to |
| `Points` | int | No | Global default | Points awarded for successful check |
| `Timeout` | int | No | Global default | Timeout in seconds |
| `SlaPenalty` | int | No | Global default | Points deducted for SLA violation |
| `SlaThreshold` | int | No | Global default | Number of consecutive failures before SLA penalty |
| `LaunchTime` | datetime | No | - | Time when the check should start running |
| `StopTime` | datetime | No | - | Time when the check should stop running |
| `Disabled` | bool | No | `false` | Whether the check is disabled |
| `Target` | string | No | Box IP | Override target IP/hostname (supports `_` placeholder) |
| `Attempts` | int | No | `1` | Number of check attempts per round |

### IP Address Templating

The `_` character in IP addresses and hostnames is replaced with the team identifier. For example:
- `172.19.12_.22` becomes `172.19.1201.22` for team01
- `team_.example.com` becomes `team01.example.com` for team01

### Credential Lists

Credential lists are CSV files stored in `config/credlists/` with the format:
```
username,password
user1,pass1
user2,pass2
```

Reference them in checks using the `CredlistName` defined in `[CredlistSettings]`.

---

## Check Types

### Custom

Executes a custom shell command or script. Use this for services not covered by built-in checks.

**Default Port:** None (not applicable)

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Command` | string | **Yes** | - | Shell command to execute |
| `Regex` | string | No | - | Regex pattern that must match command output for success |

#### Placeholder Variables

The following placeholders are replaced in the `Command` string:

| Placeholder | Description |
|-------------|-------------|
| `ROUND` | Current round number |
| `TARGET` | Target IP address (with team identifier substituted) |
| `TEAMIDENTIFIER` | Team identifier (e.g., "01", "02") |
| `USERNAME` | Random username from credential list (shell-escaped) |
| `PASSWORD` | Corresponding password (shell-escaped) |

#### Success Criteria

- If `Regex` is specified: Command must exit 0 AND output must match regex
- If no `Regex`: Command must exit 0

#### Example

```toml
[[Box]]
  Name = "webserver"
  IP = "10.10.1_.5"

  [[Box.Custom]]
    Display = "api-health"
    CredLists = ["api-users"]
    Command = "curl -s -u USERNAME:PASSWORD http://TARGET:8080/health"
    Regex = "\"status\":\\s*\"ok\""
    Timeout = 10
```

#### Custom Check Scripts

Place scripts in `custom-checks/` directory. Example script:

```bash
#!/bin/sh
# Usage: ./check.sh <round> <target> <team_id> <username> <password>

ROUND="$1"
TARGET="$2"
TEAM_ID="$3"
USERNAME="$4"
PASSWORD="$5"

# Your check logic here
curl -s "http://$TARGET/api" | grep -q "success"
exit $?
```

---

### DNS

Queries a DNS server and validates the response.

**Default Port:** `53`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Record` | DnsRecord[] | **Yes** | - | List of DNS records to check |

#### DnsRecord Structure

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `Kind` | string | **Yes** | Record type: `"A"` or `"MX"` |
| `Domain` | string | **Yes** | Domain name to query (supports `_` placeholder) |
| `Answer` | string[] | **Yes** | Expected answers (supports `_` placeholder) |

#### Success Criteria

A random record is selected each round. The check passes if any expected answer matches the DNS response.

#### Example

```toml
[[Box]]
  Name = "dns-server"
  IP = "10.10.1_.2"

  [[Box.Dns]]
    Display = "dns"
    Port = 53

    [[Box.Dns.Record]]
      Kind = "A"
      Domain = "www.team_.local"
      Answer = ["10.10.1_.10"]

    [[Box.Dns.Record]]
      Kind = "A"
      Domain = "mail.team_.local"
      Answer = ["10.10.1_.11", "10.10.1_.12"]

    [[Box.Dns.Record]]
      Kind = "MX"
      Domain = "team_.local"
      Answer = ["mail.team_.local"]
```

---

### FTP

Connects to an FTP server, optionally authenticates, and optionally verifies file contents.

**Default Port:** `21`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `File` | FtpFile[] | No | - | Files to retrieve and verify |

#### FtpFile Structure

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `Name` | string | **Yes** | Path to the file on the FTP server |
| `Hash` | string | No | Expected SHA256 hash of file contents |
| `Regex` | string | No | Regex pattern that must match file contents |

**Note:** Cannot specify both `Hash` and `Regex` for the same file.

#### Success Criteria

1. Connection and authentication succeed
2. If `File` specified: Random file is retrieved successfully
3. If `Hash` specified: File hash must match
4. If `Regex` specified: File contents must match pattern

#### Example

```toml
[[Box]]
  Name = "ftp-server"
  IP = "10.10.1_.20"

  [[Box.Ftp]]
    Display = "ftp"
    CredLists = ["ftp-users"]

    [[Box.Ftp.File]]
      Name = "pub/readme.txt"
      Regex = "Welcome to"

    [[Box.Ftp.File]]
      Name = "pub/config.dat"
      Hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
```

#### Anonymous FTP

If no `CredLists` specified, anonymous login is attempted with `anonymous:anonymous`.

---

### IMAP

Connects to an IMAP mail server and lists mailboxes.

**Default Port:** `143`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Domain` | string | No | - | Domain suffix appended to username (e.g., `"@example.com"`) |
| `Encrypted` | bool | No | `false` | Use TLS/SSL connection |

#### Success Criteria

1. Connection succeeds
2. If credentials provided: Login succeeds and mailboxes can be listed

#### Example

```toml
[[Box]]
  Name = "mail-server"
  IP = "10.10.1_.30"

  [[Box.Imap]]
    Display = "imap"
    CredLists = ["mail-users"]
    Domain = "@team_.local"
    Port = 143

  [[Box.Imap]]
    Display = "imaps"
    CredLists = ["mail-users"]
    Domain = "@team_.local"
    Port = 993
    Encrypted = true
```

---

### LDAP

Authenticates against an LDAP/Active Directory server.

**Default Port:** `636`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Domain` | string | **Yes** | - | Domain for authentication (format: `domain.tld`) |
| `Encrypted` | bool | No | `false` | Use LDAPS (TLS) connection |

#### Authentication Format

Credentials are bound using: `username@domain`

#### Success Criteria

LDAP bind operation succeeds with provided credentials.

#### Example

```toml
[[Box]]
  Name = "domain-controller"
  IP = "10.10.1_.5"

  [[Box.Ldap]]
    Display = "ldap"
    CredLists = ["domain-users"]
    Domain = "team_.local"
    Port = 389

  [[Box.Ldap]]
    Display = "ldaps"
    CredLists = ["domain-users"]
    Domain = "team_.local"
    Port = 636
    Encrypted = true
```

---

### Ping

Sends ICMP ping requests to verify host availability.

**Default Port:** None (not applicable)

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Count` | int | No | `1` | Number of ping packets to send |
| `AllowPacketLoss` | bool | No | `false` | Allow partial packet loss |
| `Percent` | int | No | `0` | Maximum allowed packet loss percentage (when `AllowPacketLoss` is true) |

#### Success Criteria

- If `AllowPacketLoss` is false: All packets must be received
- If `AllowPacketLoss` is true: Packet loss must be less than `Percent`

#### Example

```toml
[[Box]]
  Name = "router"
  IP = "10.10.1_.1"

  [[Box.Ping]]
    Display = "ping"
    Count = 3

  [[Box.Ping]]
    Display = "ping-tolerant"
    Count = 5
    AllowPacketLoss = true
    Percent = 40
```

---

### POP3

Connects to a POP3 mail server and retrieves mailbox statistics.

**Default Port:** `110`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Domain` | string | No | - | Domain suffix appended to username |
| `Encrypted` | bool | No | `false` | Use TLS/SSL connection |

#### Success Criteria

1. Connection succeeds
2. If credentials provided: Login succeeds and STAT command returns successfully

#### Example

```toml
[[Box]]
  Name = "mail-server"
  IP = "10.10.1_.30"

  [[Box.Pop3]]
    Display = "pop3"
    CredLists = ["mail-users"]
    Domain = "@team_.local"
    Port = 110

  [[Box.Pop3]]
    Display = "pop3s"
    CredLists = ["mail-users"]
    Domain = "@team_.local"
    Port = 995
    Encrypted = true
```

---

### RDP

Verifies that an RDP service is accepting connections (TCP connectivity check only).

**Default Port:** `3389`

No additional properties.

#### Success Criteria

TCP connection to the RDP port succeeds.

#### Example

```toml
[[Box]]
  Name = "windows-server"
  IP = "10.10.1_.50"

  [[Box.Rdp]]
    Display = "rdp"
    Port = 3389
```

---

### SMB

Connects to an SMB file share and optionally verifies file contents.

**Default Port:** `445`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Domain` | string | No | - | Domain for NTLM authentication |
| `Share` | string | No | - | Share name to mount (required if `File` specified) |
| `File` | smbFile[] | No | - | Files to retrieve and verify |

#### smbFile Structure

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `Name` | string | **Yes** | Path to file within the share |
| `Hash` | string | No | Expected SHA256 hash of file contents |
| `Regex` | string | No | Regex pattern that must match file contents |

#### Success Criteria

1. SMB connection and authentication succeed
2. If `File` specified: Share mounts and file retrieval succeeds
3. If `Hash`/`Regex` specified: File contents must match

#### Example

```toml
[[Box]]
  Name = "file-server"
  IP = "10.10.1_.40"

  [[Box.Smb]]
    Display = "smb"
    CredLists = ["domain-users"]
    Share = "shared"

    [[Box.Smb.File]]
      Name = "documents/policy.txt"
      Regex = "Acceptable Use Policy"

    [[Box.Smb.File]]
      Name = "data/config.ini"
      Hash = "abc123..."
```

#### Guest Access

If no `CredLists` specified, guest login is attempted with `guest:` (empty password).

---

### SMTP

Connects to an SMTP server and sends a test email.

**Default Port:** `25`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Encrypted` | bool | No | `false` | Use TLS/SSL connection |
| `Domain` | string | No | - | Domain suffix appended to usernames |
| `RequireAuth` | bool | No | `false` | Require authentication even if server doesn't advertise AUTH |

#### Success Criteria

1. Connection succeeds
2. If credentials and AUTH: Authentication succeeds
3. Email is accepted by the server (MAIL FROM, RCPT TO, DATA)

#### Example

```toml
[[Box]]
  Name = "mail-server"
  IP = "10.10.1_.30"

  [[Box.Smtp]]
    Display = "smtp"
    CredLists = ["mail-users"]
    Domain = "@team_.local"
    Port = 25

  [[Box.Smtp]]
    Display = "smtps"
    CredLists = ["mail-users"]
    Domain = "@team_.local"
    Port = 465
    Encrypted = true
    RequireAuth = true
```

---

### SQL

Connects to a SQL database server and optionally executes queries.

**Default Port:** `3306`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Kind` | string | No | `"mysql"` | Database driver: `"mysql"` |
| `Query` | queryData[] | No | - | Queries to execute |

#### queryData Structure

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `Database` | string | No | Database name to connect to |
| `Command` | string | No | SQL query to execute |
| `Output` | string | No | Expected output (first column of first matching row) |
| `UseRegex` | bool | No | Treat `Output` as regex pattern |

#### Success Criteria

1. Database connection and authentication succeed
2. If `Query` specified: Random query executes successfully
3. If `Output` specified: First column of result matches (exact or regex)

#### Example

```toml
[[Box]]
  Name = "database-server"
  IP = "10.10.1_.60"

  [[Box.Sql]]
    Display = "mysql"
    CredLists = ["db-users"]
    Kind = "mysql"
    Port = 3306

    [[Box.Sql.Query]]
      Database = "webapp"
      Command = "SELECT COUNT(*) FROM users"
      Output = "5"

    [[Box.Sql.Query]]
      Database = "webapp"
      Command = "SELECT version()"
      Output = "^[0-9]+\\.[0-9]+"
      UseRegex = true
```

---

### SSH

Connects to an SSH server and optionally executes commands.

**Default Port:** `22`

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `PrivKey` | string | No | - | Private key filename (in `config/scoredfiles/`) |
| `BadAttempts` | int | No | `0` | Number of failed login attempts before real attempt |
| `Command` | commandData[] | No | - | Commands to execute |

**Note:** Cannot use both `PrivKey` and `BadAttempts`.

#### commandData Structure

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `Command` | string | **Yes** | Shell command to execute |
| `Output` | string | No | Expected output |
| `UseRegex` | bool | No | Treat `Output` as regex pattern |
| `Contains` | bool | No | Check if output contains `Output` string |

#### Success Criteria

1. SSH connection and authentication succeed
2. Shell session starts successfully
3. If `Command` specified: Random command executes
4. If `Contains`: Output must contain the string
5. If `UseRegex`: Output must match regex
6. Otherwise: stderr must be empty

#### Example

```toml
[[Box]]
  Name = "linux-server"
  IP = "10.10.1_.10"

  [[Box.Ssh]]
    Display = "ssh"
    CredLists = ["linux-users"]
    Port = 22

    [[Box.Ssh.Command]]
      Command = "whoami"
      # No output check - just verifies command runs without error

    [[Box.Ssh.Command]]
      Command = "cat /etc/hostname"
      Output = "server"
      Contains = true

    [[Box.Ssh.Command]]
      Command = "uptime"
      Output = "load average"
      Contains = true

  [[Box.Ssh]]
    Display = "ssh-key"
    PrivKey = "id_rsa"
    Port = 22
```

---

### TCP

Simple TCP connectivity check to verify a port is open.

**Default Port:** None (required)

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Port` | int | **Yes** | - | Port number to connect to |

#### Success Criteria

TCP connection succeeds within timeout.

#### Example

```toml
[[Box]]
  Name = "app-server"
  IP = "10.10.1_.70"

  [[Box.Tcp]]
    Display = "app-port"
    Port = 8080

  [[Box.Tcp]]
    Display = "metrics"
    Port = 9090
```

---

### VNC

Connects to a VNC server and authenticates.

**Default Port:** `5900`

No additional properties. Requires `CredLists` for password authentication.

#### Success Criteria

VNC connection and password authentication succeed.

#### Example

```toml
[[Box]]
  Name = "workstation"
  IP = "10.10.1_.80"

  [[Box.Vnc]]
    Display = "vnc"
    CredLists = ["vnc-passwords"]
    Port = 5900
```

**Note:** VNC typically uses password-only authentication. The credential list should have the password in the second column; the username column is logged but not used for authentication.

---

### Web

Makes HTTP/HTTPS requests and validates responses.

**Default Port:** `80` (HTTP) or `443` (HTTPS)

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Scheme` | string | No | `"http"` | URL scheme: `"http"` or `"https"` |
| `Url` | urlData[] | **Yes** | - | URLs to check |

#### urlData Structure

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `Path` | string | No | URL path (default: `"/"`) |
| `Status` | int | No | Expected HTTP status code |
| `Regex` | string | No | Regex pattern that must match response body |
| `Diff` | int | No | Maximum allowed difference percentage from `CompareFile` |
| `CompareFile` | string | No | File to compare response against (required if `Diff` set) |

#### Success Criteria

1. HTTP request succeeds
2. If `Status` specified: Response status code must match
3. If `Regex` specified: Response body must match pattern

#### Example

```toml
[[Box]]
  Name = "web-server"
  IP = "10.10.1_.100"

  [[Box.Web]]
    Display = "http"
    Scheme = "http"
    Port = 80

    [[Box.Web.Url]]
      Path = "/"
      Status = 200
      Regex = "<title>Welcome</title>"

    [[Box.Web.Url]]
      Path = "/api/health"
      Status = 200
      Regex = "\"status\":\\s*\"healthy\""

    [[Box.Web.Url]]
      Path = "/login"
      Status = 200

  [[Box.Web]]
    Display = "https"
    Scheme = "https"
    Port = 443

    [[Box.Web.Url]]
      Path = "/"
      Status = 200
```

---

### WinRM

Connects to Windows Remote Management and executes PowerShell commands.

**Default Port:** `80` (HTTP) or `443` (HTTPS)

| Property | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `Encrypted` | bool | No | `false` | Use HTTPS connection |
| `BadAttempts` | int | No | `0` | Number of failed login attempts before real attempt |
| `Command` | winCommandData[] | No | - | PowerShell commands to execute |

#### winCommandData Structure

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `Command` | string | **Yes** | PowerShell command to execute |
| `Output` | string | No | Expected output |
| `UseRegex` | bool | No | Treat `Output` as regex pattern |

#### Success Criteria

1. WinRM connection succeeds
2. If no commands: `hostname` test command succeeds
3. If commands: Random command executes without errors
4. If `Output` specified: Output must match (exact or regex)

#### Example

```toml
[[Box]]
  Name = "windows-server"
  IP = "10.10.1_.50"

  [[Box.Winrm]]
    Display = "winrm"
    CredLists = ["windows-admins"]
    Port = 5985

    [[Box.Winrm.Command]]
      Command = "Get-Service W32Time | Select-Object -ExpandProperty Status"
      Output = "Running"

    [[Box.Winrm.Command]]
      Command = "Get-Process | Measure-Object | Select-Object -ExpandProperty Count"
      Output = "^[0-9]+$"
      UseRegex = true

  [[Box.Winrm]]
    Display = "winrm-ssl"
    CredLists = ["windows-admins"]
    Port = 5986
    Encrypted = true
```

---

## Complete Configuration Example

```toml
[RequiredSettings]
  EventName = "Cyber Defense Competition"
  EventType = "rvb"
  DBConnectURL = "postgres://user:pass@localhost:5432/quotient"
  BindAddress = "0.0.0.0"

[MiscSettings]
  Delay = 60
  Jitter = 10
  Points = 1
  Timeout = 30
  SlaThreshold = 5
  SlaPenalty = 5

[CredlistSettings]
  [[CredlistSettings.Credlist]]
    CredlistName = "linux-users"
    CredlistPath = "linux.csv"
    CredlistExplainText = "username,password"

  [[CredlistSettings.Credlist]]
    CredlistName = "windows-users"
    CredlistPath = "windows.csv"
    CredlistExplainText = "username,password"

[[Admin]]
  Name = "admin"
  Pw = "changeme"

[[Team]]
  Name = "team01"
  Pw = "team01pass"

[[Team]]
  Name = "team02"
  Pw = "team02pass"

[[Box]]
  Name = "linux-web"
  IP = "10.10.1_.10"

  [[Box.Ping]]
    Display = "ping"
    Count = 2

  [[Box.Ssh]]
    Display = "ssh"
    CredLists = ["linux-users"]

  [[Box.Web]]
    Display = "http"
    [[Box.Web.Url]]
      Path = "/"
      Status = 200

[[Box]]
  Name = "windows-dc"
  IP = "10.10.1_.20"

  [[Box.Ping]]
    Display = "ping"

  [[Box.Ldap]]
    Display = "ldap"
    CredLists = ["windows-users"]
    Domain = "team_.local"

  [[Box.Dns]]
    Display = "dns"
    [[Box.Dns.Record]]
      Kind = "A"
      Domain = "dc.team_.local"
      Answer = ["10.10.1_.20"]
```
