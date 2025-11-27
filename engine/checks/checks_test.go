package checks

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTeamIdentifierSubstitution tests that team identifiers are correctly substituted in IP addresses
func TestTeamIdentifierSubstitution(t *testing.T) {
	tests := []struct {
		name           string
		ip             string
		teamIdentifier string
		expected       string
	}{
		{
			name:           "single underscore",
			ip:             "10.100.1_.2",
			teamIdentifier: "01",
			expected:       "10.100.101.2",
		},
		{
			name:           "multiple underscores",
			ip:             "10.100._._",
			teamIdentifier: "42",
			expected:       "10.100.42.42",
		},
		{
			name:           "no underscore",
			ip:             "10.100.1.2",
			teamIdentifier: "01",
			expected:       "10.100.1.2",
		},
		{
			name:           "underscore at start",
			ip:             "_.100.1.2",
			teamIdentifier: "10",
			expected:       "10.100.1.2",
		},
		{
			name:           "underscore at end",
			ip:             "10.100.1._",
			teamIdentifier: "99",
			expected:       "10.100.1.99",
		},
		{
			name:           "single digit team",
			ip:             "192.168._.1",
			teamIdentifier: "5",
			expected:       "192.168.5.1",
		},
		{
			name:           "three digit team",
			ip:             "172.16._._",
			teamIdentifier: "100",
			expected:       "172.16.100.100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strings.Replace(tt.ip, "_", tt.teamIdentifier, -1)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestDomainSubstitution tests domain name substitution
func TestDomainSubstitution(t *testing.T) {
	tests := []struct {
		name           string
		domain         string
		teamIdentifier string
		expected       string
	}{
		{
			name:           "team in subdomain",
			domain:         "team_.example.com",
			teamIdentifier: "01",
			expected:       "team01.example.com",
		},
		{
			name:           "team in middle",
			domain:         "web.team_.local",
			teamIdentifier: "42",
			expected:       "web.team42.local",
		},
		{
			name:           "multiple substitutions",
			domain:         "team_-box_.internal",
			teamIdentifier: "5",
			expected:       "team5-box5.internal",
		},
		{
			name:           "no substitution needed",
			domain:         "static.example.com",
			teamIdentifier: "01",
			expected:       "static.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strings.ReplaceAll(tt.domain, "_", tt.teamIdentifier)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestServiceValidation tests service validation logic
func TestServiceValidation(t *testing.T) {
	tests := []struct {
		name        string
		service     Service
		expectValid bool
	}{
		{
			name: "valid web service",
			service: Service{
				Name:        "web01-web",
				Display:     "Web Service",
				Port:        80,
				ServiceType: "Web",
				Target:      "10.100.1_.2",
			},
			expectValid: true,
		},
		{
			name: "valid ssh service",
			service: Service{
				Name:        "server01-ssh",
				Display:     "SSH Service",
				Port:        22,
				ServiceType: "SSH",
				Target:      "10.100.1_.2",
			},
			expectValid: true,
		},
		{
			name: "missing name",
			service: Service{
				Name:        "",
				Display:     "Web Service",
				Port:        80,
				ServiceType: "Web",
				Target:      "10.100.1_.2",
			},
			expectValid: false,
		},
		{
			name: "missing service type",
			service: Service{
				Name:        "web01-web",
				Display:     "Web Service",
				Port:        80,
				ServiceType: "",
				Target:      "10.100.1_.2",
			},
			expectValid: false,
		},
		{
			name: "missing target",
			service: Service{
				Name:        "web01-web",
				Display:     "Web Service",
				Port:        80,
				ServiceType: "Web",
				Target:      "",
			},
			expectValid: false,
		},
		{
			name: "custom check without port (valid)",
			service: Service{
				Name:        "custom01-check",
				Display:     "Custom Check",
				Port:        0, // Custom checks might not have port
				ServiceType: "Custom",
				Target:      "10.100.1_.2",
			},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := validateService(tt.service)
			assert.Equal(t, tt.expectValid, valid)
		})
	}
}

// validateService checks if a service configuration is valid
func validateService(service Service) bool {
	if service.Name == "" {
		return false
	}
	if service.ServiceType == "" {
		return false
	}
	if service.Target == "" {
		return false
	}
	return true
}

// TestResultCreation tests Result struct creation
func TestResultCreation(t *testing.T) {
	tests := []struct {
		name         string
		teamID       uint
		serviceName  string
		serviceType  string
		roundID      uint
		status       bool
		points       int
		expectedErr  bool
	}{
		{
			name:        "successful result",
			teamID:      1,
			serviceName: "web01-web",
			serviceType: "Web",
			roundID:     5,
			status:      true,
			points:      5,
			expectedErr: false,
		},
		{
			name:        "failed result",
			teamID:      2,
			serviceName: "ssh01-ssh",
			serviceType: "SSH",
			roundID:     10,
			status:      false,
			points:      0,
			expectedErr: false,
		},
		{
			name:        "zero team ID",
			teamID:      0,
			serviceName: "web01-web",
			serviceType: "Web",
			roundID:     1,
			status:      true,
			points:      5,
			expectedErr: true,
		},
		{
			name:        "empty service name",
			teamID:      1,
			serviceName: "",
			serviceType: "Web",
			roundID:     1,
			status:      true,
			points:      5,
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Result{
				TeamID:      tt.teamID,
				ServiceName: tt.serviceName,
				ServiceType: tt.serviceType,
				RoundID:     tt.roundID,
				Status:      tt.status,
				Points:      tt.points,
			}

			// Validate result
			err := validateResult(result)
			if tt.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// validateResult checks if a result is valid
func validateResult(result Result) error {
	if result.TeamID == 0 {
		return assert.AnError
	}
	if result.ServiceName == "" {
		return assert.AnError
	}
	return nil
}

// TestPointsCalculation tests points calculation for different scenarios
func TestPointsCalculation(t *testing.T) {
	tests := []struct {
		name           string
		status         bool
		basePoints     int
		slaPenalty     int
		expectedPoints int
	}{
		{
			name:           "service up - full points",
			status:         true,
			basePoints:     5,
			slaPenalty:     0,
			expectedPoints: 5,
		},
		{
			name:           "service down - zero points",
			status:         false,
			basePoints:     5,
			slaPenalty:     0,
			expectedPoints: 0,
		},
		{
			name:           "service up with SLA penalty",
			status:         true,
			basePoints:     10,
			slaPenalty:     3,
			expectedPoints: 7,
		},
		{
			name:           "service down with SLA penalty (still zero)",
			status:         false,
			basePoints:     10,
			slaPenalty:     3,
			expectedPoints: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			points := calculatePoints(tt.status, tt.basePoints, tt.slaPenalty)
			assert.Equal(t, tt.expectedPoints, points)
		})
	}
}

// calculatePoints simulates points calculation logic
func calculatePoints(status bool, basePoints int, slaPenalty int) int {
	if !status {
		return 0
	}
	points := basePoints - slaPenalty
	if points < 0 {
		points = 0
	}
	return points
}
