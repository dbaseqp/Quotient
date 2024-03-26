package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"quotient/checks"

	"golang.org/x/exp/slices"

	"github.com/BurntSushi/toml"
)

var (
	configErrors    = []string{}
	supportedEvents = []string{"rvb", "koth"} // golang doesn't have constant arrays :/
)

type Config struct {
	// General engine settings
	Event         string
	EventType     string
	DBConnectURL  string
	BindAddress   string
	Interface     string
	Subnet        string
	Timezone      string
	JWTPrivateKey string
	JWTPublicKey  string

	// LDAP settings
	LdapConnectUrl   string
	LdapBindDn       string
	LdapBindPassword string
	LdapUserBaseDn   string
	LdapAdminBaseDn  string
	LdapTeamFilter   string

	// Optional settings
	EasyPCR     bool
	Verbose     bool
	Port        int
	Https       bool
	Cert        string `toml:"cert,omitempty" json:"cert,omitempty"`
	Key         string `toml:"key,omitempty" json:"key,omitempty"`
	StartPaused bool
	// Restrict information
	DisableInfoPage      bool
	DisableHeadToHead    bool
	DisableExternalPorts bool

	// Round settings
	Delay  int
	Jitter int

	// Defaults for checks
	Points       int
	Timeout      int
	SlaThreshold int
	SlaPenalty   int

	Admin []Admin
	Box   []Box
}

// mostly used for Form and NOT FOR DATABASE... maybe change later
type Admin struct {
	ID   uint
	Name string
	Pw   string
}

type Box struct {
	Name string
	IP   string
	FQDN string `toml:"FQDN,omitempty" json:"fqdn,omitempty"`

	// Internal use but not in config file
	Runners []checks.Runner `toml:"-"`

	Custom []checks.Custom `toml:"Custom,omitempty" json:"custom,omitempty"`
	Dns    []checks.Dns    `toml:"Dns,omitempty" json:"dns,omitempty"`
	Ftp    []checks.Ftp    `toml:"Ftp,omitempty" json:"ftp,omitempty"`
	Imap   []checks.Imap   `toml:"Imap,omitempty" json:"imap,omitempty"`
	Ldap   []checks.Ldap   `toml:"Ldap,omitempty" json:"ldap,omitempty"`
	Ping   []checks.Ping   `toml:"Ping,omitempty" json:"ping,omitempty"`
	Pop3   []checks.Pop3   `toml:"Pop3,omitempty" json:"pop3,omitempty"`
	Rdp    []checks.Rdp    `toml:"Rdp,omitempty" json:"rdp,omitempty"`
	Smb    []checks.Smb    `toml:"Smb,omitempty" json:"smb,omitempty"`
	Smtp   []checks.Smtp   `toml:"Smtp,omitempty" json:"smtp,omitempty"`
	Sql    []checks.Sql    `toml:"Sql,omitempty" json:"sql,omitempty"`
	Ssh    []checks.Ssh    `toml:"Ssh,omitempty" json:"ssh,omitempty"`
	Tcp    []checks.Tcp    `toml:"Tcp,omitempty" json:"tcp,omitempty"`
	Vnc    []checks.Vnc    `toml:"Vnc,omitempty" json:"vnc,omitempty"`
	Web    []checks.Web    `toml:"Web,omitempty" json:"web,omitempty"`
	WinRM  []checks.WinRM  `toml:"Winrm,omitempty" json:"winrm,omitempty"`
}

func readConfig(path string) Config {
	conf := Config{}
	fileContent, err := os.ReadFile(path)
	if err != nil {
		log.Fatalln("Configuration file ("+path+") not found:", err)
	}
	if md, err := toml.Decode(string(fileContent), &conf); err != nil {
		log.Fatalln(err)
	} else {
		for _, undecoded := range md.Undecoded() {
			errMsg := "[WARN] Undecoded configuration key \"" + undecoded.String() + "\" will not be used."
			configErrors = append(configErrors, errMsg)
			log.Println(errMsg)
		}
	}
	return conf
}

