package www

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/dbaseqp/Quotient/engine"
	"github.com/dbaseqp/Quotient/engine/config"
	"github.com/dbaseqp/Quotient/www/api"
	"github.com/dbaseqp/Quotient/www/middleware"
)

type Router struct {
	Config *config.ConfigSettings
	Engine *engine.ScoringEngine
	api    *api.API
}

func NewRouter(conf *config.ConfigSettings, eng *engine.ScoringEngine) *Router {
	router := &Router{Config: conf, Engine: eng, api: api.NewAPI(conf, eng)}

	// Initialize OIDC if enabled
	if err := router.api.InitOIDC(); err != nil {
		slog.Error("Failed to initialize OIDC", "error", err)
	}

	return router
}

func (router *Router) Start() {
	// choose http/https
	var protocol string
	if router.Config.SslSettings == (config.SslConfig{}) {
		protocol = "http"
	} else {
		protocol = "https"
	}

	a := router.api
	mux := http.NewServeMux()
	// api routes
	/******************************************
	|                                         |
	|              PUBLIC ROUTES              |
	|                                         |
	******************************************/

	mux.Handle("/static/assets/", http.StripPrefix("/static/assets/", http.FileServer(http.Dir("./static/assets"))))

	UNAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Cors, middleware.Authentication(a, "anonymous", "team", "admin", "red", "inject"))
	// public API routes
	mux.HandleFunc("POST /api/login", a.Login)

	// OIDC routes (public)
	mux.HandleFunc("GET /auth/oidc/login", a.OIDCLoginHandler)
	mux.HandleFunc("GET /auth/oidc/callback", a.OIDCCallbackHandler)

	mux.HandleFunc("GET /api/graphs/services", UNAUTH(a.GetServiceStatus))
	mux.HandleFunc("GET /api/graphs/scores", UNAUTH(a.GetScoreStatus))
	mux.HandleFunc("GET /api/graphs/uptimes", UNAUTH(a.GetUptimeStatus))

	// public WWW routes
	mux.HandleFunc("GET /login", router.LoginPage)
	mux.HandleFunc("GET /{$}", router.HomePage)

	mux.HandleFunc("GET /graphs", UNAUTH(router.GraphPage))

	/******************************************
	|                                         |
	|               AUTH ROUTES               |
	|                                         |
	******************************************/

	ALLAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Authentication(a, "team", "admin", "red", "inject"))
	// general auth API routes
	mux.HandleFunc("GET /api/logout", ALLAUTH(a.Logout))

	mux.HandleFunc("GET /api/announcements", ALLAUTH(a.GetAnnouncements))
	mux.HandleFunc("GET /announcements/{id}/{file}", ALLAUTH(a.DownloadAnnouncementFile))

	// general auth WWW routes
	mux.HandleFunc("GET /logout", ALLAUTH(router.LogoutPage))
	mux.HandleFunc("GET /announcements", ALLAUTH(router.AnnouncementsPage))
	// mux.HandleFunc("GET /graphs", ALLAUTH(router.GraphPage))

	/******************************************
	|                                         |
	|               TEAM ROUTES               |
	|                                         |
	******************************************/

	TEAMAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Authentication(a, "team", "admin", "inject"))
	// team auth API routes
	mux.HandleFunc("GET /api/teams", TEAMAUTH(a.GetTeams))
	mux.HandleFunc("GET /api/metadata", TEAMAUTH(a.GetMetadata))
	mux.HandleFunc("GET /api/services/{team_id}", TEAMAUTH(a.GetTeamSummary))
	mux.HandleFunc("GET /api/services/{team_id}/{service_name}", TEAMAUTH(a.GetServiceAll))
	mux.HandleFunc("GET /api/injects", TEAMAUTH(a.GetInjects))
	mux.HandleFunc("POST /api/injects/{id}/submit", TEAMAUTH(a.CreateSubmission))
	mux.HandleFunc("GET /injects/{id}/submissions/{team}/{version}", TEAMAUTH(a.DownloadSubmissionFile))
	mux.HandleFunc("GET /injects/{id}/{file}", TEAMAUTH(a.DownloadInjectFile))

	mux.HandleFunc("GET /services", TEAMAUTH(router.ServicesPage))
	mux.HandleFunc("POST /api/pcrs/reset", TEAMAUTH(a.ResetPcr))
	mux.HandleFunc("GET /api/credlists", TEAMAUTH(a.GetCredlists))
	mux.HandleFunc("POST /api/pcrs/submit", TEAMAUTH(a.CreatePcr))

	// team auth WWW routes
	mux.HandleFunc("GET /injects", TEAMAUTH(router.InjectsPage))

	mux.HandleFunc("GET /pcr", TEAMAUTH(router.PcrPage))

	/******************************************
	|                                         |
	|               RED ROUTES                |
	|                                         |
	******************************************/
	REDAUTH := middleware.MiddlewareChain(middleware.Authentication(a, "red", "admin"))

	// red auth API routes
	mux.HandleFunc("GET /api/red", REDAUTH(a.GetRed))
	// mux.HandleFunc("POST /api/red/vuln", REDAUTH(api.CreatePcr))
	mux.HandleFunc("POST /api/red/box", REDAUTH(a.CreateBox))
	mux.HandleFunc("POST /api/red/vector", REDAUTH(a.CreateVector))
	mux.HandleFunc("POST /api/red/attack", REDAUTH(a.CreateAttack))

	mux.HandleFunc("POST /api/red/box/{id}", REDAUTH(a.EditBox))
	mux.HandleFunc("POST /api/red/vector/{id}", REDAUTH(a.EditVector))
	mux.HandleFunc("POST /api/red/attack/{id}", REDAUTH(a.EditAttack))

	// mux.HandleFunc("DELETE /api/red/box/{id}", REDAUTH(api.DeleteBox))
	// mux.HandleFunc("DELETE /api/red/vector/{id}", REDAUTH(api.DeleteVector))
	// mux.HandleFunc("DELETE /api/red/attack/{id}", REDAUTH(api.DeleteAttack))

	// red auth WWW routes
	mux.HandleFunc("GET /red", REDAUTH(router.RedPage))

	/******************************************
	|                                         |
	|               ADMIN ROUTES              |
	|                                         |
	******************************************/

	INJECTAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Authentication(a, "admin", "inject"))
	// admin auth API routes
	mux.HandleFunc("POST /api/announcements/create", INJECTAUTH(a.CreateAnnouncement))
	mux.HandleFunc("POST /api/announcements/{id}", INJECTAUTH(a.UpdateAnnouncement))
	mux.HandleFunc("DELETE /api/announcements/{id}", INJECTAUTH(a.DeleteAnnouncement))

	mux.HandleFunc("POST /api/injects/create", INJECTAUTH(a.CreateInject))
	mux.HandleFunc("POST /api/injects/import", INJECTAUTH(a.ImportInjects))
	mux.HandleFunc("POST /api/injects/{id}", INJECTAUTH(a.UpdateInject))
	mux.HandleFunc("DELETE /api/injects/{id}", INJECTAUTH(a.DeleteInject))
	mux.HandleFunc("GET /api/injects/{id}/submissions/download", INJECTAUTH(a.DownloadAllSubmissions))

	// router.HandleFunc("POST /api/engine/service/create", ADMINAUTH(api.CreateService))
	// router.HandleFunc("POST /api/engine/service/update", ADMINAUTH(api.UpdateService))
	// router.HandleFunc("DELETE /api/engine/service/delete", ADMINAUTH(api.DeleteService))

	ADMINAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Authentication(a, "admin"))
	mux.HandleFunc("POST /api/engine/pause", ADMINAUTH(a.PauseEngine))
	mux.HandleFunc("GET /api/engine/reset", ADMINAUTH(a.ResetScores))
	mux.HandleFunc("GET /api/engine", ADMINAUTH(a.GetEngine))
	mux.HandleFunc("GET /api/engine/tasks", ADMINAUTH(a.GetActiveTasks))
	mux.HandleFunc("POST /api/competition/start", ADMINAUTH(a.SetCompetitionStarted))
	mux.HandleFunc("POST /api/admin/teams", ADMINAUTH(a.UpdateTeams))
	mux.HandleFunc("GET /api/admin/teamchecks", ADMINAUTH(a.GetTeamChecks))
	mux.HandleFunc("POST /api/admin/teamchecks", ADMINAUTH(a.UpdateTeamChecks))

	mux.HandleFunc("GET /api/engine/export/scores", ADMINAUTH(a.ExportScores))
	mux.HandleFunc("GET /api/engine/export/config", ADMINAUTH(a.ExportConfig))

	// admin-only PCR routes
	mux.HandleFunc("GET /api/pcrs", ADMINAUTH(a.GetPcrs))
	mux.HandleFunc("GET /api/pcrs/history", ADMINAUTH(a.GetPcrHistory))

	// admin-only WWW routes (inject role excluded)
	mux.HandleFunc("GET /admin", ADMINAUTH(router.AdminPage))
	mux.HandleFunc("GET /admin/engine", ADMINAUTH(router.AdministrateEnginePage))
	mux.HandleFunc("GET /admin/runners", ADMINAUTH(router.AdministrateRunnersPage))
	mux.HandleFunc("GET /admin/teams", ADMINAUTH(router.AdministrateTeamsPage))
	mux.HandleFunc("GET /admin/appearance", ADMINAUTH(router.AdministrateAppearancePage))

	// start server with security headers middleware wrapping all routes
	securityMiddleware := middleware.SecurityHeaders(router.Config)
	server := http.Server{
		Addr:              fmt.Sprintf("%s:%d", router.Config.RequiredSettings.BindAddress, router.Config.MiscSettings.Port),
		Handler:           http.HandlerFunc(securityMiddleware(mux.ServeHTTP)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info(fmt.Sprintf("Starting Web Server on %s://%s:%d", protocol, router.Config.RequiredSettings.BindAddress, router.Config.MiscSettings.Port))

	// start server
	if router.Config.SslSettings != (config.SslConfig{}) {
		log.Fatal(server.ListenAndServeTLS(router.Config.SslSettings.HttpsCert, router.Config.SslSettings.HttpsKey))
	} else {
		log.Fatal(server.ListenAndServe())
	}
}
