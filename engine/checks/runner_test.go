package checks

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebCheckVerification tests Web check configuration validation
func TestWebCheckVerification(t *testing.T) {
	tests := []struct {
		name        string
		check       *Web
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid http check",
			check: &Web{
				Service: Service{
					Target: "10.100.1_.2",
				},
				Scheme: "http",
				Url:    []urlData{{Path: "/", Status: 200}},
			},
			expectError: false,
		},
		{
			name: "valid https check",
			check: &Web{
				Service: Service{
					Target: "10.100.1_.2",
				},
				Scheme: "https",
				Url:    []urlData{{Path: "/admin", Status: 200}},
			},
			expectError: false,
		},
		{
			name: "no urls defined",
			check: &Web{
				Service: Service{
					Target: "10.100.1_.2",
				},
				Scheme: "http",
				Url:    []urlData{},
			},
			expectError: true,
			errorMsg:    "no urls defined",
		},
		{
			name: "default port for http",
			check: &Web{
				Service: Service{
					Target: "10.100.1_.2",
					Port:   0,
				},
				Scheme: "http",
				Url:    []urlData{{Path: "/"}},
			},
			expectError: false,
		},
		{
			name: "default port for https",
			check: &Web{
				Service: Service{
					Target: "10.100.1_.2",
					Port:   0,
				},
				Scheme: "https",
				Url:    []urlData{{Path: "/"}},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
			if tt.expectError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				// Verify defaults were set
				if tt.check.Scheme == "http" && tt.check.Port == 0 {
					assert.Equal(t, 80, tt.check.Port)
				}
				if tt.check.Scheme == "https" && tt.check.Port == 0 {
					assert.Equal(t, 443, tt.check.Port)
				}
			}
		})
	}
}

// TestWebCheckRun tests Web check execution with a real HTTP server
func TestWebCheckRun(t *testing.T) {
	// Create a test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/success" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Welcome to the competition!"))
		} else if r.URL.Path == "/admin" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Admin Panel - Flag{test123}"))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Parse server URL to get host and port
	serverURL := server.URL[7:] // Remove "http://"
	parts := strings.Split(serverURL, ":")

	tests := []struct {
		name           string
		check          *Web
		expectedStatus bool
		expectedError  string
	}{
		{
			name: "successful check with status code",
			check: &Web{
				Service: Service{
					Target:  parts[0],
					Port:    mustAtoi(parts[1]),
					Timeout: 5,
				},
				Scheme: "http",
				Url:    []urlData{{Path: "/success", Status: 200}},
			},
			expectedStatus: true,
		},
		{
			name: "successful check with regex match",
			check: &Web{
				Service: Service{
					Target:  parts[0],
					Port:    mustAtoi(parts[1]),
					Timeout: 5,
				},
				Scheme: "http",
				Url:    []urlData{{Path: "/admin", Regex: "Flag\\{[a-z0-9]+\\}"}},
			},
			expectedStatus: true,
		},
		{
			name: "failed check - wrong status code",
			check: &Web{
				Service: Service{
					Target:  parts[0],
					Port:    mustAtoi(parts[1]),
					Timeout: 5,
				},
				Scheme: "http",
				Url:    []urlData{{Path: "/success", Status: 404}},
			},
			expectedStatus: false,
			expectedError:  "status returned by webserver was incorrect",
		},
		{
			name: "failed check - regex not found",
			check: &Web{
				Service: Service{
					Target:  parts[0],
					Port:    mustAtoi(parts[1]),
					Timeout: 5,
				},
				Scheme: "http",
				Url:    []urlData{{Path: "/success", Regex: "NonExistentPattern"}},
			},
			expectedStatus: false,
			expectedError:  "didn't find regex on page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultsChan := make(chan Result, 1)
			tt.check.Run(1, "01", 1, resultsChan)

			select {
			case result := <-resultsChan:
				assert.Equal(t, tt.expectedStatus, result.Status)
				if tt.expectedError != "" {
					assert.Contains(t, result.Error, tt.expectedError)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Check timed out")
			}
		})
	}
}