// general error checking
func checkConfig(conf *Config) error {
	// check top level configs

	// required settings

	var errResult error

	if conf.Event == "" {
		errResult = errors.Join(errResult, errors.New("event title blank or not specified"))
	}

	if !slices.Contains(supportedEvents, conf.EventType) {
		errResult = errors.Join(errResult, errors.New("not a valid event type"))
	}

	if conf.DBConnectURL == "" {
		errResult = errors.Join(errResult, errors.New("no db connect url specified"))
	}

	if conf.BindAddress == "" {
		errResult = errors.Join(errResult, errors.New("no bind address specified"))
	}

	if conf.Interface == "" {
		errResult = errors.Join(errResult, errors.New("no interface specified"))
	}

	if conf.Subnet == "" {
		errResult = errors.Join(errResult, errors.New("no rotation subnet specified"))
	}

	if conf.JWTPrivateKey == "" || conf.JWTPublicKey == "" {
		errResult = errors.Join(errResult, errors.New("missing JWT private/public key pair"))
	}

	if len(conf.Admin) == 0 {
		errResult = errors.Join(errResult, errors.New("missing at least 1 defined admin user"))
	} else {
		for _, admin := range conf.Admin {
			if admin.Name == "" || admin.Pw == "" {
				errResult = errors.Join(errResult, errors.New("admin "+admin.Name+" missing required property"))
			}
		}
	}

	if conf.Timezone == "" {
		errResult = errors.Join(errResult, errors.New("no timezone specified"))
	}

	// optional settings

	if conf.Delay == 0 {
		conf.Delay = 60
	}

	if conf.Jitter == 0 {
		conf.Jitter = 5
	}

	if conf.Https == true {
		if conf.Cert == "" || conf.Key == "" {
			errResult = errors.Join(errResult, errors.New("https requires a cert and key pair"))
		}
	}

	if conf.Port == 0 {
		if conf.Https {
			conf.Port = 443
		} else {
			conf.Port = 80
		}
	}

	if conf.Jitter >= conf.Delay {
		errResult = errors.Join(errResult, errors.New("jitter must be smaller than delay"))
	}

	if conf.Timeout == 0 {
		conf.Timeout = conf.Delay / 2
	}
	if conf.Timeout >= conf.Delay-conf.Jitter {
		errResult = errors.Join(errResult, errors.New("timeout must be smaller than delay minus jitter"))
	}

	if conf.Points == 0 {
		conf.Points = 1
	}

	if conf.SlaThreshold == 0 {
		conf.SlaThreshold = 5
	}

	if conf.SlaPenalty == 0 {
		conf.SlaPenalty = conf.SlaThreshold * conf.Points
	}

	if conf.StartPaused {
		enginePauseWg.Add(1)
		enginePause = true
	}

	// =======================================
	// prepare for box config checking
	// sort boxes
	sort.SliceStable(conf.Box, func(i, j int) bool {
		return conf.Box[i].IP < conf.Box[j].IP
	})

	// check for duplicate box names
	dupeBoxMap := make(map[string]bool)
	for _, b := range conf.Box {
		if b.Name == "" {
			errResult = errors.Join(errResult, errors.New("a box is missing a name"))
		}
		if _, ok := dupeBoxMap[b.Name]; ok {
			errResult = errors.Join(errResult, errors.New("duplicate box name found: "+b.Name))
		}
	}

	// ACTUALLY DO CHECKS FOR BOX AND SERVICE CONFIGURATION
	err := parseEnvironment(conf.Box)
	if err != nil {
		errResult = errors.Join(errResult, err)
	}

	// look for duplicate check names
	for _, box := range conf.Box {
		for j := 0; j < len(box.Runners)-1; j++ {
			if box.Runners[j].GetService().Name == box.Runners[j+1].GetService().Name {
				errResult = errors.Join(errResult, errors.New("duplicate check name '"+box.Runners[j].GetService().Name+"' and '"+box.Runners[j+1].GetService().Name+"' for box "+box.Name))
			}
		}
	}

	// errResult is nil by default if no errors occured
	return errResult
}

