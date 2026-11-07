package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/p0vidl0/lateness/internal/adapters/authprovider"
	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
	"github.com/p0vidl0/lateness/internal/secret"
)

const (
	oauthCookie = "lateness_oauth"
	oauthTTL    = 10 * time.Minute
)

// Error codes passed in ?error= after a provider round trip.
var authErrors = map[string]string{
	"provider": "Не удалось войти через внешний сервис. Попробуйте ещё раз.",
	"invite":   "Вход только по приглашению. Попросите ссылку у администратора.",
	"unusable": "Приглашение уже использовано, истекло или отозвано.",
	"taken":    "Этот аккаунт провайдера уже привязан к другому пользователю Lateness.",
}

func (h *Handler) providerViews() []providerView {
	out := make([]providerView, 0, len(h.Providers))
	for _, p := range h.Providers {
		out = append(out, providerView{ID: p.ID(), Label: p.Label()})
	}
	return out
}

func (h *Handler) provider(id string) app.AuthProvider {
	for _, p := range h.Providers {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// Password sign-in

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.account(r); ok {
		redirect(w, r, "/")
		return
	}
	h.render(w, http.StatusOK, "login", view{Title: "Вход", Providers: h.providerViews(), Error: authErrors[r.URL.Query().Get("error")]})
}

func (h *Handler) loginPost(w http.ResponseWriter, r *http.Request) {
	token, _, err := h.Service.Login(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		h.render(w, http.StatusUnauthorized, "login", view{
			Title: "Вход", Providers: h.providerViews(), EmailField: r.FormValue("email"), Error: "Неверная почта или пароль",
		})
		return
	}
	h.setSession(w, token)
	redirect(w, r, "/")
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookie); err == nil {
		_ = h.Service.Logout(r.Context(), cookie.Value)
	}
	clearCookie(w, SessionCookie, "/")
	redirect(w, r, "/login")
}

// Invite sign-up

func (h *Handler) inviteView(r *http.Request, token string) (view, int) {
	v := view{Title: "Приглашение", InviteToken: token, Providers: h.providerViews(), Error: authErrors[r.URL.Query().Get("error")]}
	inv, err := h.Service.Invite(r.Context(), token)
	switch {
	case errors.Is(err, app.ErrNotFound):
		v.Error = "Такого приглашения нет. Проверьте ссылку."
		return v, http.StatusNotFound
	case err != nil:
		slog.Error("invite", "err", err.Error())
		v.Error = "Не удалось проверить приглашение."
		return v, http.StatusInternalServerError
	case inv.State(h.Service.Now()) != domain.InviteActive:
		v.Error = authErrors["unusable"]
		return v, http.StatusGone
	}
	v.InviteOK = true
	return v, http.StatusOK
}

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	v, status := h.inviteView(r, chi.URLParam(r, "token"))
	h.render(w, status, "invite", v)
}

func (h *Handler) invitePost(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	session, _, err := h.Service.RegisterWithInvite(r.Context(), token, r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		v, _ := h.inviteView(r, token)
		v.EmailField = r.FormValue("email")
		var status int
		switch {
		case errors.Is(err, app.ErrInviteUnusable):
			v.InviteOK, v.Error, status = false, authErrors["unusable"], http.StatusGone
		case errors.Is(err, app.ErrInvalid):
			v.Error, status = "Проверьте почту и пароль: не короче 8 символов.", http.StatusBadRequest
		case errors.Is(err, app.ErrConflict):
			v.Error, status = "Эта почта уже зарегистрирована. Войдите со своим паролем.", http.StatusConflict
		default:
			slog.Error("register", "err", err.Error())
			v.Error, status = "Не удалось создать аккаунт.", http.StatusInternalServerError
		}
		h.render(w, status, "invite", v)
		return
	}
	h.setSession(w, session)
	redirect(w, r, "/")
}

// Provider round trip: POST /auth/{provider} → provider → GET /auth/{provider}/callback.

// oauthState lives in a short-lived HttpOnly cookie instead of a table.
type oauthState struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Invite   string `json:"i,omitempty"`
	Link     bool   `json:"l,omitempty"` // attach to the signed-in account instead of signing in
}

func (h *Handler) redirectURI(provider string) string {
	return h.PublicURL + "/auth/" + provider + "/callback"
}