// TestDnsCheckVerification tests DNS check configuration validation
func TestDnsCheckVerification(t *testing.T) {
	tests := []struct {
		name        string
		check       *Dns
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid dns check",
			check: &Dns{
				Service: Service{
					Target: "10.100.1_.2",
				},
				Record: []DnsRecord{
					{Kind: "A", Domain: "team_.example.com", Answer: []string{"10.100.1_.2"}},
				},
			},
			expectError: false,
		},
		{
			name: "no records defined",
			check: &Dns{
				Service: Service{
					Target: "10.100.1_.2",
				},
				Record: []DnsRecord{},
			},
			expectError: true,
			errorMsg:    "has no records",
		},
		{
			name: "default port",
			check: &Dns{
				Service: Service{
					Target: "10.100.1_.2",
					Port:   0,
				},
				Record: []DnsRecord{
					{Kind: "A", Domain: "example.com", Answer: []string{"192.168.1.1"}},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
			if tt.expectError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.check.Port == 0 {
					assert.Equal(t, 53, tt.check.Port, "Default DNS port should be 53")
				}
			}
		})
	}
}

// TestDnsCheckRun tests DNS check execution with a real DNS server
func TestDnsCheckRun(t *testing.T) {
	// Start a test DNS server
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("Cannot create UDP listener for DNS test")
		return
	}
	defer pc.Close()

	serverAddr := pc.LocalAddr().String()
	parts := strings.Split(serverAddr, ":")
	serverPort := mustAtoi(parts[1])

	// DNS server handler
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}

			msg := new(dns.Msg)
			if err := msg.Unpack(buf[:n]); err != nil {
				continue
			}

			// Create response
			resp := new(dns.Msg)
			resp.SetReply(msg)

			// Add answer for test.example.com
			if len(msg.Question) > 0 && msg.Question[0].Name == "test.example.com." {
				rr := &dns.A{
					Hdr: dns.RR_Header{
						Name:   msg.Question[0].Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    300,
					},
					A: net.ParseIP("192.168.1.100"),
				}
				resp.Answer = append(resp.Answer, rr)
			}

			packed, _ := resp.Pack()
			pc.WriteTo(packed, addr)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	tests := []struct {
		name           string
		check          *Dns
		expectedStatus bool
	}{
		{
			name: "successful DNS A record lookup",
			check: &Dns{
				Service: Service{
					Target:  "127.0.0.1",
					Port:    serverPort,
					Timeout: 5,
				},
				Record: []DnsRecord{
					{Kind: "A", Domain: "test.example.com", Answer: []string{"192.168.1.100"}},
				},
			},
			expectedStatus: true,
		},
		{
			name: "failed DNS lookup - wrong answer",
			check: &Dns{
				Service: Service{
					Target:  "127.0.0.1",
					Port:    serverPort,
					Timeout: 5,
				},
				Record: []DnsRecord{
					{Kind: "A", Domain: "test.example.com", Answer: []string{"10.0.0.1"}},
				},
			},
			expectedStatus: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultsChan := make(chan Result, 1)
			tt.check.Run(1, "01", 1, resultsChan)

			select {
			case result := <-resultsChan:
				assert.Equal(t, tt.expectedStatus, result.Status)
			case <-time.After(10 * time.Second):
				t.Fatal("Check timed out")
			}
		})
	}
}

// TestSshCheckVerification tests SSH check configuration validation
func TestSshCheckVerification(t *testing.T) {
	tests := []struct {
		name        string
		check       *Ssh
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid ssh check",
			check: &Ssh{
				Service: Service{
					Target:    "10.100.1_.2",
					CredLists: []string{"creds.csv"},
				},
			},
			expectError: false,
		},
		{
			name: "default port",
			check: &Ssh{
				Service: Service{
					Target:    "10.100.1_.2",
					Port:      0,
					CredLists: []string{"creds.csv"},
				},
			},
			expectError: false,
		},
		{
			name: "privkey with bad attempts",
			check: &Ssh{
				Service: Service{
					Target:    "10.100.1_.2",
					CredLists: []string{"creds.csv"},
				},
				PrivKey:     "key.pem",
				BadAttempts: 3,
			},
			expectError: true,
			errorMsg:    "cannot use both private key and bad attempts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
			if tt.expectError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.check.Port == 0 {
					assert.Equal(t, 22, tt.check.Port, "Default SSH port should be 22")
				}
			}
		})
	}
}

