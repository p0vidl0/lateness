// Package web serves everything over HTTP: server-rendered pages, the intake endpoint
// and health checks. Pages work without JS; assets/app.js only enhances them.
package web

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets
var assetFS embed.FS

const (
	SessionCookie = "lateness_session"
	projectCookie = "lateness_project"
	maxBody       = 1 << 20
	maxIntakeBody = 1 << 16
)

type Handler struct {
	Service    *app.Service
	PublicURL  string
	SessionTTL time.Duration
	Providers  []app.AuthProvider
	tmpl       *template.Template
	assets     http.Handler
}

func New(service *app.Service, publicURL string, sessionTTL time.Duration, providers ...app.AuthProvider) (*Handler, error) {
	tmpl, err := template.New("pages").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		return nil, err
	}
	if sessionTTL <= 0 {
		sessionTTL = 30 * 24 * time.Hour
	}
	return &Handler{
		Service: service, PublicURL: strings.TrimRight(publicURL, "/"), SessionTTL: sessionTTL, Providers: providers,
		tmpl: tmpl, assets: http.StripPrefix("/assets/", http.FileServer(http.FS(sub))),
	}, nil
}

// Routes is the whole HTTP surface of serve. ready backs /readyz.
func (h *Handler) Routes(ready func(context.Context) error) http.Handler {
	r := chi.NewRouter()
	r.Use(limitBody, sameOrigin)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready != nil && ready(r.Context()) != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	r.Post("/intake/{token}", h.intake)
	r.Get("/assets/*", h.assets.ServeHTTP)

	r.Get("/login", h.login)
	r.Post("/login", h.loginPost)
	r.Get("/invite/{token}", h.invite)
	r.Post("/invite/{token}", h.invitePost)
	r.Post("/auth/{provider}", h.authStart)
	r.Get("/auth/{provider}/callback", h.authCallback)

	r.Group(func(r chi.Router) {
		r.Use(h.requireSession)
		r.Post("/logout", h.logout)
		r.Get("/profile", h.profile)
		r.Post("/profile", h.profileSave)
		r.Post("/profile/password", h.passwordSave)
		r.Post("/profile/identities/{provider}/unlink", h.unlink)
		r.Get("/", h.home)
		r.Get("/projects/new", h.projectNew)
		r.Post("/projects", h.projectCreate)
		r.Get("/admin/invites", h.invites)
		r.Post("/admin/invites", h.inviteCreate)
		r.Post("/admin/invites/{id}/revoke", h.inviteRevoke)
		r.Route("/p/{project}", func(r chi.Router) {
			r.Use(h.requireProject)
			r.Get("/", h.list)
			r.Get("/expectations/new", h.newForm)
			r.Post("/expectations", h.create)
			r.Get("/expectations/{id}", h.card)
			r.Get("/expectations/{id}/edit", h.editForm)
			r.Post("/expectations/{id}", h.update)
			r.Post("/expectations/{id}/hold", h.hold)
			r.Post("/expectations/{id}/resume", h.resume)
			r.Post("/expectations/{id}/delete", h.expectationDelete)
			r.Get("/outlets", h.outlets)
			r.Post("/outlets", h.outletSave)
			r.Get("/outlets/{id}", h.outletEdit)
			r.Post("/outlets/{id}", h.outletSave)
			r.Post("/outlets/{id}/delete", h.outletDelete)
			r.Get("/settings", h.settings)
			r.Post("/settings", h.settingsSave)
			r.Post("/delete", h.projectDelete)
		})
	})
	return r
}

// Intake

func (h *Handler) intake(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxIntakeBody+1))
	if err != nil || len(raw) > maxIntakeBody {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	err = h.Service.AcceptSignal(r.Context(), chi.URLParam(r, "token"), raw)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, app.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, app.ErrNotFound):
		http.Error(w, "unknown intake token", http.StatusNotFound)
	case errors.Is(err, app.ErrConflict):
		http.Error(w, "busy, retry", http.StatusConflict)
	default:
		slog.Error("intake", "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// Context: the signed-in account and the current project.

type ctxKey int

const (
	accountKey ctxKey = iota
	projectKey
)

func me(r *http.Request) domain.Account {
	a, _ := r.Context().Value(accountKey).(domain.Account)
	return a
}

func project(r *http.Request) domain.Project {
	p, _ := r.Context().Value(projectKey).(domain.Project)
	return p
}

func (h *Handler) account(r *http.Request) (domain.Account, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || h.Service == nil {
		return domain.Account{}, false
	}
	a, err := h.Service.Session(r.Context(), cookie.Value)
	return a, err == nil
}

func (h *Handler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := h.account(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountKey, a)))
	})
}

