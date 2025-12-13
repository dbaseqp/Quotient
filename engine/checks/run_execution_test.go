package checks

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
)

// TestWebRun_ActualExecution tests Web check Run() with real HTTP server
func TestWebRun_ActualExecution(t *testing.T) {
	tests := []struct {
		name           string
		serverHandler  http.HandlerFunc
		webCheck       *Web
		expectedStatus bool
		expectedError  string
	}{
		{
			name: "successful check - correct status code",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("Hello World"))
			},
			webCheck: func(port int) *Web {
				return &Web{
					Service: Service{
						Target:  "127.0.0.1",
						Port:    port,
						Timeout: 5,
					},
					Scheme: "http",
					Url:    []urlData{{Path: "/", Status: 200}},
				}
			}(0), // Port set later
			expectedStatus: true,
		},
		{
			name: "failed check - wrong status code",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			webCheck: func(port int) *Web {
				return &Web{
					Service: Service{
						Target:  "127.0.0.1",
						Port:    port,
						Timeout: 5,
					},
					Scheme: "http",
					Url:    []urlData{{Path: "/", Status: 200}},
				}
			}(0),
			expectedStatus: false,
			expectedError:  "status returned by webserver was incorrect",
		},
		{
			name: "successful check - regex match",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("Flag{test123}"))
			},
			webCheck: func(port int) *Web {
				return &Web{
					Service: Service{
						Target:  "127.0.0.1",
						Port:    port,
						Timeout: 5,
					},
					Scheme: "http",
					Url:    []urlData{{Path: "/", Regex: `Flag\{[a-z0-9]+\}`}},
				}
			}(0),
			expectedStatus: true,
		},
		{
			name: "failed check - regex not found",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("No flag here"))
			},
			webCheck: func(port int) *Web {
				return &Web{
					Service: Service{
						Target:  "127.0.0.1",
						Port:    port,
						Timeout: 5,
					},
					Scheme: "http",
					Url:    []urlData{{Path: "/", Regex: `Flag\{[a-z0-9]+\}`}},
				}
			}(0),
			expectedStatus: false,
			expectedError:  "didn't find regex on page",
		},
		{
			name: "failed check - connection refused",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				// Server won't start for this test
			},
			webCheck: &Web{
				Service: Service{
					Target:  "127.0.0.1",
					Port:    1, // Port that's not listening
					Timeout: 2,
				},
				Scheme: "http",
				Url:    []urlData{{Path: "/"}},
			},
			expectedStatus: false,
			expectedError:  "web request errored out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server *httptest.Server

			// Start server only if not testing connection failure
			if tt.name != "failed check - connection refused" {
				server = httptest.NewServer(tt.serverHandler)
				defer server.Close()

				// Extract port from server URL
				_, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
				fmt.Sscanf(portStr, "%d", &tt.webCheck.Port)
			}

			// Run the ACTUAL check
			resultsChan := make(chan Result, 1)
			tt.webCheck.Run(1, "01", 1, resultsChan)

			// Wait for result with timeout
			select {
			case result := <-resultsChan:
				assert.Equal(t, tt.expectedStatus, result.Status, "status mismatch")
				if tt.expectedError != "" {
					assert.Contains(t, result.Error, tt.expectedError, "error message mismatch")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("check timed out")
			}
		})
	}
}

// TestTcpRun_ActualExecution tests TCP check Run() with real TCP server
func TestTcpRun_ActualExecution(t *testing.T) {
	tests := []struct {
		name           string
		setupServer    func() (int, func())
		expectedStatus bool
	}{
		{
			name: "successful connection",
			setupServer: func() (int, func()) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatalf("failed to start TCP server: %v", err)
				}

				// Accept connections in background
				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						conn.Close()
					}
				}()

				port := listener.Addr().(*net.TCPAddr).Port
				cleanup := func() { listener.Close() }
				return port, cleanup
			},
			expectedStatus: true,
		},
		{
			name: "connection refused",
			setupServer: func() (int, func()) {
				return 1, func() {} // Port not listening
			},
			expectedStatus: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, cleanup := tt.setupServer()
			defer cleanup()

			tcpCheck := &Tcp{
				Service: Service{
					Target:  "127.0.0.1",
					Port:    port,
					Timeout: 5,
				},
			}

			// Run the ACTUAL check
			resultsChan := make(chan Result, 1)
			tcpCheck.Run(1, "01", 1, resultsChan)

			// Wait for result
			select {
			case result := <-resultsChan:
				assert.Equal(t, tt.expectedStatus, result.Status, "status mismatch")
			case <-time.After(10 * time.Second):
				t.Fatal("check timed out")
			}
		})
	}
}