func parseEnvironment(boxes []Box) error {
	var errResult error

	for i, box := range boxes {
		// Immediately fail if boxes aren't configured properly
		if box.Name == "" {
			return fmt.Errorf("no name found for box %d", i)
		}

		if box.IP == "" {
			return errors.New("no ip found for box " + box.Name)
		}

		// Ensure TeamID replacement chars are lowercase
		box.IP = strings.ToLower(box.IP)
		boxes[i].IP = box.IP
		box.FQDN = strings.ToLower(box.FQDN)
		boxes[i].FQDN = box.FQDN

		for j, c := range box.Custom {
			if c.Display == "" {
				c.Display = "custom"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if len(c.CredLists) < 1 && !strings.Contains(c.Command, "USERNAME") && !strings.Contains(c.Command, "PASSWORD") {
				c.Anonymous = true
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Custom[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Dns {
			c.Anonymous = true // call me when you need authed DNS
			if c.Display == "" {
				c.Display = "dns"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if len(c.Record) < 1 {
				errResult = errors.Join(errResult, errors.New("dns check "+c.Name+" has no records"))
			}
			if c.Port == 0 {
				c.Port = 53
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Dns[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Ftp {
			if c.Display == "" {
				c.Display = "ftp"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 21
			}
			for _, f := range c.File {
				if f.Regex != "" && f.Hash != "" {
					errResult = errors.Join(errResult, errors.New("can't have both regex and hash for ftp file check"))
				}
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Ftp[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Imap {
			if c.Display == "" {
				c.Display = "imap"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 143
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Imap[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Ldap {
			if c.Display == "" {
				c.Display = "ldap"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 636
			}
			if c.Anonymous {
				errResult = errors.Join(errResult, errors.New("anonymous ldap not supported"))
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Ldap[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Ping {
			c.Anonymous = true
			if c.Count == 0 {
				c.Count = 1
			}
			if c.Display == "" {
				c.Display = "ping"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Ping[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Pop3 {
			if c.Display == "" {
				c.Display = "pop3"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 110
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Pop3[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Rdp {
			if c.Display == "" {
				c.Display = "rdp"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 3389
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Rdp[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Smb {
			if c.Display == "" {
				c.Display = "smb"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 445
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Smb[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Smtp {
			if c.Display == "" {
				c.Display = "smtp"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 25
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Smtp[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Sql {
			if c.Display == "" {
				c.Display = "sql"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Kind == "" {
				c.Kind = "mysql"
			}
			if c.Port == 0 {
				c.Port = 3306
			}
			for _, q := range c.Query {
				if q.UseRegex {
					regexp.MustCompile(q.Output)
				}
				if q.UseRegex && q.Contains {
					errResult = errors.Join(errResult, errors.New("cannot use both regex and contains"))
				}
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Sql[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Ssh {
			if c.Display == "" {
				c.Display = "ssh"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 22
			}
			if c.PrivKey != "" && c.BadAttempts != 0 {
				errResult = errors.Join(errResult, errors.New("can not have bad attempts with pubkey for ssh"))
			}
			for _, r := range c.Command {
				if r.UseRegex {
					regexp.MustCompile(r.Output)
				}
				if r.UseRegex && r.Contains {
					errResult = errors.Join(errResult, errors.New("cannot use both regex and contains"))
				}
			}
			if c.Anonymous {
				errResult = errors.Join(errResult, errors.New("anonymous ssh not supported"))
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Ssh[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Tcp {
			c.Anonymous = true
			if c.Display == "" {
				c.Display = "tcp"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				errResult = errors.Join(errResult, errors.New("tcp port required"))
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Tcp[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Vnc {
			if c.Display == "" {
				c.Display = "vnc"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				c.Port = 5900
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Vnc[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.Web {
			if c.Display == "" {
				c.Display = "web"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				if c.Scheme == "https" {
					c.Port = 443
				} else {
					c.Port = 80
				}
			}
			if len(c.Url) == 0 {
				errResult = errors.Join(errResult, errors.New("no urls specified for web check "+c.Name))
			}
			if len(c.CredLists) < 1 {
				c.Anonymous = true
			}
			if c.Scheme == "" {
				c.Scheme = "http"
			}
			for _, u := range c.Url {
				if u.Diff != 0 && u.CompareFile == "" {
					errResult = errors.Join(errResult, errors.New("need compare file for diff in web"))
				}
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].Web[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
		for j, c := range box.WinRM {
			if c.Display == "" {
				c.Display = "winrm"
			}
			if c.Name == "" {
				c.Name = box.Name + "-" + c.Display
			}
			if c.Port == 0 {
				if c.Encrypted {
					c.Port = 443
				} else {
					c.Port = 80
				}
			}
			if c.Anonymous {
				errResult = errors.Join(errResult, errors.New("anonymous winrm not supported"))
			}
			for _, r := range c.Command {
				if r.UseRegex {
					regexp.MustCompile(r.Output)
				}
				if r.UseRegex && r.Contains {
					errResult = errors.Join(errResult, errors.New("cannot use both regex and contains"))
				}
			}

			if err := configureService(&c.Service, box); err != nil {
				errResult = errors.Join(errResult, err)
			}
			boxes[i].WinRM[j] = c
			boxes[i].Runners = append(boxes[i].Runners, c)
		}
	}
	return nil
}

// configure general service attributes
func configureService(service *checks.Service, box Box) error {
	service.BoxName = box.Name
	service.BoxIP = box.IP
	service.BoxFQDN = box.FQDN

	if service.Points == 0 {
		service.Points = eventConf.Points
	}
	if service.Timeout == 0 {
		service.Timeout = eventConf.Timeout
	}
	if service.SlaPenalty == 0 {
		service.SlaPenalty = eventConf.SlaPenalty
	}
	if service.SlaThreshold == 0 {
		service.SlaThreshold = eventConf.SlaThreshold
	}
	if service.StopTime.IsZero() {
		service.StopTime = time.Now().AddDate(3, 0, 0) // 3 years ahead should be far enough
	}
	for _, list := range service.CredLists {
		if !strings.HasSuffix(list, ".credlist") {
			return errors.New("check " + service.Name + " has invalid credlist names")
		}
	}
	return nil
}
