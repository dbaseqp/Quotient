package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type oidcState struct {
	oidcProvider *oidc.Provider
	oauth2Config *oauth2.Config
	oidcVerifier *oidc.IDTokenVerifier

	// Session storage for OIDC state and PKCE
	oidcSessions   map[string]*oidcSession
	oidcSessionsMu sync.RWMutex
}

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
func (a *API) InitOIDC() error {
	if !a.conf.OIDCSettings.OIDCEnabled {
		return nil
	}

	ctx := context.Background()

	// Initialize OIDC provider
	provider, err := oidc.NewProvider(ctx, a.conf.OIDCSettings.OIDCIssuerURL)
	if err != nil {
		return fmt.Errorf("failed to initialize OIDC provider: %w", err)
	}

	// Configure OAuth2
	oauth2Config := &oauth2.Config{
		ClientID:     a.conf.OIDCSettings.OIDCClientID,
		ClientSecret: a.conf.OIDCSettings.OIDCClientSecret,
		RedirectURL:  a.conf.OIDCSettings.OIDCRedirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       a.conf.OIDCSettings.OIDCScopes,
	}

	// Initialize ID token verifier with more options
	oidcVerifier := provider.Verifier(&oidc.Config{
		ClientID:          a.conf.OIDCSettings.OIDCClientID,
		SkipClientIDCheck: false,
		SkipExpiryCheck:   false,
		SkipIssuerCheck:   false,
	})

	a.oidc = &oidcState{
		oidcProvider: provider,
		oauth2Config: oauth2Config,
		oidcVerifier: oidcVerifier,
		oidcSessions: make(map[string]*oidcSession),
	}

	slog.Info("OIDC provider initialized successfully",
		"issuer", a.conf.OIDCSettings.OIDCIssuerURL,
		"client_id", a.conf.OIDCSettings.OIDCClientID)

	// Clean up old sessions periodically
	go a.oidc.cleanupOIDCSessions()

	return nil
}

// OIDCLoginHandler initiates the OIDC authentication flow
func (a *API) OIDCLoginHandler(w http.ResponseWriter, r *http.Request) {
	if !a.conf.OIDCSettings.OIDCEnabled {
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

	// Generate PKCE challenge
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		slog.Error("Failed to generate code verifier", "error", err)
		http.Error(w, "Failed to initiate authentication", http.StatusInternalServerError)
		return
	}

	codeChallenge := generateCodeChallenge(codeVerifier)

	// Prepare auth URL options
	authURLOpts := []oauth2.AuthCodeOption{
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}

	// Store session
	session := &oidcSession{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		CreatedAt:    time.Now(),
	}
	a.oidc.oidcSessionsMu.Lock()
	a.oidc.oidcSessions[state] = session
	a.oidc.oidcSessionsMu.Unlock()

	// Generate authorization URL
	authURL := a.oidc.oauth2Config.AuthCodeURL(state, authURLOpts...)
	slog.Info("OIDC login initiated", "redirect_uri", a.oidc.oauth2Config.RedirectURL, "auth_url", authURL)

	// Set CSRF cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cookieSecure(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600, // 10 minutes
	})

	// Redirect to OIDC provider
	http.Redirect(w, r, authURL, http.StatusFound)
}

