# Quotient

## Prerequisites

Ensure you have the following installed on your system:
- Docker
- Docker Compose

## Quick Start

```bash
git clone --recurse-submodules https://github.com/dbaseqp/Quotient
cd Quotient
docker-compose up --build --detach
```

## Architecture

The system is designed as a group of docker components using docker compose in docker-compose.yml. These components are:
1. Server - this is the scoring engine, the web frontend and API, configuration parser, and coordinator of scoring checks.
2. Database - PostgreSQL database that keeps state for each of the checks and round information.
3. Redis - This passes data between the runners and the scoring engine as a queue.
4. Runner - This alpine container has go code that runs the service check after retrieving the task from redis. It needs to be customize if custom checks require additionally installed software like python modules (automatically installed from requirements.txt) or alpine packages. This is managed by Dockerfile.runner and can be rebuilt with `docker-compose build runner`.
5. Divisor - Docker container with elevated privileges that randomly selects an IP address from a configured subnet, and assigns from a configurable pool size to the docker runner containers. This needs to be able to query docker to determine the docker hosts and to be able to manage host network iptables. This is a separate git repo and a submodule of this repo under ./divisor.

## Troubleshooting

If you encounter any issues during setup or operation, consider the following:
- Check the logs for any error messages using `docker-compose logs`.
- Verify that all environment variables are correctly set in the `.env` file.
- Make sure that config values are set in `event.conf` before running the engine.
- Set `vm.overcommit_memory=1` on the host to avoid Redis warnings. Run `sudo sysctl vm.overcommit_memory=1` or add `vm.overcommit_memory = 1` to `/etc/sysctl.conf` and reboot.

## Web setup

Through the Admin UI you will have to specify the "Identifier" for each team. This is the unique part of the target address. Also, you will need to mark the team as "Active" so that the team can start scoring.

