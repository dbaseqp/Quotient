Some Unhinged Guy Made Another All-in-one Scoring Engine (SUGMAASE)
=================================

The successor of [DWAYNE-INATOR-5000](https://github.com/DSU-DefSec/DWAYNE-INATOR-5000).

Usage
-----

1. Download this repository (`git clone https://github.com/dbaseqp/SUGMAASE`).
2. Bring up the Postgres database (`docker-compose up --detach`)
3. Compile the code (`cd SUGMAASE; go mod init sugmaase; go mod tidy; go build`).
4. Save your configuration as `./config/event.conf`.
5. Run the engine (`./sugmaase`).

Configuration
-------------

## Cred Lists
Cred lists need to be CSVs specified in the `./config` directory with a `.credlist` extension. The name of the file will be used as the name of the credlist. When password change requests (PCRs) get processed, credlists will only be mutated by changing the password column of an existing user in the defined list. Below is an example.

```csv
joe,s3cret
robby,mypass
johndoe,helloworld
```

## Example configuration (`event.conf`):
Anything you leave blank will be default. 

```toml
##### Required engine settings #####
event = "Awesome Comp"              # Event title
eventtype = "rvb"                   # Scoring algorithm to use
                                        # rvb: score each sevice for each team
                                        # more options will be added later
dbconnecturl = "postgres://engineuser:password@localhost:5432/engine" # configure these in ./.env  

timezone = "America/Los_Angeles"        # Timezone you want to use
jwtprivatekey = "config/privkey.pem"    # Private key used for JWT 
jwtpublickey = "config/pubkey.pem"      # Public key used for JWT

##### Optional engine settings #####
# verbose = true                      # Show score debug info to competitors
# easypcr = true                      # Allow teams to submit password changes
# https = true                        # Enable HTTPS
# port = 443                          # Port to listen on
# cert = "config/cert.pem"            # Path to cert file
# key = "config/key.pem"              # Path to key file
# disableheadtohead = true            # Hide head to head stats (other than current service status) between competitors
# startpaused = true                  # Start the competition paused

##### Round settings #####
# delay = 60                          # delay (seconds) between checks (>0) (default 60)
                                          # note: the "real" max delay will be delay+jitter
# jitter = 15                         # jitter (seconds) between rounds (0<jitter<delay)
# timeout = 30                        # check timeout (must be smaller than delay-jitter)
# servicepoints = 1                   # default points each up check is worth
# slathreshold = 5                    # default checks before incurring SLA violation
# slapoints = 5                       # default points is an SLA penalty (default slathreshold * servicepoints)

##### Admins #####
# Admins have access to all records and information.
# You need at least one admin.

[[admin]]
name = "admin"
pw = "admin"

##### Box configurations/Scoring checks #####

[[box]]
name="castle"
ip = "10.20.x.1"

    # If you want to keep something default, just don't specify it
    # For this box, we're running a default SSH login check
    [[box.ssh]]

    # There are also some configurations you can use on all types of checks
    [[box.smb]]
    stoptime = 2024-01-05T06:00:00-08:00
    [[box.smb]]
    launchtime = 2024-01-05T06:00:00-08:00
    points = 50
    slathreshold = 2
    slapenalty = 50

[[box]]
name = "village"
ip = "10.20.x.2"

    # A custom check that runs command (like shell or python scripts) with sh. Compares output against regex.
    # Command must return exit code 0 to pass.
    [[box.custom]]
    # BOXIP and USERNAME and PASSWORD are replaced with their values when run
    command = "python3 ./test.py BOXIP USERNAME PASSWORD" # Keywords not required
    regex = "success"

    # If you omit a value, it is set to the default
    # For example, if I removed the line port = 4000,
    # the check port would be 53
    [[box.dns]]
    port = 4000 # default 53
        [[box.dns.record]]
        kind = "A" # DNS record type
        domain = "townsquare.sherwood.lan" # Domain query
        answer = ["192.168.1.4",] # List of acceptable answers

        [[box.dns.record]]
        kind = "MX"
        domain = "sherwood.lan"
        answer = ["192.168.1.5", "10.20.1.5"]

    [[box.ftp]]
    port = 55 # default 21
    anonymous = true # default false

        [[box.ftp.file]]
        name = "memo.txt" # file to retrieve
        hash = "9d8453505bdc6f269678e16b3e56c2a2948a41f2c792617cc9611ed363c95b63" # sha256 sum to compare to

        # When multiple files are passed, one is randomly chosen
        # This pattern persists for any multi-item check
        [[box.ftp.file]]
        name = "workfiles.txt" # file to retrieve
        regex = "work.*work" # regex to test against file
    
    [[box.imap]]
    port = 33 # default 143
    encrypted = true # default false

    [[box.ldap]]
    port = 222 # default 636;
    encrypted = true # default false
    domain = "sherwood.lan"

    [[box.ping]]
    count = 3 # default 1
    allowpacketloss = true # default false
    percent = 50 # max percent packet loss

    # Note: RDP is nonfunctional until a good go RDP library is written, or I write one
    [[box.rdp]]
    port = 3389

    [[box.smb]]
    credlists = ["admins",] # for any check using credentials, you can specify the list
    port = 55 # default 21
    anonymous = true # default false

        [[box.smb.file]]
        name = "memo.txt"
        hash = "9d8453505bdc6f269678e16b3e56c2a2948a41f2c792617cc9611ed363c95b63"

        [[box.smb.file]]
        name = "workfiles.txt"
        regex = "work.*work"

    [[box.smtp]]
    encrypted = false # default false
    sender = "hello@scoring.engine"
    receiver = "tuck@sherwood.lan"
    body = "howdy, friar! he's about to have an outlaw for an inlaw!"

    [[box.sql]]
    kind = "mysql" # default mysql

        [[box.sql.query]]
        contains = true
        database = "wordpress"
        table = "users"
        column = "username"
        output = "Tuck"
        
        [[box.sql.query]]
        database = "squirrelmail"
        table = "senders"
        column = "name"
        output = "Toby Turtle" # Must match exactly

        [[box.sql.query]]
        database = "wordpress"
        databaseexists = true # simply checks if database exists with "show databases;"

    [[box.ssh]]
    display = "remote"         # you can set the display name for any check
    privkey = "village_sshkey" # name of private key in checkfiles/

    [[box.ssh]]
    badattempts = 2
    port = 2222

        [[box.ssh.command]]
        command = "cat /etc/passwd"
        contains = true
        output = "robin:"

        [[box.ssh.command]]
        useregex = true
        command = "getent `id`"
        output = '\w.*:[1-9].*:.*'

    [[box.tcp]] # the most simple check. check tcp connect
    port = 4444

    [[box.vnc]]
    port = 5901

    [[box.web]]
    display = "ecom"

        [[box.web.url]]
        path = "/joomla"
        regex = ".*easy to get started creating your website.*"
        
        [[box.web.url]]
        path="/wp-admin.php"
        status = 302

    [[box.web]]
    port = 8006
    scheme = "https"
        
        [[box.web.url]]
        # defaults to successful page retrieval

    [[box.winrm]]
    badattempts = 1
    encrypted = true

        [[box.winrm.command]]
        command = "Get-FileContent memo.txt"
        contains = true
        output = "business as usual in the kingdom!"
```

## Injects
Create injects through the admin view of the Injects portal. Grade team submissions in the same portal. 