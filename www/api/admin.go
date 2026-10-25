package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validIdentifierRegex = regexp.MustCompile(`^[0-9]{1,3}$`)

func isValidIdentifier(identifier string) bool {
	return validIdentifierRegex.MatchString(identifier)
}

func (a *API) PauseEngine(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Pause bool `json:"pause"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		return
	}

	slog.Debug("pause engine requested", "wasEnginePausedWhenRequestIssued", a.eng.IsEnginePaused, "setPauseTo", form.Pause)
	if a.eng.IsEnginePaused && !form.Pause {
		a.eng.ResumeEngine()
	} else if !a.eng.IsEnginePaused && form.Pause {
		a.eng.PauseEngine()
	} else {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid engine state transition"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (a *API) ResetScores(w http.ResponseWriter, r *http.Request) {
	slog.Debug("reset scores requested")
	if err := a.eng.ResetScores(); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to reset scores"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (a *API) SetCompetitionStarted(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Started bool `json:"started"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		return
	}

	slog.Info("competition started toggle requested", "started", form.Started)
	if err := a.eng.DB.SetCompetitionStarted(form.Started); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update competition status"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

func (a *API) ExportScores(w http.ResponseWriter, r *http.Request) {
	type ServiceScore struct {
		ServiceName   string `json:"service_name"`
		Points        int    `json:"service_points"`
		SlaViolations int    `json:"sla_violations"`
		SlaPenalty    int    `json:"sla_penalty"`
	}

	type TeamScore struct {
		TeamID             uint           `json:"team_id"`
		TeamName           string         `json:"team_name"`
		Services           []ServiceScore `json:"services"`
		TotalPoints        int            `json:"total_points"`
		GrossPoints        int            `json:"gross_points"`
		TotalSLAPenalty    int            `json:"total_sla_penalty"`
		TotalSLAViolations int            `json:"total_sla_violations"`
	}

	teams, err := a.eng.DB.GetTeams()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to retrieve teams"})
		return
	}

	teamScores := make(map[uint]*TeamScore)
	for _, team := range teams {
		teamScores[team.ID] = &TeamScore{
			TeamID:   team.ID,
			TeamName: team.Name,
			Services: []ServiceScore{},
		}
	}

	serviceData, err := a.eng.DB.GetServiceScores()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to retrieve service scores"})
		return
	}

	for _, sd := range serviceData {
		if team, ok := teamScores[sd.TeamID]; ok {
			service := ServiceScore{
				ServiceName:   sd.ServiceName,
				Points:        sd.Points,
				SlaViolations: sd.Violations,
				SlaPenalty:    sd.TotalPenalty,
			}
			team.Services = append(team.Services, service)
			team.TotalPoints += sd.Points - sd.TotalPenalty
			team.GrossPoints += sd.Points
			team.TotalSLAPenalty += sd.TotalPenalty
			team.TotalSLAViolations += sd.Violations
		}
	}

	var data []TeamScore
	for _, score := range teamScores {
		data = append(data, *score)
	}

	WriteJSON(w, http.StatusOK, data)
}

func (a *API) ExportConfig(w http.ResponseWriter, r *http.Request) {

}

func (a *API) GetActiveTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := a.eng.GetActiveTasks()
	if err != nil {
		slog.Error("failed to get active tasks", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to retrieve active tasks"})
		return
	}

	WriteJSON(w, http.StatusOK, tasks)
}

func (a *API) GetEngine(w http.ResponseWriter, r *http.Request) {
	lastRound, err := a.eng.DB.GetLastRound()
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to retrieve engine status"})
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"last_round":          lastRound,
		"current_round_time":  a.eng.CurrentRoundStartTime,
		"next_round_time":     a.eng.NextRoundStartTime,
		"running":             !a.eng.IsEnginePaused,
		"competition_started": a.eng.DB.GetCompetitionStarted(),
	})
}