// TestSimpleCheckVerification tests basic verification for check types with simple default port logic
func TestSimpleCheckVerification(t *testing.T) {
	tests := []struct {
		name         string
		serviceType  string
		defaultPort  int
		needsCreds   bool
	}{
		{"Tcp", "Tcp", 0, false},
		{"Ping", "Ping", 0, false},
		{"Ftp", "Ftp", 21, true},
		{"Imap", "Imap", 143, true},
		{"Pop3", "Pop3", 110, true},
		{"Ldap", "Ldap", 636, true},
		{"Smb", "Smb", 445, true},
		{"Rdp", "Rdp", 3389, true},
		{"Vnc", "Vnc", 5900, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			var actualType string
			var actualPort int

			service := Service{Target: "10.100.1_.2", Port: 0}
			if tt.needsCreds {
				service.CredLists = []string{"creds.csv"}
			}

			switch tt.serviceType {
			case "Tcp":
				c := &Tcp{Service: service}
				c.Port = 8080 // TCP doesn't have a default, use explicit
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			case "Ping":
				c := &Ping{Service: service}
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			case "Ftp":
				c := &Ftp{Service: service}
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			case "Imap":
				c := &Imap{Service: service}
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			case "Pop3":
				c := &Pop3{Service: service}
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			case "Ldap":
				c := &Ldap{Service: service}
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			case "Smb":
				c := &Smb{Service: service}
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			case "Rdp":
				c := &Rdp{Service: service}
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			case "Vnc":
				c := &Vnc{Service: service}
				err = c.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
				actualType, actualPort = c.ServiceType, c.Port
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.serviceType, actualType)
			if tt.defaultPort > 0 {
				assert.Equal(t, tt.defaultPort, actualPort)
			}
		})
	}
}

// TestSmtpCheckVerification tests SMTP check configuration validation
func TestSmtpCheckVerification(t *testing.T) {
	tests := []struct {
		name        string
		check       *Smtp
		expectError bool
	}{
		{
			name: "valid smtp check",
			check: &Smtp{
				Service: Service{
					Target: "10.100.1_.2",
				},
			},
			expectError: false,
		},
		{
			name: "default port 25",
			check: &Smtp{
				Service: Service{
					Target: "10.100.1_.2",
					Port:   0,
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.check.Port == 0 {
					assert.Equal(t, 25, tt.check.Port, "Default SMTP port should be 25")
				}
			}
		})
	}
}

// TestSqlCheckVerification tests SQL check configuration validation
func TestSqlCheckVerification(t *testing.T) {
	tests := []struct {
		name        string
		check       *Sql
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid mysql check",
			check: &Sql{
				Service: Service{
					Target:    "10.100.1_.2",
					CredLists: []string{"creds.csv"},
				},
				Kind: "mysql",
			},
			expectError: false,
		},
		{
			name: "valid postgres check",
			check: &Sql{
				Service: Service{
					Target:    "10.100.1_.2",
					CredLists: []string{"creds.csv"},
				},
				Kind: "postgres",
			},
			expectError: false,
		},
		{
			name: "default kind",
			check: &Sql{
				Service: Service{
					Target:    "10.100.1_.2",
					Port:      0,
					CredLists: []string{"creds.csv"},
				},
				Kind: "",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
			if tt.expectError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				// Check defaults were set
				if tt.name == "default kind" {
					assert.Equal(t, "mysql", tt.check.Kind, "Default SQL kind should be mysql")
					assert.Equal(t, 3306, tt.check.Port, "Default MySQL port should be 3306")
				}
			}
		})
	}
}

// TestWinRMCheckVerification tests WinRM check configuration validation
func TestWinRMCheckVerification(t *testing.T) {
	tests := []struct {
		name        string
		check       *WinRM
		expectedPort int
	}{
		{
			name: "default unencrypted",
			check: &WinRM{
				Service: Service{
					Target:    "10.100.1_.2",
					Port:      0,
					CredLists: []string{"creds.csv"},
				},
				Encrypted: false,
			},
			expectedPort: 80,
		},
		{
			name: "encrypted",
			check: &WinRM{
				Service: Service{
					Target:    "10.100.1_.2",
					Port:      0,
					CredLists: []string{"creds.csv"},
				},
				Encrypted: true,
			},
			expectedPort: 443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
			assert.NoError(t, err)
			assert.Equal(t, "WinRM", tt.check.ServiceType)
			assert.Equal(t, tt.expectedPort, tt.check.Port)
		})
	}
}

