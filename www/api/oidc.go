package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"quotient/engine/config"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var (
	oidcProvider *oidc.Provider
	oauth2Config *oauth2.Config
	oidcVerifier *oidc.IDTokenVerifier

	// Session storage for OIDC state and PKCE
	oidcSessions   = make(map[string]*oidcSession)
	oidcSessionsMu sync.RWMutex
)

type oidcSession struct {
	State        string
	Nonce        string
	CodeVerifier string
	CreatedAt    time.Time
}

// IDTokenClaims includes common claims from various OIDC providers
type IDTokenClaims struct {
	// Standard OIDC claims
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Nonce             string `json:"nonce"`

	// Common group claim names from various providers
	Groups          []string `json:"groups"`           // Most common
	Roles           []string `json:"roles"`            // Some providers use "roles"
	MemberOf        []string `json:"memberOf"`         // Active Directory style
	GroupMembership []string `json:"group_membership"` // Alternative naming

	// Keycloak specific
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`

	// Some providers nest groups under a resource
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
}

// InitOIDC initializes the OIDC provider and OAuth2 configuration
func InitOIDC() error {

	if !conf.OIDCSettings.OIDCEnabled {
		return nil
	}

	ctx := context.Background()

	// Initialize OIDC provider
	provider, err := oidc.NewProvider(ctx, conf.OIDCSettings.OIDCIssuerURL)
	if err != nil {
		return fmt.Errorf("failed to initialize OIDC provider: %w", err)
	}
	oidcProvider = provider

	// Configure OAuth2
	oauth2Config = &oauth2.Config{
		ClientID:     conf.OIDCSettings.OIDCClientID,
		ClientSecret: conf.OIDCSettings.OIDCClientSecret,
		RedirectURL:  conf.OIDCSettings.OIDCRedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       conf.OIDCSettings.OIDCScopes,
	}

	// Initialize ID token verifier with more options
	oidcVerifier = provider.Verifier(&oidc.Config{
		ClientID:          conf.OIDCSettings.OIDCClientID,
		SkipClientIDCheck: false,
		SkipExpiryCheck:   false,
		SkipIssuerCheck:   false,
	})

	slog.Info("OIDC provider initialized successfully",
		"issuer", conf.OIDCSettings.OIDCIssuerURL,
		"client_id", conf.OIDCSettings.OIDCClientID)

	// Clean up old sessions periodically
	go cleanupOIDCSessions()

	return nil
}

// OIDCLoginHandler initiates the OIDC authentication flow
func OIDCLoginHandler(w http.ResponseWriter, r *http.Request) {

	if !conf.OIDCSettings.OIDCEnabled {
		http.Error(w, "OIDC authentication is not enabled", http.StatusNotFound)
		return
	}

	// Generate CSRF state
	state, err := generateRandomString(32)
	if err != nil {
		slog.Error("Failed to generate state", "error", err)
		http.Error(w, "Failed to initiate authentication", http.StatusInternalServerError)
		return
	}

	// Generate nonce
	nonce, err := generateRandomString(32)
	if err != nil {
		slog.Error("Failed to generate nonce", "error", err)
		http.Error(w, "Failed to initiate authentication", http.StatusInternalServerError)
		return
	}

	// Prepare auth URL options
	authURLOpts := []oauth2.AuthCodeOption{
		oidc.Nonce(nonce),
	}

	// Generate PKCE challenge if enabled
	var codeVerifier string
	if conf.OIDCSettings.OIDCUsePKCE {
		codeVerifier, err = generateCodeVerifier()
		if err != nil {
			slog.Error("Failed to generate code verifier", "error", err)
			http.Error(w, "Failed to initiate authentication", http.StatusInternalServerError)
			return
		}

		codeChallenge := generateCodeChallenge(codeVerifier)
		authURLOpts = append(authURLOpts,
			oauth2.SetAuthURLParam("code_challenge", codeChallenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
	}

	// Store session
	session := &oidcSession{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		CreatedAt:    time.Now(),
	}
	oidcSessionsMu.Lock()
	oidcSessions[state] = session
	oidcSessionsMu.Unlock()

	// Generate authorization URL
	authURL := oauth2Config.AuthCodeURL(state, authURLOpts...)

	// Set CSRF cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   conf.SslSettings != (config.SslConfig{}) || conf.OIDCSettings.OIDCEnabled,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600, // 10 minutes
	})

	// Redirect to OIDC provider
	http.Redirect(w, r, authURL, http.StatusFound)
}

// OIDCCallbackHandler handles the OIDC provider callback
func OIDCCallbackHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !conf.OIDCSettings.OIDCEnabled {
		http.Error(w, "OIDC authentication is not enabled", http.StatusNotFound)
		return
	}

	// Verify state parameter
	state := r.URL.Query().Get("state")
	if state == "" {
		slog.Error("Missing state parameter in callback")
		http.Error(w, "Invalid authentication response", http.StatusBadRequest)
		return
	}

	// Verify state cookie
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value != state {
		slog.Error("State mismatch", "cookie_error", err)
		http.Error(w, "Invalid authentication state", http.StatusBadRequest)
		return
	}

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   conf.SslSettings != (config.SslConfig{}) || conf.OIDCSettings.OIDCEnabled,
		SameSite: http.SameSiteStrictMode,
	})

	// Retrieve session
	oidcSessionsMu.Lock()
	session, exists := oidcSessions[state]
	if !exists {
		oidcSessionsMu.Unlock()
		slog.Error("Session not found for state", "state", state)
		http.Error(w, "Invalid authentication session", http.StatusBadRequest)
		return
	}
	delete(oidcSessions, state)
	oidcSessionsMu.Unlock()

	// Check for errors from provider
	if errCode := r.URL.Query().Get("error"); errCode != "" {
		errDesc := r.URL.Query().Get("error_description")
		slog.Error("OIDC provider returned error", "error", errCode, "description", errDesc)
		http.Error(w, "Authentication failed", http.StatusBadRequest)
		return
	}

	// Get authorization code
	code := r.URL.Query().Get("code")
	if code == "" {
		slog.Error("Missing authorization code in callback")
		http.Error(w, "Invalid authentication response", http.StatusBadRequest)
		return
	}

	// Prepare token exchange options
	tokenOpts := []oauth2.AuthCodeOption{}
	if conf.OIDCSettings.OIDCUsePKCE && session.CodeVerifier != "" {
		tokenOpts = append(tokenOpts,
			oauth2.SetAuthURLParam("code_verifier", session.CodeVerifier),
		)
	}

	// Exchange code for tokens
	oauth2Token, err := oauth2Config.Exchange(ctx, code, tokenOpts...)
	if err != nil {
		slog.Error("Failed to exchange authorization code", "error", err)
		http.Error(w, "Failed to complete authentication", http.StatusInternalServerError)
		return
	}

	// Extract ID token
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		slog.Error("No ID token in response")
		http.Error(w, "Failed to complete authentication", http.StatusInternalServerError)
		return
	}

	// Verify ID token
	idToken, err := oidcVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		slog.Error("Failed to verify ID token", "error", err)
		http.Error(w, "Failed to verify authentication", http.StatusInternalServerError)
		return
	}

	// Extract claims - always try structured approach first
	var claims IDTokenClaims
	if err := idToken.Claims(&claims); err != nil {
		slog.Error("Failed to extract claims from ID token", "error", err)
		http.Error(w, "Failed to process authentication", http.StatusInternalServerError)
		return
	}

	// Verify nonce
	if claims.Nonce != session.Nonce {
		slog.Error("Nonce mismatch", "expected", session.Nonce, "got", claims.Nonce)
		http.Error(w, "Failed to verify authentication", http.StatusInternalServerError)
		return
	}

	// Extract user info from claims
	username, groups, roles := extractUserInfoFromClaims(&claims)

	if username == "" {
		slog.Error("No username found in claims")
		http.Error(w, "No username in authentication response", http.StatusInternalServerError)
		return
	}
	if len(roles) == 0 {
		slog.Error("User has no authorized roles", "username", username, "groups", groups)
		http.Error(w, "User is not authorized to access this application", http.StatusForbidden)
		return
	}

	// Use role-based session expiration instead of access token expiry
	expirySeconds := getRefreshTokenExpiry(roles)
	expiresAt := time.Now().Add(time.Duration(expirySeconds) * time.Second)

	// Store refresh token if available
	refreshToken := ""
	if oauth2Token.RefreshToken != "" {
		refreshToken = oauth2Token.RefreshToken
	}

	storeOIDCUserInfo(username, groups, roles, expiresAt, refreshToken)

	// Create session cookie with auth source
	cookieData := map[string]interface{}{
		"username":   username,
		"authSource": "oidc",
	}

	encodedCookie, err := CookieEncoder.Encode(COOKIENAME, cookieData)
	if err != nil {
		slog.Error("Failed to encode cookie", "error", err)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	cookieMaxAge := getRefreshTokenExpiry(roles)

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     COOKIENAME,
		Value:    encodedCookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   conf.SslSettings != (config.SslConfig{}) || conf.OIDCSettings.OIDCEnabled,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge,
	})

	slog.Info("OIDC authentication successful", "username", username, "roles", roles)

	// Redirect to appropriate dashboard
	redirectURL := "/announcements"
	if slices.Contains(roles, "red") {
		redirectURL = "/graphs"
	}

	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// OIDCLogoutHandler handles OIDC logout
func OIDCLogoutHandler(w http.ResponseWriter, r *http.Request) {

	// Clear session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     COOKIENAME,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   conf.SslSettings != (config.SslConfig{}),
		SameSite: http.SameSiteStrictMode,
	})

	// Check if provider supports end session endpoint
	var endSessionURL string
	if conf.OIDCSettings.OIDCEnabled && oidcProvider != nil {
		// Try to get end session endpoint from provider metadata
		var claims struct {
			EndSessionEndpoint string `json:"end_session_endpoint"`
		}
		if err := oidcProvider.Claims(&claims); err == nil && claims.EndSessionEndpoint != "" {
			endSessionURL = claims.EndSessionEndpoint
		}
	}

	if endSessionURL != "" {
		// Redirect to provider's logout endpoint
		http.Redirect(w, r, endSessionURL, http.StatusFound)
	} else {
		// Redirect to login page
		http.Redirect(w, r, "/auth", http.StatusFound)
	}
}

func mapGroupsToRoles(groups []string) []string {
	var roles []string

	// Check admin groups
	for _, adminGroup := range conf.OIDCSettings.OIDCAdminGroups {
		if matchesGroup(groups, adminGroup) {
			roles = append(roles, "admin")
			break
		}
	}

	// Check inject groups
	for _, injectGroup := range conf.OIDCSettings.OIDCInjectGroups {
		if matchesGroup(groups, injectGroup) {
			roles = append(roles, "inject")
			break
		}
	}

	// Check red groups
	for _, redGroup := range conf.OIDCSettings.OIDCRedGroups {
		if matchesGroup(groups, redGroup) {
			roles = append(roles, "red")
			break
		}
	}

	// Check team groups
	for _, teamGroup := range conf.OIDCSettings.OIDCTeamGroups {
		if matchesGroup(groups, teamGroup) {
			roles = append(roles, "team")
			break
		}
	}

	// Check inject groups
	for _, injectGroup := range conf.OIDCSettings.OIDCInjectGroups {
		if matchesGroup(groups, injectGroup) {
			roles = append(roles, "inject")
			break
		}
	}

	return roles
}

func matchesGroup(userGroups []string, configGroup string) bool {
	// Support prefix wildcards only
	if strings.HasSuffix(configGroup, "*") {
		prefix := strings.TrimSuffix(configGroup, "*")
		for _, userGroup := range userGroups {
			if strings.HasPrefix(userGroup, prefix) {
				return true
			}
		}
		return false
	}

	// Exact match
	return slices.Contains(userGroups, configGroup)
}

func getRefreshTokenExpiry(roles []string) int {
	defaultExpiry := 86400 // 24 hours in seconds

	if slices.Contains(roles, "admin") {
		expiry := conf.OIDCSettings.OIDCRefreshTokenExpiryAdmin
		if expiry == 0 {
			return defaultExpiry
		}
		return expiry
	}
	if slices.Contains(roles, "red") {
		expiry := conf.OIDCSettings.OIDCRefreshTokenExpiryRed
		if expiry == 0 {
			return defaultExpiry
		}
		return expiry
	}
	if slices.Contains(roles, "inject") {
		expiry := conf.OIDCSettings.OIDCRefreshTokenExpiryInject
		if expiry == 0 {
			return defaultExpiry
		}
		return expiry
	}
	// Default for team users
	expiry := conf.OIDCSettings.OIDCRefreshTokenExpiryTeam
	if expiry == 0 {
		return defaultExpiry
	}
	return expiry
}

// OIDC user session storage
var (
	oidcUsers   = make(map[string]*OidcUserInfo)
	oidcUsersMu sync.RWMutex
)

type OidcUserInfo struct {
	Username     string
	Groups       []string
	Roles        []string
	ExpiresAt    time.Time
	RefreshToken string
}

func storeOIDCUserInfo(username string, groups []string, roles []string, expiresAt time.Time, refreshToken string) {
	oidcUsersMu.Lock()
	oidcUsers[username] = &OidcUserInfo{
		Username:     username,
		Groups:       groups,
		Roles:        roles,
		ExpiresAt:    expiresAt,
		RefreshToken: refreshToken,
	}
	oidcUsersMu.Unlock()
	slog.Debug("Stored OIDC user session", "username", username, "expires_at", expiresAt.Format(time.RFC3339), "expires_in_hours", time.Until(expiresAt).Hours())
}

func GetOIDCUserInfo(username string) (*OidcUserInfo, bool) {
	oidcUsersMu.RLock()
	info, exists := oidcUsers[username]
	oidcUsersMu.RUnlock()

	if !exists {
		slog.Debug("OIDC user not found in cache", "username", username)
		return nil, false
	}

	// Check if session has expired
	if time.Now().After(info.ExpiresAt) {
		slog.Info("OIDC session expired", "username", username, "expired_at", info.ExpiresAt.Format(time.RFC3339), "now", time.Now().Format(time.RFC3339))
		// Remove expired session
		oidcUsersMu.Lock()
		delete(oidcUsers, username)
		oidcUsersMu.Unlock()
		return nil, false
	}

	return info, exists
}

// extractUserInfoFromClaims extracts username, groups, and roles from OIDC claims
func extractUserInfoFromClaims(claims *IDTokenClaims) (username string, groups []string, roles []string) {
	// Extract username
	username = claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	if username == "" {
		username = claims.Subject
	}

	// Extract groups based on configured claim
	groupClaim := conf.OIDCSettings.OIDCGroupClaim
	if groupClaim == "" {
		groupClaim = "groups"
	}

	switch groupClaim {
	case "groups":
		groups = claims.Groups
	case "roles":
		groups = claims.Roles
	case "realm_access.roles", "resource_access.roles":
		groups = claims.RealmAccess.Roles
	default:
		// Unsupported group claim location
		slog.Warn("Unsupported OIDCGroupClaim value", "claim", groupClaim)
		groups = []string{}
	}

	// Map groups to roles
	roles = mapGroupsToRoles(groups)

	return username, groups, roles
}

// FetchUserInfoFromProvider fetches user info from the OIDC provider using an access token
func FetchUserInfoFromProvider(accessToken string) (*OidcUserInfo, error) {
	if oidcProvider == nil {
		return nil, errors.New("OIDC provider not initialized")
	}

	ctx := context.Background()

	// Call the UserInfo endpoint
	userInfo, err := oidcProvider.UserInfo(ctx, oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: accessToken,
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}

	// Parse claims
	var claims IDTokenClaims
	if err := userInfo.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to parse claims: %w", err)
	}

	// Extract user info using common function
	username, groups, roles := extractUserInfoFromClaims(&claims)

	// Store in cache for future use with default expiration
	expirySeconds := getRefreshTokenExpiry(roles)
	expiresAt := time.Now().Add(time.Duration(expirySeconds) * time.Second)
	storeOIDCUserInfo(username, groups, roles, expiresAt, "")

	return &OidcUserInfo{
		Username:  username,
		Groups:    groups,
		Roles:     roles,
		ExpiresAt: expiresAt,
	}, nil
}

// ValidateOIDCToken validates an OIDC token (used for API authentication)
func ValidateOIDCToken(token string) (map[string]interface{}, error) {
	ctx := context.Background()

	// Verify the token
	idToken, err := oidcVerifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("failed to verify token: %w", err)
	}

	// Extract claims using the same approach as callback
	var claims IDTokenClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to extract claims: %w", err)
	}

	// Get username with fallback priority
	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	if username == "" {
		username = claims.Name
	}
	if username == "" {
		username = claims.Subject
	}

	if username == "" {
		return nil, errors.New("no username found in token")
	}

	// Extract groups using same logic as callback
	var groups []string
	groupClaim := conf.OIDCSettings.OIDCGroupClaim
	if groupClaim == "" {
		groupClaim = "groups"
	}

	switch groupClaim {
	case "groups":
		groups = claims.Groups
	case "roles":
		groups = claims.Roles
	case "realm_access.roles":
		groups = claims.RealmAccess.Roles
	default:
		// Unsupported group claim location
		slog.Warn("Unsupported OIDCGroupClaim value in ValidateOIDCToken", "claim", groupClaim)
		groups = []string{}
	}

	roles := mapGroupsToRoles(groups)
	if len(roles) == 0 {
		return nil, errors.New("user has no authorized roles")
	}

	return map[string]interface{}{
		"username":   username,
		"groups":     groups,
		"roles":      roles,
		"authSource": "oidc",
	}, nil
}

// PKCE helpers
func generateCodeVerifier() (string, error) {
	// Generate 32 bytes of random data (will be 43 chars when base64url encoded)
	verifier := make([]byte, 32)
	if _, err := rand.Read(verifier); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(verifier), nil
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// Security helpers
func generateRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// cleanupOIDCSessions removes expired OIDC sessions
func cleanupOIDCSessions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		oidcSessionsMu.Lock()
		for state, session := range oidcSessions {
			if now.Sub(session.CreatedAt) > 10*time.Minute {
				delete(oidcSessions, state)
			}
		}
		oidcSessionsMu.Unlock()
	}
}