// OIDCCallbackHandler handles the OIDC provider callback
func (a *API) OIDCCallbackHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !a.conf.OIDCSettings.OIDCEnabled {
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
		Secure:   a.cookieSecure(),
		SameSite: http.SameSiteLaxMode,
	})

	// Retrieve session
	a.oidc.oidcSessionsMu.Lock()
	session, exists := a.oidc.oidcSessions[state]
	if !exists {
		a.oidc.oidcSessionsMu.Unlock()
		slog.Error("Session not found for state", "state", state)
		http.Error(w, "Invalid authentication session", http.StatusBadRequest)
		return
	}
	delete(a.oidc.oidcSessions, state)
	a.oidc.oidcSessionsMu.Unlock()

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

	// Exchange code for tokens with PKCE verifier
	oauth2Token, err := a.oidc.oauth2Config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", session.CodeVerifier),
	)
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
	idToken, err := a.oidc.oidcVerifier.Verify(ctx, rawIDToken)
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
	username, groups, roles := a.extractUserInfoFromClaims(&claims)

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
	expirySeconds := a.getRefreshTokenExpiry(roles)
	expiresAt := time.Now().Add(time.Duration(expirySeconds) * time.Second)

	// Store refresh token if available
	refreshToken := ""
	if oauth2Token.RefreshToken != "" {
		refreshToken = oauth2Token.RefreshToken
	}

	a.storeOIDCUserInfo(username, groups, roles, expiresAt, refreshToken)

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

	cookieMaxAge := a.getRefreshTokenExpiry(roles)

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     COOKIENAME,
		Value:    encodedCookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cookieSecure(),
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
func (a *API) OIDCLogoutHandler(w http.ResponseWriter, r *http.Request) {

	// Clear session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     COOKIENAME,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cookieSecure(),
		SameSite: http.SameSiteStrictMode,
	})

	// Check if provider supports end session endpoint
	var endSessionURL string
	if a.conf.OIDCSettings.OIDCEnabled && a.oidc.oidcProvider != nil {
		// Try to get end session endpoint from provider metadata
		var claims struct {
			EndSessionEndpoint string `json:"end_session_endpoint"`
		}
		if err := a.oidc.oidcProvider.Claims(&claims); err == nil && claims.EndSessionEndpoint != "" {
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

func (a *API) mapGroupsToRoles(groups []string) []string {
	var roles []string

	// Check admin groups
	for _, adminGroup := range a.conf.OIDCSettings.OIDCAdminGroups {
		if matchesGroup(groups, adminGroup) {
			roles = append(roles, "admin")
			break
		}
	}

	// Check inject groups
	for _, injectGroup := range a.conf.OIDCSettings.OIDCInjectGroups {
		if matchesGroup(groups, injectGroup) {
			roles = append(roles, "inject")
			break
		}
	}

	// Check red groups
	for _, redGroup := range a.conf.OIDCSettings.OIDCRedGroups {
		if matchesGroup(groups, redGroup) {
			roles = append(roles, "red")
			break
		}
	}

	// Check team groups
	for _, teamGroup := range a.conf.OIDCSettings.OIDCTeamGroups {
		if matchesGroup(groups, teamGroup) {
			roles = append(roles, "team")
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

func (a *API) getRefreshTokenExpiry(roles []string) int {
	defaultExpiry := 86400 // 24 hours in seconds

	if slices.Contains(roles, "admin") {
		expiry := a.conf.OIDCSettings.OIDCRefreshTokenExpiryAdmin
		if expiry == 0 {
			return defaultExpiry
		}
		return expiry
	}
	if slices.Contains(roles, "red") {
		expiry := a.conf.OIDCSettings.OIDCRefreshTokenExpiryRed
		if expiry == 0 {
			return defaultExpiry
		}
		return expiry
	}
	if slices.Contains(roles, "inject") {
		expiry := a.conf.OIDCSettings.OIDCRefreshTokenExpiryInject
		if expiry == 0 {
			return defaultExpiry
		}
		return expiry
	}
	// Default for team users
	expiry := a.conf.OIDCSettings.OIDCRefreshTokenExpiryTeam
	if expiry == 0 {
		return defaultExpiry
	}
	return expiry
}

// OIDC user session storage (stored in Redis)
type OidcUserInfo struct {
	Username     string
	Groups       []string
	Roles        []string
	ExpiresAt    time.Time
	RefreshToken string
}

func (a *API) storeOIDCUserInfo(username string, groups []string, roles []string, expiresAt time.Time, refreshToken string) {
	userInfo := &OidcUserInfo{
		Username:     username,
		Groups:       groups,
		Roles:        roles,
		ExpiresAt:    expiresAt,
		RefreshToken: refreshToken,
	}

	// Serialize to JSON
	data, err := json.Marshal(userInfo)
	if err != nil {
		slog.Error("Failed to marshal OIDC user info", "username", username, "error", err)
		return
	}

	// Store in Redis with TTL
	ctx := context.Background()
	key := fmt.Sprintf("oidc:session:%s", username)
	ttl := time.Until(expiresAt)

	if a.eng != nil && a.eng.RedisClient != nil {
		err = a.eng.RedisClient.Set(ctx, key, data, ttl).Err()
		if err != nil {
			slog.Error("Failed to store OIDC session in Redis", "username", username, "error", err)
			return
		}
		slog.Debug("Stored OIDC user session in Redis", "username", username, "expires_at", expiresAt.Format(time.RFC3339), "expires_in_hours", time.Until(expiresAt).Hours())
	} else {
		slog.Warn("Redis client not available, OIDC session not persisted", "username", username)
	}
}

func (a *API) GetOIDCUserInfo(username string) (*OidcUserInfo, bool) {
	// Try to get from Redis
	if a.eng == nil || a.eng.RedisClient == nil {
		slog.Warn("Redis client not available, cannot retrieve OIDC session", "username", username)
		return nil, false
	}

	ctx := context.Background()
	key := fmt.Sprintf("oidc:session:%s", username)

	// Attempt to retrieve from Redis
	data, err := a.eng.RedisClient.Get(ctx, key).Result()
	if err != nil {
		if err.Error() == "redis: nil" {
			slog.Debug("OIDC user not found in Redis", "username", username)
		} else {
			slog.Error("Failed to retrieve OIDC session from Redis", "username", username, "error", err)
		}

		// Try to refresh the session if we had a cached refresh token
		// This would require storing refresh tokens separately, which we'll skip for now
		return nil, false
	}

	// Deserialize from JSON
	var info OidcUserInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		slog.Error("Failed to unmarshal OIDC user info", "username", username, "error", err)
		// Delete corrupted data
		a.eng.RedisClient.Del(ctx, key)
		return nil, false
	}

	// Check if session has expired (Redis TTL should handle this, but double-check)
	if time.Now().After(info.ExpiresAt) {
		slog.Info("OIDC session expired", "username", username, "expired_at", info.ExpiresAt.Format(time.RFC3339))
		a.eng.RedisClient.Del(ctx, key)

		// Try to refresh using refresh token if available
		if info.RefreshToken != "" {
			if refreshed := a.tryRefreshOIDCSession(username, &info); refreshed {
				return a.GetOIDCUserInfo(username) // Recursive call to get refreshed session
			}
		}

		return nil, false
	}

	return &info, true
}

// tryRefreshOIDCSession attempts to refresh an OIDC session using the refresh token
func (a *API) tryRefreshOIDCSession(username string, oldInfo *OidcUserInfo) bool {
	if a.oidc == nil || oldInfo.RefreshToken == "" {
		return false
	}

	ctx := context.Background()

	// Create a token with the refresh token
	token := &oauth2.Token{
		RefreshToken: oldInfo.RefreshToken,
	}

	// Use the token source to get a fresh token
	tokenSource := a.oidc.oauth2Config.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		slog.Warn("Failed to refresh OIDC token", "username", username, "error", err)
		return false
	}

	// Extract and verify the new ID token
	rawIDToken, ok := newToken.Extra("id_token").(string)
	if !ok {
		slog.Warn("No ID token in refreshed response", "username", username)
		return false
	}

	idToken, err := a.oidc.oidcVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		slog.Warn("Failed to verify refreshed ID token", "username", username, "error", err)
		return false
	}

	// Extract claims
	var claims IDTokenClaims
	if err := idToken.Claims(&claims); err != nil {
		slog.Warn("Failed to extract claims from refreshed token", "username", username, "error", err)
		return false
	}

	// Extract user info from new claims
	newUsername, groups, roles := a.extractUserInfoFromClaims(&claims)
	if newUsername != username {
		slog.Error("Username mismatch after refresh", "expected", username, "got", newUsername)
		return false
	}

	// Calculate new expiration
	expirySeconds := a.getRefreshTokenExpiry(roles)
	expiresAt := time.Now().Add(time.Duration(expirySeconds) * time.Second)

	// Store the refreshed session
	newRefreshToken := newToken.RefreshToken
	if newRefreshToken == "" {
		newRefreshToken = oldInfo.RefreshToken // Reuse old refresh token if new one not provided
	}

	a.storeOIDCUserInfo(username, groups, roles, expiresAt, newRefreshToken)
	slog.Info("Successfully refreshed OIDC session", "username", username, "new_expiry", expiresAt.Format(time.RFC3339))

	return true
}

// extractUserInfoFromClaims extracts username, groups, and roles from OIDC claims
func (a *API) extractUserInfoFromClaims(claims *IDTokenClaims) (username string, groups []string, roles []string) {
	// Extract username
	username = claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	if username == "" {
		username = claims.Subject
	}

	// Extract groups based on configured claim
	groupClaim := a.conf.OIDCSettings.OIDCGroupClaim
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
	roles = a.mapGroupsToRoles(groups)

	return username, groups, roles
}

// FetchUserInfoFromProvider fetches user info from the OIDC provider using an access token
func (a *API) FetchUserInfoFromProvider(accessToken string) (*OidcUserInfo, error) {
	if a.oidc == nil {
		return nil, errors.New("OIDC state not initialized")
	}

	ctx := context.Background()

	// Call the UserInfo endpoint
	userInfo, err := a.oidc.oidcProvider.UserInfo(ctx, oauth2.StaticTokenSource(&oauth2.Token{
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
	username, groups, roles := a.extractUserInfoFromClaims(&claims)

	// Store in cache for future use with default expiration
	expirySeconds := a.getRefreshTokenExpiry(roles)
	expiresAt := time.Now().Add(time.Duration(expirySeconds) * time.Second)
	a.storeOIDCUserInfo(username, groups, roles, expiresAt, "")

	return &OidcUserInfo{
		Username:  username,
		Groups:    groups,
		Roles:     roles,
		ExpiresAt: expiresAt,
	}, nil
}

// ValidateOIDCToken validates an OIDC token (used for API authentication)
func (a *API) ValidateOIDCToken(token string) (map[string]interface{}, error) {
	if a.oidc == nil {
		return nil, errors.New("OIDC state not initialized")
	}

	ctx := context.Background()

	// Verify the token
	idToken, err := a.oidc.oidcVerifier.Verify(ctx, token)
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
	groupClaim := a.conf.OIDCSettings.OIDCGroupClaim
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

	roles := a.mapGroupsToRoles(groups)
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
func (o *oidcState) cleanupOIDCSessions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		o.oidcSessionsMu.Lock()
		for state, session := range o.oidcSessions {
			if now.Sub(session.CreatedAt) > 10*time.Minute {
				delete(o.oidcSessions, state)
			}
		}
		o.oidcSessionsMu.Unlock()
	}
}
