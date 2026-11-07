package api

import (
	"errors"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"slices"
	"sync"

	"github.com/dbaseqp/Quotient/engine/db"
	"github.com/dbaseqp/Quotient/www/timelapse"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// ExportTimelapse writes an animated GIF of the active teams' service status,
// one frame per round.
func (a *API) ExportTimelapse(w http.ResponseWriter, r *http.Request) {
	teams, err := a.eng.DB.GetTeams()
	if err != nil {
		WriteInternalError(w, r, "Failed to retrieve teams", err)
		return
	}
	teams = slices.DeleteFunc(teams, func(team db.TeamSchema) bool { return !team.Active })
	compare := labelOrder()
	slices.SortFunc(teams, func(x, y db.TeamSchema) int { return compare(x.Name, y.Name) })

	teamIDs := make([]uint, len(teams))
	teamNames := make([]string, len(teams))
	rowOf := make(map[uint]int, len(teams))
	for i, team := range teams {
		teamIDs[i] = team.ID
		teamNames[i] = team.Name
		rowOf[team.ID] = i
	}

	results, err := a.eng.DB.GetCheckResults(teamIDs)
	if err != nil {
		WriteInternalError(w, r, "Failed to retrieve check results", err)
		return
	}
	var services []string
	for _, res := range results {
		if !slices.Contains(services, res.ServiceName) {
			services = append(services, res.ServiceName)
		}
	}
	slices.SortFunc(services, compare)
	colOf := make(map[string]int, len(services))
	for i, svc := range services {
		colOf[svc] = i
	}

	b := timelapse.NewBuilder(teamNames, services)
	for _, res := range results {
		b.Set(res.RoundID, rowOf[res.TeamID], colOf[res.ServiceName], res.Result)
	}

	icons, err := statusIcons()
	if err != nil {
		WriteInternalError(w, r, "Failed to load status icons", err)
		return
	}
	tl, err := b.Build(icons)
	if errors.Is(err, timelapse.ErrNoRounds) {
		WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "No check results for active teams"})
		return
	}
	if err != nil {
		WriteInternalError(w, r, "Failed to lay out timelapse", err)
		return
	}

	w.Header().Set("Content-Type", "image/gif")
	if err := tl.Encode(w); err != nil {
		slog.Error("failed to write timelapse", "request_id", r.Context().Value("request_id"), "error", err)
	}
}

// statusIcons loads the images the /graphs status heatmap draws, once.
var statusIcons = sync.OnceValues(func() (timelapse.Icons, error) {
	load := func(name string) (image.Image, error) {
		f, err := SafeOpen("static/assets/services", name)
		if err != nil {
			return nil, err
		}
		// nolint:errcheck
		defer f.Close()
		return png.Decode(f)
	}
	var icons timelapse.Icons
	var err error
	if icons.Up, err = load("up.png"); err != nil {
		return icons, err
	}
	if icons.Down, err = load("down.png"); err != nil {
		return icons, err
	}
	icons.Unknown, err = load("pending.png")
	return icons, err
})

// labelOrder compares names the way the /graphs page sorts them with
// localeCompare. A collator is not safe for concurrent use, so each export
// makes its own.
func labelOrder() func(a, b string) int {
	return collate.New(language.English).CompareString
}
