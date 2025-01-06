package config

import (
	"errors"
	"fmt"
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

	Custom []*checks.Custom `toml:"Custom,omitempty" json:"custom,omitempty"`
	Dns    []*checks.Dns    `toml:"Dns,omitempty" json:"dns,omitempty"`
	Ftp    []*checks.Ftp    `toml:"Ftp,omitempty" json:"ftp,omitempty"`
	Imap   []*checks.Imap   `toml:"Imap,omitempty" json:"imap,omitempty"`
	Ldap   []*checks.Ldap   `toml:"Ldap,omitempty" json:"ldap,omitempty"`
	Ping   []*checks.Ping   `toml:"Ping,omitempty" json:"ping,omitempty"`
	Pop3   []*checks.Pop3   `toml:"Pop3,omitempty" json:"pop3,omitempty"`
	Rdp    []*checks.Rdp    `toml:"Rdp,omitempty" json:"rdp,omitempty"`
	Smb    []*checks.Smb    `toml:"Smb,omitempty" json:"smb,omitempty"`
	Smtp   []*checks.Smtp   `toml:"Smtp,omitempty" json:"smtp,omitempty"`
	Sql    []*checks.Sql    `toml:"Sql,omitempty" json:"sql,omitempty"`
	Ssh    []*checks.Ssh    `toml:"Ssh,omitempty" json:"ssh,omitempty"`
	Tcp    []*checks.Tcp    `toml:"Tcp,omitempty" json:"tcp,omitempty"`
	Vnc    []*checks.Vnc    `toml:"Vnc,omitempty" json:"vnc,omitempty"`
	Web    []*checks.Web    `toml:"Web,omitempty" json:"web,omitempty"`
	WinRM  []*checks.WinRM  `toml:"Winrm,omitempty" json:"winrm,omitempty"`
}

// Load in a config
func (conf *ConfigSettings) SetConfig(path string) error {
	tempConf := ConfigSettings{}
	fileContent, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("configuration file ("+path+") not found:", err)
	}

	md, err := toml.Decode(string(fileContent), &tempConf)
	if err != nil {
		return err
	}

	for _, undecoded := range md.Undecoded() {
		slog.Warn("undecoded configuration key \"" + undecoded.String() + "\" will not be used.")
	}

	// check the configuration and set defaults
	if err := checkConfig(&tempConf); err != nil {
		return fmt.Errorf("configuration file ("+path+") is invalid:", err)
	}

	// if we're here, the config is valid
	*conf = tempConf

	return nil
}

// general error checking
func checkConfig(conf *ConfigSettings) error {
	var errResult error

	// required settings
	if conf.RequiredSettings.EventName == "" {
		errResult = errors.Join(errResult, errors.New("event title blank or not specified"))
	}

	if !slices.Contains(supportedEvents, conf.RequiredSettings.EventType) {
		errResult = errors.Join(errResult, errors.New("not a valid event type"))
	}

	if conf.RequiredSettings.DBConnectURL == "" {
		dbUser := os.Getenv("POSTGRES_USER")
		dbPassword := os.Getenv("POSTGRES_PASSWORD")
		dbHost := os.Getenv("POSTGRES_HOST")
		dbDatabase := os.Getenv("POSTGRES_DB")
		if dbUser != "" && dbPassword != "" && dbHost != "" && dbDatabase != "" {
			conf.RequiredSettings.DBConnectURL = fmt.Sprintf("postgres://%s:%s@%s:5432/%s", dbUser, dbPassword, dbHost, dbDatabase)
		} else {
			errResult = errors.Join(errResult, errors.New("no db connect url specified"))
		}
	}

	if conf.RequiredSettings.BindAddress == "" {
		errResult = errors.Join(errResult, errors.New("no bind address specified"))
	}

	// check top level configs
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

	// optional settings, set defaults
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

	// validate times and scoring info
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
	// sort boxes by IP
	sort.SliceStable(conf.Box, func(i, j int) bool {
		return conf.Box[i].IP < conf.Box[j].IP
	})

	// check for duplicate box names
	dupeBoxMap := make(map[string]bool)
	runnerNames := make(map[string]bool)

	// ACTUALLY DO CHECKS FOR BOX AND SERVICE CONFIGURATION
	for i := range conf.Box {
		if conf.Box[i].Name == "" {
			return fmt.Errorf("no name found for box %d", i)
		}
		if conf.Box[i].IP == "" {
			return fmt.Errorf("no IP found for box %s", conf.Box[i].Name)
		}
		if _, ok := dupeBoxMap[conf.Box[i].Name]; ok {
			errResult = errors.Join(errResult, errors.New("duplicate box name found: "+conf.Box[i].Name))
		}

		// Ensure TeamID replacement chars are lowercase
		conf.Box[i].IP = strings.ToLower(conf.Box[i].IP)

		allChecks := []checks.Runner{}
		checkSets := [][]checks.Runner{
			getRunners(conf.Box[i].Custom), getRunners(conf.Box[i].Dns), getRunners(conf.Box[i].Ftp), getRunners(conf.Box[i].Imap),
			getRunners(conf.Box[i].Ldap), getRunners(conf.Box[i].Ping), getRunners(conf.Box[i].Pop3), getRunners(conf.Box[i].Rdp),
			getRunners(conf.Box[i].Smb), getRunners(conf.Box[i].Smtp), getRunners(conf.Box[i].Sql), getRunners(conf.Box[i].Ssh),
			getRunners(conf.Box[i].Tcp), getRunners(conf.Box[i].Vnc), getRunners(conf.Box[i].Web), getRunners(conf.Box[i].WinRM),
		}
		for _, checks := range checkSets {
			for _, check := range checks {
				if err := check.Verify(
					conf.Box[i].Name,
					conf.Box[i].IP,
					conf.MiscSettings.Points,
					conf.MiscSettings.Timeout,
					conf.MiscSettings.SlaPenalty,
					conf.MiscSettings.SlaThreshold,
				); err != nil {
					errResult = errors.Join(errResult, err)
				}
				if _, exists := runnerNames[check.GetName()]; exists {
					errResult = errors.Join(errResult, fmt.Errorf("duplicate runner name found: %s", check.GetName()))
				} else {
					runnerNames[check.GetName()] = true
				}
				allChecks = append(allChecks, check)
			}
		}
		conf.Box[i].Runners = allChecks
	}

	// errResult is nil by default if no errors occured
	return errResult
}

func getRunners[T checks.Runner](arr []T) []checks.Runner {
	out := make([]checks.Runner, len(arr))
	for i, v := range arr {
		out[i] = v
	}
	return out
}

// Returns a flat list of all checks across all boxes.
func (conf *ConfigSettings) AllChecks() []checks.Runner {
	var out []checks.Runner
	for _, box := range conf.Box {
		out = append(out, box.Runners...)
	}
	return out
}
