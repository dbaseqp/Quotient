package www

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"

	"quotient/engine"
	"quotient/engine/config"
	"quotient/www/api"
	"quotient/www/middleware"
)

type Router struct {
	Config *config.ConfigSettings
	Engine *engine.ScoringEngine
}

func (router *Router) Start() {
	// choose http/https
	var protocol string
	if router.Config.SslSettings == (config.SslConfig{}) {
		protocol = "http"
	} else {
		protocol = "https"
	}

	mux := http.NewServeMux()
	api.SetConfig(router.Config)
	api.SetEngine(router.Engine)

	// api routes
	/******************************************
	|                                         |
	|              PUBLIC ROUTES              |
	|                                         |
	******************************************/

	mux.Handle("/static/assets/", http.StripPrefix("/static/assets/", http.FileServer(http.Dir("./static/assets"))))

	UNAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Cors, middleware.Authentication("anonymous", "team", "admin", "red"))
	// public API routes
	mux.HandleFunc("POST /api/login", api.Login)

	mux.HandleFunc("GET /api/graphs/services", UNAUTH(api.GetServiceStatus))
	mux.HandleFunc("GET /api/graphs/scores", UNAUTH(api.GetScoreStatus))
	mux.HandleFunc("GET /api/graphs/uptimes", UNAUTH(api.GetUptimeStatus))

	// public WWW routes
	mux.HandleFunc("GET /login", router.LoginPage)
	mux.HandleFunc("GET /{$}", router.HomePage)

	mux.HandleFunc("GET /graphs", UNAUTH(router.GraphPage))

	/******************************************
	|                                         |
	|               AUTH ROUTES               |
	|                                         |
	******************************************/

	ALLAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Authentication("team", "admin", "red"))
	// general auth API routes
	mux.HandleFunc("GET /api/logout", ALLAUTH(api.Logout))

	mux.HandleFunc("GET /api/announcements", ALLAUTH(api.GetAnnouncements))
	mux.HandleFunc("GET /announcements/{id}/{file}", ALLAUTH(api.DownloadAnnouncementFile))

	// general auth WWW routes
	mux.HandleFunc("GET /logout", ALLAUTH(router.LogoutPage))
	mux.HandleFunc("GET /announcements", ALLAUTH(router.AnnouncementsPage))
	// mux.HandleFunc("GET /graphs", ALLAUTH(router.GraphPage))

	/******************************************
	|                                         |
	|               TEAM ROUTES               |
	|                                         |
	******************************************/

	TEAMAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Authentication("team", "admin"))
	// team auth API routes
	mux.HandleFunc("GET /api/teams", TEAMAUTH(api.GetTeams))
	mux.HandleFunc("GET /api/services/{team_id}", TEAMAUTH(api.GetTeamSummary))
	mux.HandleFunc("GET /api/services/{team_id}/{service_name}", TEAMAUTH(api.GetServiceAll))
	mux.HandleFunc("GET /api/injects", TEAMAUTH(api.GetInjects))
	mux.HandleFunc("POST /api/injects/{id}/submit", TEAMAUTH(api.CreateSubmission))
	mux.HandleFunc("GET /injects/{id}/submissions/{team}/{version}", TEAMAUTH(api.DownloadSubmissionFile))
	mux.HandleFunc("GET /injects/{id}/{file}", TEAMAUTH(api.DownloadInjectFile))

	mux.HandleFunc("GET /services", TEAMAUTH(router.ServicesPage))
	mux.HandleFunc("GET /api/pcrs", TEAMAUTH(api.GetPcrs))
	mux.HandleFunc("GET /api/credlists", TEAMAUTH(api.GetCredlists))
	mux.HandleFunc("POST /api/pcrs/submit", TEAMAUTH(api.CreatePcr))

	// team auth WWW routes
	mux.HandleFunc("GET /injects", TEAMAUTH(router.InjectsPage))

	mux.HandleFunc("GET /pcr", TEAMAUTH(router.PcrPage))

	/******************************************
	|                                         |
	|               RED ROUTES                |
	|                                         |
	******************************************/
	REDAUTH := middleware.MiddlewareChain(middleware.Authentication("red", "admin"))

	// red auth API routes
	mux.HandleFunc("GET /api/red", REDAUTH(api.GetRed))
	// mux.HandleFunc("POST /api/red/vuln", REDAUTH(api.CreatePcr))
	mux.HandleFunc("POST /api/red/box", REDAUTH(api.CreateBox))
	mux.HandleFunc("POST /api/red/vector", REDAUTH(api.CreateVector))
	mux.HandleFunc("POST /api/red/attack", REDAUTH(api.CreateAttack))

	mux.HandleFunc("POST /api/red/box/{id}", REDAUTH(api.EditBox))
	mux.HandleFunc("POST /api/red/vector/{id}", REDAUTH(api.EditVector))
	mux.HandleFunc("POST /api/red/attack/{id}", REDAUTH(api.EditAttack))

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

	ADMINAUTH := middleware.MiddlewareChain(middleware.Logging, middleware.Authentication("admin"))
	// admin auth API routes
	mux.HandleFunc("POST /api/announcements/create", ADMINAUTH(api.CreateAnnouncement))
	mux.HandleFunc("POST /api/announcements/{id}", ADMINAUTH(api.UpdateAnnouncement))
	mux.HandleFunc("DELETE /api/announcements/{id}", ADMINAUTH(api.DeleteAnnouncement))

	mux.HandleFunc("POST /api/injects/create", ADMINAUTH(api.CreateInject))
	mux.HandleFunc("POST /api/injects/{id}", ADMINAUTH(api.UpdateInject))
	mux.HandleFunc("DELETE /api/injects/{id}", ADMINAUTH(api.DeleteInject))

	// router.HandleFunc("POST /api/engine/service/create", ADMINAUTH(api.CreateService))
	// router.HandleFunc("POST /api/engine/service/update", ADMINAUTH(api.UpdateService))
	// router.HandleFunc("DELETE /api/engine/service/delete", ADMINAUTH(api.DeleteService))

	mux.HandleFunc("POST /api/engine/pause", ADMINAUTH(api.PauseEngine))
	mux.HandleFunc("GET /api/engine/reset", ADMINAUTH(api.ResetScores))
	mux.HandleFunc("GET /api/engine", ADMINAUTH(api.GetEngine))
	mux.HandleFunc("GET /api/engine/tasks", ADMINAUTH(api.GetActiveTasks))
	mux.HandleFunc("POST /api/admin/teams", ADMINAUTH(api.UpdateTeams))
	mux.HandleFunc("GET /api/admin/teamchecks", ADMINAUTH(api.GetTeamChecks))
	mux.HandleFunc("POST /api/admin/teamchecks", ADMINAUTH(api.UpdateTeamChecks))

	mux.HandleFunc("GET /api/engine/export/scores", ADMINAUTH(api.ExportScores))
	mux.HandleFunc("GET /api/engine/export/config", ADMINAUTH(api.ExportConfig))

	// admin auth WWW routes
	mux.HandleFunc("GET /admin", ADMINAUTH(router.AdminPage))
	mux.HandleFunc("GET /admin/engine", ADMINAUTH(router.AdministrateEnginePage))
	mux.HandleFunc("GET /admin/runners", ADMINAUTH(router.AdministrateRunnersPage))
	mux.HandleFunc("GET /admin/teams", ADMINAUTH(router.AdministrateTeamsPage))
	mux.HandleFunc("GET /admin/appearance", ADMINAUTH(router.AdministrateAppearancePage))

	// start server
	server := http.Server{
		Addr:    fmt.Sprintf("%s:%d", router.Config.RequiredSettings.BindAddress, router.Config.MiscSettings.Port),
		Handler: mux,
	}
	slog.Info(fmt.Sprintf("Starting Web Server on %s://%s:%d", protocol, router.Config.RequiredSettings.BindAddress, router.Config.MiscSettings.Port))

	// start server
	if router.Config.SslSettings != (config.SslConfig{}) {
		log.Fatal(server.ListenAndServeTLS(router.Config.SslSettings.HttpsCert, router.Config.SslSettings.HttpsKey))
	} else {
		log.Fatal(server.ListenAndServe())
	}
}
