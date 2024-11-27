# Quotient

## Quick Start

```bash
git clone https://github.com/dbaseqp/Quotient
cd Quotient
docker build ./ -t quotient_server
docker-compose up --detach
```

If you want to rotate the IP, use the following script:

```bash
rotate.sh
```

IP rotation is still currently under development.

## Configuration

1. How to Create Configuration File
2. Configuration Sections

### How to Create Configuration File

#### Basics

The configuration file is a TOML file that is used to configure the scoring engine. The configuration file is located in the `./config` directory and is named `event.conf`. 

The configuration file is broken up into sections. Only the `RequiredSettings` section is required. The other sections are optional and can be omitted if not needed.

#### Credential Lists

Cred lists need to be CSVs specified in the `./config/credlists` directory with a `.credlist` extension. The name of the file will be used as the name of the credlist. When password change requests (PCRs) get processed, credlists will only be mutated by changing the password column of an existing user in the defined list. This means submitting a PCR with a user that does not exist will ignore that specific entry. Below is an example. See the below configuration examples to specify credlists for checks.

```
joe,s3cret
robby,mypass
johndoe,helloworld
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

The "DBConnectURL" will use values you popualte in the `.env` file. The `BindAddress` is the IP address the scoring engine will bind to. If you are deploying in the Docker container, this can be set to `0.0.0.0`.

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
[UiSettings]
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

```toml
[[box]]
name = "web01"
ip = "10.100.1_.2"
```

Each service check is defined beneath a box.

```toml
[[box]]
name = "web01"
ip = "10.100.1_.2"

    [[box.web]]
    display = "web01"

        [[box.web.url]]
        path = "/index.html"