If you want to rotate IPs, configure [Divisor](https://github.com/dbaseqp/Divisor).

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
EventType = "rvb"
DBConnectURL = "postgres://engineuser:password@quotient_database:5432/engine"
BindAddress = "0.0.0.0"
```

The "DBConnectURL" will use values you populate in the `.env` file. The `BindAddress` is the IP address the scoring engine will bind to. If you are deploying in the Docker container, this can be set to `0.0.0.0`.

#### Ldap Settings

```toml
[LdapSettings]
LdapConnectUrl = "ldap://ldap.yournet.org:389"
LdapBindDn = "CN=Scoring Engine Service Account,OU=service accounts,DC=yournet,DC=org"
LdapBindPassword = "password"
LdapSearchBaseDn = "OU=Users,DC=yournet,DC=org"
LdapAdminGroupDn = "CN=YourAdmins,OU=Groups,DC=yournet,DC=org"
LdapTeamGroupDn = "CN=YourBlueTeam,OU=Groups,DC=yournet,DC=org"
LdapRedGroupDn = "CN=YourRedTeam,OU=Groups,DC=yournet,DC=org"
```

If using LDAPS for the Docker deployment, make sure you add the cert to the `./config/certs` so that it gets added to the certificate store.

#### SSL Settings

```toml
[SslSettings]
HttpsCert = "/path/to/https/cert"
HttpsKey = "/path/to/https/key"
```

#### Misc Settings

```toml
[MiscSettings]
EasyPCR = true
Port = 80
LogoImage = "/static/assets/quotient.svg"
StartPaused = true

Delay = 60
Jitter = 10

Points = 5
Timeout = 30
SlaThreshold = 5
SlaPenalty = 50
```

#### UI Settings

```toml
[UISettings]
DisableInfoPage = true
DisableGraphsForBlueTeam = true
ShowAnnouncementsForRedTeam = true
```

#### Local Auth

```toml
[[team]]
name = "Team01"
pw = "password"

[[admin]]
name = "admin"
pw = "password"

[[red]]
name = "red01"
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

Custom checks can be added to the `./custom-checks/` directory. It is very common to make the custom check simply run some other script that you have written that has the necessary logic to check the service. The script should return a 0 if the service is up and anything else if it is down. The script should be executable. The script will be mounted in the `/app/checks/` directory of the runner. If the script invokes external dependencies or needs to have a specific run time, this should be added to the Dockerfile.runner and the runner rebuilt and redeployed.

For a detailed walkthrough of writing custom checks, see [docs/custom-checks.md](docs/custom-checks.md).

### Full Example Configuration

Below is a complete example configuration directly taken from a non-WRCCDC invitational. This was the exact configuration used, minus ommitted passwords:

```toml
[RequiredSettings]
  EventName = "2026 SEMO Invitational"
  EventType = "rvb"
  DBConnectURL = "postgres://engineuser:[postgress_password]@quotient_database:5432/engine"
  BindAddress = "0.0.0.0"
[MiscSettings]
  EasyPCR = true
  ShowDebugToBlueTeam = false
  LogoImage = "/static/assets/quotient.svg"
  StartPaused = true
  
  Delay = 60
  Jitter = 10
  
  Points = 5
  Timeout = 30
  SlaThreshold = 5
  SlaPenalty = 25
[CredlistSettings]
  [[CredlistSettings.Credlist]]
    CredlistName = "AD"
    CredlistPath = "AD.credlist"
    CredlistExplainText = "username,password"
# ===========================================
# ADMIN ACCOUNTS
# ===========================================
[[Admin]]
  Name = "admin"
  Pw = "Changeme_example"
# ===========================================
# TEAM ACCOUNTS
# ===========================================
[[Team]]
  Name = "redteam"
  Pw = "Changeme_example"
[[Team]]
  Name = "guest"
  PW = "Changeme_example"
[[Team]]
  Name = "team1"
  Pw = "Changeme_example"
[[Team]]
  Name = "team2"
  Pw = "Changeme_example"
[[Team]]
  Name = "team3"
  Pw = "Changeme_example"
[[Team]]
  Name = "team4"
  Pw = "Changeme_example"
[[Team]]
  Name = "team5"
  Pw = "Changeme_example"
  
[[Team]]
  Name = "team6"
  Pw = "Changeme_example"
  
[[Team]]
  Name = "team7"
  Pw = "Changeme_example"
# ===========================================
# BOX DEFINITIONS
# ===========================================
# ---- Ubuntu-Ecomm (TTP Service) ----
[[Box]]
  Name = "Ubuntu-Ecomm"
  IP = "172.16._.38"
  [[Box.Web]]
    Display = "http"
    Scheme = "http"
    Port = 80
    Points = 5
    [[Box.Web.Url]]
      Path = "/"
      Status = 200
  
  [[Box.Ssh]]
  Display = "ssh"
  CredLists = ["AD"]
  Points = 5
  
# ---- Fedora-Webmail (SMTP, POP3) ----
[[Box]]
  Name = "Fedora-Webmail"
  IP = "172.16._.17"
  [[Box.Smtp]]
    Display = "smtp"
    Port = 25
    CredLists = ["AD"]
    Domain = "@comp.local"
    RequireAuth = true
    Points = 5
  [[Box.Pop3]]
    Display = "pop3"
    Port = 110
    CredLists = ["AD"]
    Domain = "@comp.local"
    Points = 5
# ---- Devuan-Web (HTTP) ----
[[Box]]
  Name = "Devuan-Web"
  IP = "172.16._.21"
  [[Box.Web]]
    Display = "http"
    Scheme = "http"
    Port = 80
    Points = 5
    [[Box.Web.Url]]
      Path = "/"
      Status = 200
# ---- Win-AD (DNS, SSH) ----
[[Box]]
  Name = "Win-AD"
  IP = "172.16._.1"
  [[Box.Dns]]
    Display = "dns"
    Port = 53
    Points = 5
    [[Box.Dns.Record]]
      Kind = "A"
      Domain = "splunk.comp.local"
      Answer = ["172.16.1.20"]
    
    [[Box.Ssh]]
    Display = "ssh"
    CredLists = ["AD"]
    Points = 5
# ---- Win-FTP (FTP) ----
[[Box]]
  Name = "Win-FTP"
  IP = "172.16._.50"
  [[Box.Ftp]]
    Display = "ftp"
    Port = 21
    CredLists = ["AD"]
    Points = 5
# ---- Win-Web (HTTP) ----
[[Box]]
  Name = "Win-Web"
  IP = "172.16._.150"
  [[Box.Web]]
    Display = "http"
    Scheme = "http"
    Port = 80
    Points = 5
    [[Box.Web.Url]]
      Path = "/Default.aspx"
      Status = 200
# ---- Oracle-Splunk (HTTP) ----
[[Box]]
  Name = "Oracle-Splunk"
  IP = "172.16._.20"
  [[Box.Web]]
    Display = "http"
    Scheme = "http"
    Port = 8000
    Points = 5
    [[Box.Web.Url]]
      Path = "/en-US/account/login?return_to_=%2Fen-US%2F"
      Status = 200
```

## Contributing

Please fork the repository and submit a pull request. For major changes, please open an issue first to discuss what you would like to change.

## License

This project is licensed under the GNU General Public License v3.0 - see the LICENSE file for details.

## Contact

For support or questions, please open a GitHub issue.