func (a *API) UpdateTeams(w http.ResponseWriter, r *http.Request) {
	type Form struct {
		Teams []struct {
			TeamID     uint   `json:"id"`
			Identifier string `json:"identifier"`
			Active     bool   `json:"active"`
		} `json:"teams"`
	}

	var form Form
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid request body"})
		return
	}

	for _, team := range form.Teams {
		// Validate identifier format to prevent command injection
		if !isValidIdentifier(team.Identifier) {
			WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid identifier format. Only alphanumeric, hyphens, and underscores allowed."})
			return
		}

		if err := a.eng.DB.UpdateTeam(uint(team.TeamID), team.Identifier, team.Active); err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Failed to update team"})
			return
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

const maxScriptBytes = 256 * 1024

var pythonImportRegex = regexp.MustCompile(`(?m)^\s*(?:from\s+\.+\s+import|import|from)\s+\.*([a-zA-Z0-9_]+)`)

type scriptHelper struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type customCheckInfo struct {
	Name          string         `json:"name"`
	BoxName       string         `json:"box_name"`
	Target        string         `json:"target"`
	Command       string         `json:"command"`
	Regex         string         `json:"regex"`
	CredLists     []string       `json:"credlists"`
	Points        int            `json:"points"`
	Timeout       int            `json:"timeout"`
	ScriptPath    string         `json:"script_path,omitempty"`
	ScriptContent string         `json:"script_content,omitempty"`
	ScriptError   string         `json:"script_error,omitempty"`
	Helpers       []scriptHelper `json:"helpers,omitempty"`
}

func resolveChecksRoot() string {
	if info, err := os.Stat("/app/checks"); err == nil && info.IsDir() {
		return "/app/checks"
	}
	return "./custom-checks"
}

func extractScriptRelPath(command string) (string, string) {
	prefixes := []string{"/app/checks/", "./custom-checks/", "custom-checks/"}
	for _, rawToken := range strings.Fields(command) {
		candidates := []string{rawToken}
		if _, after, ok := strings.Cut(rawToken, "="); ok && after != "" {
			candidates = append(candidates, after)
		}
		for _, cand := range candidates {
			tok := strings.Trim(cand, "\"'();&|")
			for _, prefix := range prefixes {
				if rel, ok := strings.CutPrefix(tok, prefix); ok && rel != "" {
					return tok, rel
				}
			}
		}
	}
	return "", ""
}

func readSafeScriptFile(baseDir, rel string) (string, error) {
	f, err := SafeOpen(baseDir, rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if info, statErr := f.Stat(); statErr == nil && info.IsDir() {
		return "", errors.New("script path is a directory")
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxScriptBytes+1))
	if readErr != nil {
		return "", readErr
	}
	if len(data) > maxScriptBytes {
		return string(data[:maxScriptBytes]) + "\n\n# [Truncated at 256 KiB limit]", nil
	}
	return string(data), nil
}

func readCustomCheckScript(baseDir, command string) (string, string, string) {
	scriptPath, rel := extractScriptRelPath(command)
	if rel == "" {
		return "", "", ""
	}
	content, err := readSafeScriptFile(baseDir, rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return scriptPath, "", "Script file not found in " + baseDir
		}
		return scriptPath, "", err.Error()
	}
	return scriptPath, content, ""
}

func readImportedHelpers(baseDir, scriptPath, scriptContent string) []scriptHelper {
	_, rel := extractScriptRelPath(scriptPath)
	if rel == "" || scriptContent == "" {
		return nil
	}
	relDir := filepath.Dir(rel)
	displayDir := filepath.ToSlash(filepath.Dir(scriptPath))
	selfBase := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	seen := map[string]bool{selfBase: true}
	var helpers []scriptHelper

	for _, match := range pythonImportRegex.FindAllStringSubmatch(scriptContent, -1) {
		mod := match[1]
		if seen[mod] {
			continue
		}
		seen[mod] = true
		helperRel := filepath.ToSlash(filepath.Join(relDir, mod+".py"))
		content, err := readSafeScriptFile(baseDir, helperRel)
		if err != nil {
			continue
		}
		helpers = append(helpers, scriptHelper{
			Path:    filepath.ToSlash(filepath.Join(displayDir, mod+".py")),
			Content: content,
		})
	}
	return helpers
}

func (a *API) GetCustomChecks(w http.ResponseWriter, r *http.Request) {
	serviceFilter := r.URL.Query().Get("service")
	baseDir := resolveChecksRoot()
	checks := make([]customCheckInfo, 0)

	credLabelsByPath := make(map[string]string, len(a.conf.CredlistSettings.Credlist))
	for _, cl := range a.conf.CredlistSettings.Credlist {
		if cl.CredlistPath != "" && cl.CredlistName != "" && cl.CredlistName != cl.CredlistPath {
			credLabelsByPath[cl.CredlistPath] = cl.CredlistName + " (" + cl.CredlistPath + ")"
		}
	}

	for _, box := range a.conf.Box {
		for _, c := range box.Custom {
			if serviceFilter != "" && c.Name != serviceFilter {
				continue
			}
			scriptPath, scriptContent, scriptErr := readCustomCheckScript(baseDir, c.Command)
			helpers := readImportedHelpers(baseDir, scriptPath, scriptContent)
			target := c.Target
			if target == "" {
				target = box.IP
			}
			var credlists []string
			if len(c.CredLists) > 0 {
				credlists = make([]string, len(c.CredLists))
				for i, entry := range c.CredLists {
					if label, ok := credLabelsByPath[entry]; ok {
						credlists[i] = label
					} else {
						credlists[i] = entry
					}
				}
			}
			checks = append(checks, customCheckInfo{
				Name:          c.Name,
				BoxName:       box.Name,
				Target:        target,
				Command:       c.Command,
				Regex:         c.Regex,
				CredLists:     credlists,
				Points:        c.Points,
				Timeout:       c.Timeout,
				ScriptPath:    scriptPath,
				ScriptContent: scriptContent,
				ScriptError:   scriptErr,
				Helpers:       helpers,
			})
		}
	}

	WriteJSON(w, http.StatusOK, checks)
}