// TestCustomCheckVerification tests Custom check configuration validation
func TestCustomCheckVerification(t *testing.T) {
	tests := []struct {
		name        string
		check       *Custom
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid custom check",
			check: &Custom{
				Service: Service{
					Target: "10.100.1_.2",
				},
				Command: "echo 'test'",
			},
			expectError: false,
		},
		{
			name: "no command defined",
			check: &Custom{
				Service: Service{
					Target: "10.100.1_.2",
				},
				Command: "",
			},
			expectError: true,
			errorMsg:    "no command found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.check.Verify("box01", "10.100.1_.2", 5, 30, 1, 3)
			if tt.expectError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestRunnerCreation tests creating runners from task data
func TestRunnerCreation(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		checkData   interface{}
		expectError bool
	}{
		{
			name:        "create web runner",
			serviceType: "Web",
			checkData: Web{
				Service: Service{Target: "10.100.1.2", Port: 80},
				Scheme:  "http",
				Url:     []urlData{{Path: "/"}},
			},
			expectError: false,
		},
		{
			name:        "create dns runner",
			serviceType: "Dns",
			checkData: Dns{
				Service: Service{Target: "10.100.1.2", Port: 53},
				Record:  []DnsRecord{{Kind: "A", Domain: "test.com", Answer: []string{"192.168.1.1"}}},
			},
			expectError: false,
		},
		{
			name:        "create ssh runner",
			serviceType: "Ssh",
			checkData: Ssh{
				Service: Service{Target: "10.100.1.2", Port: 22, CredLists: []string{"creds.csv"}},
			},
			expectError: false,
		},
		{
			name:        "create tcp runner",
			serviceType: "Tcp",
			checkData: Tcp{
				Service: Service{Target: "10.100.1.2", Port: 8080},
			},
			expectError: false,
		},
		{
			name:        "create ping runner",
			serviceType: "Ping",
			checkData: Ping{
				Service: Service{Target: "10.100.1.2"},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Serialize check data to JSON
			checkData, err := json.Marshal(tt.checkData)
			require.NoError(t, err)

			// Create runner based on service type
			var runner Runner
			switch tt.serviceType {
			case "Web":
				runner = &Web{}
			case "Dns":
				runner = &Dns{}
			case "Ssh":
				runner = &Ssh{}
			case "Tcp":
				runner = &Tcp{}
			case "Ping":
				runner = &Ping{}
			default:
				t.Fatalf("Unknown service type: %s", tt.serviceType)
			}

			// Unmarshal check data into runner
			err = json.Unmarshal(checkData, runner)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				// ServiceType is set during Verify(), not unmarshaling
				err = runner.Verify("box01", "10.100.1.2", 5, 30, 1, 3)
				require.NoError(t, err)
				assert.Equal(t, tt.serviceType, runner.GetType())
			}
		})
	}
}

// TestTeamIdentifierInTargets tests that team identifiers are substituted in targets
func TestTeamIdentifierInTargets(t *testing.T) {
	tests := []struct {
		name           string
		target         string
		teamIdentifier string
		expected       string
	}{
		{
			name:           "IP with single underscore",
			target:         "10.100.1_.2",
			teamIdentifier: "01",
			expected:       "10.100.101.2",
		},
		{
			name:           "IP with multiple underscores",
			target:         "192.168._._",
			teamIdentifier: "42",
			expected:       "192.168.42.42",
		},
		{
			name:           "domain with underscore",
			target:         "team_.example.com",
			teamIdentifier: "05",
			expected:       "team05.example.com",
		},
		{
			name:           "no substitution needed",
			target:         "10.100.1.2",
			teamIdentifier: "01",
			expected:       "10.100.1.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strings.ReplaceAll(tt.target, "_", tt.teamIdentifier)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function to convert string to int
func mustAtoi(s string) int {
	i := 0
	fmt.Sscanf(s, "%d", &i)
	return i
}
