package config

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"quotient/engine/checks"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

var (
	supportedEvents = []string{"rvb", "koth"} // golang doesn't have constant arrays :/
)

type ConfigSettings struct {
	// General engine settings
	RequiredSettings RequiredConfig `toml:"RequiredSettings,omitempty" json:"RequiredSettings,omitempty"`

	// LDAP settings
	LdapSettings LdapAuthConfig `toml:"LdapSettings,omitempty" json:"LdapSettings,omitempty"`

	// Optional settings
	SslSettings SslConfig `toml:"SslSettings,omitempty" json:"SslSettings,omitempty"`

	MiscSettings MiscConfig `toml:"MiscSettings,omitempty" json:"MiscSettings,omitempty"`

	// Restrict information
	UISettings UIConfig `toml:"UISettings,omitempty" json:"UISettings,omitempty"`

	Admin []Admin
	Red   []Red
	Team  []Team
	Box   []Box
}

type RequiredConfig struct {
	EventName    string
	EventType    string
	DBConnectURL string
	BindAddress  string
}

type LdapAuthConfig struct {
	LdapConnectUrl   string
	LdapBindDn       string
	LdapBindPassword string
	LdapSearchBaseDn string
	LdapAdminGroupDn string
	LdapRedGroupDn   string
	LdapTeamGroupDn  string
}

type SslConfig struct {
	HttpsCert string `toml:"httpscert,omitempty" json:"httpscert,omitempty"`
	HttpsKey  string `toml:"httpskey,omitempty" json:"httpskey,omitempty"`
}

type MiscConfig struct {
	EasyPCR             bool
	ShowDebugToBlueTeam bool
	Port                int
	LogoImage           string
	LogFile             string

	StartPaused bool

	// Round settings
	Delay  int
	Jitter int

	// Defaults for checks
	Points       int
	Timeout      int
	SlaThreshold int
	SlaPenalty   int
}

type UIConfig struct {
	DisableInfoPage             bool
	DisableGraphsForBlueTeam    bool
	ShowAnnouncementsForRedTeam bool
}

type User struct {
	Name string
	Pw   string
}

type Admin User
type Red User
type Team User

type Box struct {
	Name string
	IP   string

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

// Load in a config
func (conf *ConfigSettings) SetConfig(path string) error {
	tempConf := ConfigSettings{}
	fileContent, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("configuration file ("+path+") not found:", err)
	}

	if md, err := toml.Decode(string(fileContent), &tempConf); err != nil {
		return err
	} else {
		for _, undecoded := range md.Undecoded() {
			slog.Warn("undecoded configuration key \"" + undecoded.String() + "\" will not be used.")
		}
	}

	// check the configuration and set defaults
	if err := checkConfig(&tempConf); err != nil {
		log.Fatalln("configuration file ("+path+") is invalid:", err)
	}

	// if we're here, the config is valid
	*conf = tempConf

	return nil
}

