package web

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

// methodRow is one sign-in provider on the profile: linked or offered.
type methodRow struct {
	ID       string
	Label    string
	Linked   *domain.Identity
	CanLink  bool // the provider is configured on this server
	LastOnly bool // unlinking would leave no way to sign in
}

var profileNotices = map[string]string{
	"password": "Пароль сохранён. Остальные сессии завершены.",
	"name":     "Имя сохранено.",
	"unlinked": "Способ входа отвязан.",
}

func (h *Handler) renderProfile(w http.ResponseWriter, r *http.Request, status int, errText string, passwordOpen bool) {
	p, err := h.Service.Profile(r.Context(), me(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := h.page(r, "Профиль", "profile")
	v.Profile, v.Error, v.Password = p, errText, passwordOpen
	if v.Error == "" {
		v.Error = authErrors[r.URL.Query().Get("error")]
	}
	v.Notice = profileNotices[r.URL.Query().Get("done")]
	if linked := r.URL.Query().Get("linked"); linked != "" && v.Error == "" {
		v.Notice = "Способ входа привязан."
	}
	v.SignIn = methods(p, h.Providers)
	h.render(w, status, "profile", v)
}

// methods lists configured providers plus any linked provider that is no longer configured.
func methods(p app.Profile, providers []app.AuthProvider) []methodRow {
	linked := map[string]*domain.Identity{}
	for i := range p.Identities {
		linked[p.Identities[i].Provider] = &p.Identities[i]
	}
	last := p.Methods() <= 1
	rows := make([]methodRow, 0, len(providers))
	for _, pr := range providers {
		rows = append(rows, methodRow{ID: pr.ID(), Label: pr.Label(), Linked: linked[pr.ID()], CanLink: true, LastOnly: last})
		delete(linked, pr.ID())
	}
	for _, id := range p.Identities {
		if _, gone := linked[id.Provider]; gone {
			rows = append(rows, methodRow{ID: id.Provider, Label: id.Provider, Linked: linked[id.Provider], LastOnly: last})
		}
	}
	return rows
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) {
	h.renderProfile(w, r, http.StatusOK, "", false)
}

func (h *Handler) profileSave(w http.ResponseWriter, r *http.Request) {
	err := h.Service.RenameAccount(r.Context(), me(r), r.FormValue("name"))
	if errors.Is(err, app.ErrInvalid) {
		h.renderProfile(w, r, http.StatusBadRequest, "Имя — от 1 до 200 символов.", false)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/profile?done=name")
}

func (h *Handler) passwordSave(w http.ResponseWriter, r *http.Request) {
	session := ""
	if c, err := r.Cookie(SessionCookie); err == nil {
		session = c.Value
	}
	err := h.Service.ChangePassword(r.Context(), me(r), session, r.FormValue("current"), r.FormValue("email"), r.FormValue("password"))
	var msg string
	switch {
	case err == nil:
		redirect(w, r, "/profile?done=password")
		return
	case errors.Is(err, app.ErrUnauthorized):
		msg = "Текущий пароль не подошёл."
	case errors.Is(err, app.ErrInvalid):
		msg = "Проверьте почту и пароль: не короче 8 символов."
	case errors.Is(err, app.ErrConflict):
		msg = "Эта почта уже занята другим аккаунтом."
	default:
		h.fail(w, r, err)
		return
	}
	h.renderProfile(w, r, http.StatusBadRequest, msg, true)
}

func (h *Handler) unlink(w http.ResponseWriter, r *http.Request) {
	err := h.Service.UnlinkIdentity(r.Context(), me(r), chi.URLParam(r, "provider"))
	switch {
	case err == nil, errors.Is(err, app.ErrNotFound):
		redirect(w, r, "/profile?done=unlinked")
	case errors.Is(err, app.ErrConflict):
		h.renderProfile(w, r, http.StatusConflict, "Это последний способ входа. Сначала задайте пароль или привяжите другой.", false)
	default:
		h.fail(w, r, err)
	}
}