func (h *Handler) requireProject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "project"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p, err := h.Service.Project(r.Context(), me(r), id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: projectCookie, Value: p.ID.String(), Path: "/", HttpOnly: true,
			SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 3600})
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), projectKey, p)))
	})
}

// home opens the last used project, else the first one.
func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	projects, err := h.Service.Projects(r.Context(), me(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(projects) == 0 {
		http.Redirect(w, r, "/projects/new", http.StatusSeeOther)
		return
	}
	target := projects[0].ID
	if c, err := r.Cookie(projectCookie); err == nil {
		for _, p := range projects {
			if p.ID.String() == c.Value {
				target = p.ID
			}
		}
	}
	http.Redirect(w, r, "/p/"+target.String()+"/", http.StatusSeeOther)
}

// Rendering

// view is the data of every page. Page-specific fields stay zero elsewhere.
type view struct {
	Title    string
	Nav      string
	Me       domain.Account
	Project  domain.Project
	Projects []domain.Project
	Error    string
	Notice   string

	// auth pages
	EmailField  string
	Providers   []providerView
	InviteToken string
	InviteOK    bool

	// expectations
	Rows        []expRow
	Counts      []statusCount
	Total       int
	Expectation domain.Expectation
	Schedule    string
	ProbeText   string
	Hero        hero
	Window      windowBar
	Events      []event
	IntakeURL   string
	Assigned    []domain.Outlet
	Form        expectationForm
	Methods     []string

	// outlets
	Outlets []outletItem
	Outlet  outletForm
	Usage   int

	// profile
	Profile  app.Profile
	SignIn   []methodRow
	Password bool // the password form is shown open after an error

	// invites
	Invites   []inviteRow
	InviteURL string
}

type providerView struct{ ID, Label string }

// Base is the URL prefix of the current project.
func (v view) Base() string { return "/p/" + v.Project.ID.String() }

// page fills the shared fields of an app page.
func (h *Handler) page(r *http.Request, title, nav string) view {
	v := view{Title: title, Nav: nav, Me: me(r), Project: project(r)}
	if h.Service != nil && v.Me.ID != uuid.Nil {
		v.Projects, _ = h.Service.Projects(r.Context(), v.Me)
	}
	return v
}

func (h *Handler) render(w http.ResponseWriter, status int, name string, data view) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("render", "template", name, "err", err.Error())
	}
}

// fail maps a scenario error that has no page of its own.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, app.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, app.ErrForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		slog.Error("page", "path", r.URL.Path, "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, app.ErrNotFound
	}
	return id, nil
}

// Middleware

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(w, r)
	})
}

// sameOrigin refuses cross-site form posts. Browsers send Origin on POST;
// intake is meant for scripts and stays open.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !strings.HasPrefix(r.URL.Path, "/intake/") {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "cross-origin request", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: h.secure(), Expires: time.Now().Add(h.SessionTTL),
	})
}

// secure marks cookies Secure when the service is published over HTTPS.
func (h *Handler) secure() bool { return strings.HasPrefix(h.PublicURL, "https://") }

func clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

var funcs = template.FuncMap{
	"statusLabel": statusLabel,
	"iso":         func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"ts":          func(t time.Time) string { return t.UTC().Format("02.01.2006 15:04 UTC") },
	"isHeld":      func(s domain.Status) bool { return s == domain.StatusHeld },
	"kindLabel":   outletKindLabel,
	"sameID":      func(a, b uuid.UUID) bool { return a == b },
	"initial": func(s string) string {
		for _, r := range s {
			return string(r)
		}
		return "?"
	},
}