// general error checking
func checkConfig(conf *ConfigSettings) error {
	// check top level configs

	// required settings

	var errResult error

	if conf.RequiredSettings.EventName == "" {
		errResult = errors.Join(errResult, errors.New("event title blank or not specified"))
	}

	if !slices.Contains(supportedEvents, conf.RequiredSettings.EventType) {
		errResult = errors.Join(errResult, errors.New("not a valid event type"))
	}

	if conf.RequiredSettings.DBConnectURL == "" {
		errResult = errors.Join(errResult, errors.New("no db connect url specified"))
	}

	if conf.RequiredSettings.BindAddress == "" {
		errResult = errors.Join(errResult, errors.New("no bind address specified"))
	}

	for _, admin := range conf.Admin {
		if admin.Name == "" || admin.Pw == "" {
			errResult = errors.Join(errResult, errors.New("admin "+admin.Name+" missing required property"))
		}
	}
	for _, red := range conf.Red {
		if red.Name == "" || red.Pw == "" {
			errResult = errors.Join(errResult, errors.New("red "+red.Name+" missing required property"))
		}
	}
	for _, team := range conf.Team {
		if team.Name == "" || team.Pw == "" {
			errResult = errors.Join(errResult, errors.New("team "+team.Name+" missing required property"))
		}
	}

	// optional settings

	if conf.MiscSettings.Delay == 0 {
		conf.MiscSettings.Delay = 60
	}

	if conf.MiscSettings.Jitter == 0 {
		conf.MiscSettings.Jitter = 5
	}

	if conf.SslSettings != (SslConfig{}) {
		if conf.SslSettings.HttpsCert == "" || conf.SslSettings.HttpsKey == "" {
			errResult = errors.Join(errResult, errors.New("https requires a cert and key pair"))
		}
	}

	if conf.MiscSettings.LogoImage == "" {
		conf.MiscSettings.LogoImage = "/static/assets/quotient.svg"
	}

	if conf.MiscSettings.Port == 0 {
		if conf.SslSettings != (SslConfig{}) {
			conf.MiscSettings.Port = 443
		} else {
			conf.MiscSettings.Port = 80
		}
	}

	if conf.MiscSettings.Jitter >= conf.MiscSettings.Delay {
		errResult = errors.Join(errResult, errors.New("jitter must be smaller than delay"))
	}

	if conf.MiscSettings.Timeout == 0 {
		conf.MiscSettings.Timeout = conf.MiscSettings.Delay / 2
	}
	if conf.MiscSettings.Timeout >= conf.MiscSettings.Delay-conf.MiscSettings.Jitter {
		errResult = errors.Join(errResult, errors.New("timeout must be smaller than delay minus jitter"))
	}

	if conf.MiscSettings.Points == 0 {
		conf.MiscSettings.Points = 1
	}

	if conf.MiscSettings.SlaThreshold == 0 {
		conf.MiscSettings.SlaThreshold = 5
	}

	if conf.MiscSettings.SlaPenalty == 0 {
		conf.MiscSettings.SlaPenalty = conf.MiscSettings.SlaThreshold * conf.MiscSettings.Points
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
	runnerNames := make(map[string]bool)
	for i, box := range conf.Box {
		// Immediately fail if boxes aren't configured properly
		if box.Name == "" {
			return fmt.Errorf("no name found for box %d", i)
		}

		if box.IP == "" {
			return errors.New("no ip found for box " + box.Name)
		}

		// Ensure TeamID replacement chars are lowercase
		box.IP = strings.ToLower(box.IP)
		conf.Box[i].IP = box.IP

		for j, c := range box.Custom {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("custom check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Custom[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Dns {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("dns check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Dns[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Ftp {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("ftp check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Ftp[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Imap {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("imap check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Imap[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Ldap {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("ldap check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Ldap[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Ping {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("ping check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Ping[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Pop3 {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("pop3 check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Pop3[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Rdp {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("rdp check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Rdp[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Smb {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("smb check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Smb[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Smtp {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("smtp check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Smtp[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Sql {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("sql check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Sql[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Ssh {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("ssh check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Ssh[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Tcp {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("tcp check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Tcp[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Vnc {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("vnc check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Vnc[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.Web {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("web check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].Web[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
		for j, c := range box.WinRM {
			if err := c.Verify(box.Name, box.IP, conf.MiscSettings.Points, conf.MiscSettings.Timeout, conf.MiscSettings.SlaPenalty, conf.MiscSettings.SlaThreshold); err != nil {
				errResult = errors.Join(errResult, fmt.Errorf("winrm check %s failed verification: %s", c.Name, err.Error()))
			}
			if _, exists := runnerNames[c.Name]; exists {
				errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", c.Name))
			} else {
				runnerNames[c.Name] = true
			}
			conf.Box[i].WinRM[j] = c
			conf.Box[i].Runners = append(conf.Box[i].Runners, c)
		}
	}

	// errResult is nil by default if no errors occured
	return errResult
}