// TestDnsRun_ActualExecution tests DNS check Run() with real DNS server
func TestDnsRun_ActualExecution(t *testing.T) {
	// Start a real DNS server
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot create UDP listener for DNS test")
	}
	defer func() {
		if err := pc.Close(); err != nil {
			slog.Error("failed to close UDP packet connection", "error", err)
		}
	}()

	port := pc.LocalAddr().(*net.UDPAddr).Port

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

			resp := new(dns.Msg)
			resp.SetReply(msg)

			// Return specific IPs for test domains
			if len(msg.Question) > 0 {
				switch msg.Question[0].Name {
				case "success.test.":
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
				case "failure.test.":
					rr := &dns.A{
						Hdr: dns.RR_Header{
							Name:   msg.Question[0].Name,
							Rrtype: dns.TypeA,
							Class:  dns.ClassINET,
							Ttl:    300,
						},
						A: net.ParseIP("10.0.0.1"), // Wrong IP
					}
					resp.Answer = append(resp.Answer, rr)
				}
			}

			packed, _ := resp.Pack()
			pc.WriteTo(packed, addr)
		}
	}()

	time.Sleep(100 * time.Millisecond) // Wait for server to start

	tests := []struct {
		name           string
		dnsCheck       *Dns
		expectedStatus bool
	}{
		{
			name: "successful A record lookup",
			dnsCheck: &Dns{
				Service: Service{
					Target:  "127.0.0.1",
					Port:    port,
					Timeout: 5,
				},
				Record: []DnsRecord{
					{Kind: "A", Domain: "success.test", Answer: []string{"192.168.1.100"}},
				},
			},
			expectedStatus: true,
		},
		{
			name: "failed - wrong answer",
			dnsCheck: &Dns{
				Service: Service{
					Target:  "127.0.0.1",
					Port:    port,
					Timeout: 5,
				},
				Record: []DnsRecord{
					{Kind: "A", Domain: "failure.test", Answer: []string{"192.168.1.100"}},
				},
			},
			expectedStatus: false,
		},
		{
			name: "failed - no answer",
			dnsCheck: &Dns{
				Service: Service{
					Target:  "127.0.0.1",
					Port:    port,
					Timeout: 5,
				},
				Record: []DnsRecord{
					{Kind: "A", Domain: "notfound.test", Answer: []string{"192.168.1.1"}},
				},
			},
			expectedStatus: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run the ACTUAL check
			resultsChan := make(chan Result, 1)
			tt.dnsCheck.Run(1, "01", 1, resultsChan)

			// Wait for result
			select {
			case result := <-resultsChan:
				assert.Equal(t, tt.expectedStatus, result.Status, "status mismatch")
			case <-time.After(10 * time.Second):
				t.Fatal("check timed out")
			}
		})
	}
}

// TestCustomRun_ActualExecution tests Custom check Run() with real command execution
func TestCustomRun_ActualExecution(t *testing.T) {
	tests := []struct {
		name           string
		command        string
		regex          string
		expectedStatus bool
		expectedError  string
	}{
		{
			name:           "successful command execution",
			command:        "echo 'Success: check passed'",
			expectedStatus: true,
		},
		{
			name:           "failed command - non-zero exit",
			command:        "exit 1",
			expectedStatus: false,
			expectedError:  "command returned error",
		},
		{
			name:           "successful with regex match",
			command:        "echo 'Flag{abc123}'",
			regex:          `Flag\{[a-z0-9]+\}`,
			expectedStatus: true,
		},
		{
			name:           "failed - regex not found",
			command:        "echo 'No flag here'",
			regex:          `Flag\{[a-z0-9]+\}`,
			expectedStatus: false,
			expectedError:  "output incorrect",
		},
		{
			name:           "command with variables - TARGET",
			command:        "echo TARGET",
			expectedStatus: true,
		},
		{
			name:           "command with variables - ROUND",
			command:        "echo 'Round: ROUND'",
			expectedStatus: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			customCheck := &Custom{
				Service: Service{
					Name:    "test-custom",
					Target:  "127.0.0.1",
					Timeout: 5,
				},
				Command: tt.command,
				Regex:   tt.regex,
			}

			// Run the ACTUAL check
			resultsChan := make(chan Result, 1)
			customCheck.Run(1, "01", 1, resultsChan)

			// Wait for result
			select {
			case result := <-resultsChan:
				assert.Equal(t, tt.expectedStatus, result.Status, "status mismatch")
				if tt.expectedError != "" {
					assert.Contains(t, result.Error, tt.expectedError, "error message mismatch")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("check timed out")
			}
		})
	}
}

// TestPingRun_ActualExecution tests Ping check Run()
func TestPingRun_ActualExecution(t *testing.T) {
	// Ping requires ICMP permissions which may not be available in test environment
	t.Run("ping localhost", func(t *testing.T) {
		pingCheck := &Ping{
			Service: Service{
				Target:  "127.0.0.1",
				Timeout: 5,
			},
			Count:           3,
			AllowPacketLoss: false,
		}

		// Run the ACTUAL check
		resultsChan := make(chan Result, 1)
		pingCheck.Run(1, "01", 1, resultsChan)

		// Wait for result
		select {
		case result := <-resultsChan:
			// Ping might fail due to permissions, but test should complete
			t.Logf("Ping result: status=%v, error=%s", result.Status, result.Error)
		case <-time.After(10 * time.Second):
			t.Fatal("check timed out")
		}
	})
}

// TestServiceTimeout verifies timeout handling
func TestServiceTimeout(t *testing.T) {
	t.Run("web check times out", func(t *testing.T) {
		// Server that hangs
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(10 * time.Second) // Longer than timeout
		}))
		defer server.Close()

		_, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
		var port int
		fmt.Sscanf(portStr, "%d", &port)

		webCheck := &Web{
			Service: Service{
				Target:  "127.0.0.1",
				Port:    port,
				Timeout: 1, // 1 second timeout
			},
			Scheme: "http",
			Url:    []urlData{{Path: "/"}},
		}

		resultsChan := make(chan Result, 1)
		webCheck.Run(1, "01", 1, resultsChan)

		// Should timeout and return result
		select {
		case result := <-resultsChan:
			assert.False(t, result.Status, "should fail on timeout")
			// Race between HTTP client timeout and wrapper timeout - either is acceptable
			validErrors := []string{"web request errored out", "check timeout exceeded"}
			assert.Contains(t, validErrors, result.Error, "expected a timeout error")
		case <-time.After(5 * time.Second):
			t.Fatal("test timed out waiting for check timeout")
		}
	})
}