func (h *Handler) authStart(w http.ResponseWriter, r *http.Request) {
	p := h.provider(chi.URLParam(r, "provider"))
	if p == nil {
		http.NotFound(w, r)
		return
	}
	link := r.FormValue("link") == "1"
	if _, ok := h.account(r); link && !ok {
		redirect(w, r, "/login")
		return
	}
	state, err1 := secret.Hex(24)
	verifier, err2 := secret.Hex(32)
	if err1 != nil || err2 != nil {
		http.Error(w, "random", http.StatusInternalServerError)
		return
	}
	raw, _ := json.Marshal(oauthState{Provider: p.ID(), State: state, Verifier: verifier, Invite: r.FormValue("invite"), Link: link})
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookie, Value: base64.RawURLEncoding.EncodeToString(raw), Path: "/auth/", MaxAge: int(oauthTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: h.secure(),
	})
	http.Redirect(w, r, p.AuthURL(state, authprovider.Challenge(verifier), h.redirectURI(p.ID())), http.StatusSeeOther)
}

func (h *Handler) authCallback(w http.ResponseWriter, r *http.Request) {
	p := h.provider(chi.URLParam(r, "provider"))
	if p == nil {
		http.NotFound(w, r)
		return
	}
	st, ok := readOAuthState(r)
	clearCookie(w, oauthCookie, "/auth/")
	back := func(code string) {
		target := "/login"
		if st.Link {
			target = "/profile"
		} else if st.Invite != "" {
			target = "/invite/" + url.PathEscape(st.Invite)
		}
		redirect(w, r, target+"?error="+code)
	}
	q := r.URL.Query()
	if !ok || st.Provider != p.ID() || q.Get("state") == "" || q.Get("state") != st.State || q.Get("code") == "" {
		back("provider")
		return
	}
	id, err := p.Exchange(r.Context(), q.Get("code"), st.Verifier, h.redirectURI(p.ID()), q)
	if err != nil {
		slog.Warn("auth provider", "provider", p.ID(), "err", err.Error())
		back("provider")
		return
	}
	if st.Link {
		h.linkCallback(w, r, p, id, back)
		return
	}
	token, _, err := h.Service.LoginExternal(r.Context(), id, st.Invite)
	switch {
	case err == nil:
		h.setSession(w, token)
		redirect(w, r, "/")
	case errors.Is(err, app.ErrInviteRequired):
		back("invite")
	case errors.Is(err, app.ErrInviteUnusable):
		back("unusable")
	default:
		slog.Error("external login", "provider", p.ID(), "err", err.Error())
		back("provider")
	}
}

// linkCallback attaches the identity to the account of the current session.
func (h *Handler) linkCallback(w http.ResponseWriter, r *http.Request, p app.AuthProvider, id domain.Identity, back func(string)) {
	me, ok := h.account(r)
	if !ok {
		redirect(w, r, "/login")
		return
	}
	err := h.Service.LinkIdentity(r.Context(), me, id)
	switch {
	case err == nil:
		redirect(w, r, "/profile?linked="+url.QueryEscape(p.ID()))
	case errors.Is(err, app.ErrConflict):
		back("taken")
	default:
		slog.Error("link identity", "provider", p.ID(), "err", err.Error())
		back("provider")
	}
}

func readOAuthState(r *http.Request) (oauthState, bool) {
	var st oauthState
	c, err := r.Cookie(oauthCookie)
	if err != nil {
		return st, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || json.Unmarshal(raw, &st) != nil {
		return oauthState{}, false
	}
	return st, st.State != "" && st.Verifier != ""
}

// Admin: invites

type inviteRow struct {
	domain.Invite
	State domain.InviteState
}

func (h *Handler) renderInvites(w http.ResponseWriter, r *http.Request, status int, inviteURL, errText string) {
	invites, err := h.Service.ListInvites(r.Context(), me(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	now := h.Service.Now()
	v := h.page(r, "Приглашения", "invites")
	for _, inv := range invites {
		v.Invites = append(v.Invites, inviteRow{Invite: inv, State: inv.State(now)})
	}
	v.InviteURL, v.Error = inviteURL, errText
	h.render(w, status, "invites", v)
}

func (h *Handler) invites(w http.ResponseWriter, r *http.Request) {
	h.renderInvites(w, r, http.StatusOK, "", "")
}

func (h *Handler) inviteCreate(w http.ResponseWriter, r *http.Request) {
	account := me(r)
	_, token, err := h.Service.IssueInvite(r.Context(), &account, r.FormValue("note"), r.FormValue("admin") == "1")
	if errors.Is(err, app.ErrInvalid) {
		h.renderInvites(w, r, http.StatusBadRequest, "", "Пометка — до 200 символов.")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderInvites(w, r, http.StatusOK, h.PublicURL+"/invite/"+token, "")
}

func (h *Handler) inviteRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = h.Service.RevokeInvite(r.Context(), me(r), id)
	}
	if err != nil && !errors.Is(err, app.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/admin/invites")
}
