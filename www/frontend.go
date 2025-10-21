package www

import (
	"maps"
	"net/http"
	"quotient/www/api"
	"slices"
	"text/template"
)

var (
	base *template.Template
)

var (
	templateFuncs = template.FuncMap{
		"dict": func(inputs ...any) map[string]any {
			dict := make(map[string]any)
			if len(inputs)%2 != 0 {
				panic("incorrect inputs")
			}
			var key string
			for i, v := range inputs {
				if i%2 == 0 {
					key = v.(string)
				} else {
					dict[key] = v
				}
			}
			return dict
		},
		"contains": func(array []string, element string) bool {
			return slices.Contains(array, element)
		},
	}
)

func init() {
	base = template.Must(template.ParseFiles("./static/templates/layouts/base.html"))
	base.Funcs(templateFuncs)
	template.Must(base.ParseGlob("./static/templates/partials/*.html"))
}

func (router *Router) pageData(r *http.Request, unique map[string]any) map[string]any {
	data := make(map[string]any)

	data["username"] = r.Context().Value("username")
	data["roles"] = r.Context().Value("roles")
	data["name"] = "QUOTIENT"
	data["config"] = router.Config
	data["error"] = ""

	if data["roles"] != nil {
		roles := data["roles"].([]string)
		if slices.Contains(roles, "admin") || slices.Contains(roles, "inject") {
			data["home"] = "/announcements"
		} else if slices.Contains(roles, "red") {
			data["home"] = "/graphs"
		} else {
			data["home"] = "/announcements"
		}
	}

	maps.Copy(data, unique)
	return data
}

func (router *Router) HomePage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login", http.StatusPermanentRedirect)
}

func (router *Router) LoginPage(w http.ResponseWriter, r *http.Request) {
	if username, roles := api.Authenticate(w, r); username != "" {
		var home string
		if slices.Contains(roles, "admin") || slices.Contains(roles, "inject") {
			home = "/announcements"
		} else if slices.Contains(roles, "red") {
			home = "/graphs"
		} else {
			home = "/announcements"
		}
		http.Redirect(w, r, home, http.StatusTemporaryRedirect)
		return
	}

	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/login.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Login"})); err != nil {
		panic(err)
	}
}

func (router *Router) LogoutPage(w http.ResponseWriter, r *http.Request) {
	api.Logout(w, r)
	http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
}

func (router *Router) AnnouncementsPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/announcements.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Announcements"})); err != nil {
		panic(err)
	}
}

func (router *Router) ServicesPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/services.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Services"})); err != nil {
		panic(err)
	}
}

func (router *Router) InjectsPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/injects.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Injects"})); err != nil {
		panic(err)
	}
}

func (router *Router) PcrPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/pcr.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "PCRs"})); err != nil {
		panic(err)
	}
}

func (router *Router) AdminPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/admin/admin.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Admin"})); err != nil {
		panic(err)
	}
}

func (router *Router) AdministrateTeamsPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/admin/teams.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Admin"})); err != nil {
		panic(err)
	}
}

func (router *Router) AdministrateEnginePage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/admin/engine.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Admin"})); err != nil {
		panic(err)
	}
}

func (router *Router) AdministrateRunnersPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/admin/runners.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Admin"})); err != nil {
		panic(err)
	}
}

func (router *Router) AdministrateAppearancePage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/admin/appearance.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Appearance"})); err != nil {
		panic(err)
	}
}

func (router *Router) GraphPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/graphs.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Graphs"})); err != nil {
		panic(err)
	}
}

func (router *Router) RedPage(w http.ResponseWriter, r *http.Request) {
	page := template.Must(template.Must(base.Clone()).ParseFiles("./static/templates/layouts/page.html", "./static/templates/pages/red.html"))
	if err := page.ExecuteTemplate(w, "base", router.pageData(r, map[string]any{"title": "Red Team"})); err != nil {
		panic(err)
	}
}
