package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/p0vidl0/lateness/internal/app"
	"github.com/p0vidl0/lateness/internal/domain"
)

type outletItem struct {
	domain.Outlet
	Usage int
}

func plainItems(outlets []domain.Outlet) []outletItem {
	items := make([]outletItem, 0, len(outlets))
	for _, o := range outlets {
		items = append(items, outletItem{Outlet: o})
	}
	return items
}

// outletForm is an outlet as the editor shows it: secrets are never sent back to the browser.
type outletForm struct {
	ID          uuid.UUID
	Name        string
	Kind        domain.OutletKind
	SMTP        domain.SMTPSettings
	HTTPURL     string
	ChatID      string
	HasPassword bool
	HasToken    bool
}

func (f outletForm) IsNew() bool { return f.ID == uuid.Nil }

func newOutletForm() outletForm {
	return outletForm{Kind: domain.OutletSMTP, SMTP: domain.SMTPSettings{Port: 587, Security: domain.SMTPStartTLS}}
}

func formFromOutlet(o domain.Outlet) outletForm {
	f := outletForm{ID: o.ID, Name: o.Name, Kind: o.Kind, SMTP: domain.SMTPSettings{Port: 587, Security: domain.SMTPStartTLS}}
	if s := o.Settings.SMTP; s != nil {
		f.SMTP, f.HasPassword = *s, s.Password != ""
		f.SMTP.Password = ""
	}
	if s := o.Settings.HTTP; s != nil {
		f.HTTPURL = s.URL
	}
	if s := o.Settings.Telegram; s != nil {
		f.ChatID, f.HasToken = s.ChatID, s.BotToken != ""
	}
	return f
}

func outletFromRequest(r *http.Request, id uuid.UUID) domain.Outlet {
	o := domain.Outlet{ID: id, Name: r.FormValue("name"), Kind: domain.OutletKind(r.FormValue("kind"))}
	switch o.Kind {
	case domain.OutletSMTP:
		port, _ := strconv.Atoi(r.FormValue("smtp_port"))
		o.Settings.SMTP = &domain.SMTPSettings{
			Host: r.FormValue("smtp_host"), Port: port, Security: domain.SMTPSecurity(r.FormValue("smtp_security")),
			Username: r.FormValue("smtp_username"), Password: r.FormValue("smtp_password"),
			From: r.FormValue("smtp_from"), To: r.FormValue("smtp_to"),
		}
	case domain.OutletHTTP:
		o.Settings.HTTP = &domain.HTTPSettings{URL: r.FormValue("http_url")}
	case domain.OutletTelegram:
		o.Settings.Telegram = &domain.TelegramSettings{BotToken: r.FormValue("telegram_token"), ChatID: r.FormValue("telegram_chat")}
	}
	return o
}

func (h *Handler) renderOutlets(w http.ResponseWriter, r *http.Request, status int, form outletForm, errText string) {
	p := project(r)
	outlets, err := h.Service.ListOutlets(r.Context(), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	usage, err := h.Service.OutletUsage(r.Context(), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := h.page(r, "Каналы", "outlets")
	for _, o := range outlets {
		v.Outlets = append(v.Outlets, outletItem{Outlet: o, Usage: usage[o.ID]})
	}
	v.Outlet, v.Usage, v.Error = form, usage[form.ID], errText
	h.render(w, status, "outlets", v)
}

func (h *Handler) outlets(w http.ResponseWriter, r *http.Request) {
	h.renderOutlets(w, r, http.StatusOK, newOutletForm(), "")
}

func (h *Handler) outletEdit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	o, err := h.Service.GetOutlet(r.Context(), project(r), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderOutlets(w, r, http.StatusOK, formFromOutlet(o), "")
}

func (h *Handler) outletSave(w http.ResponseWriter, r *http.Request) {
	id := uuid.Nil
	if chi.URLParam(r, "id") != "" {
		var err error
		if id, err = pathID(r); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	saved, err := h.Service.SaveOutlet(r.Context(), project(r), outletFromRequest(r, id))
	if errors.Is(err, app.ErrInvalid) {
		form := formFromOutlet(outletFromRequest(r, id))
		if id != uuid.Nil {
			if prev, gerr := h.Service.GetOutlet(r.Context(), project(r), id); gerr == nil {
				prevForm := formFromOutlet(prev)
				form.HasPassword, form.HasToken = prevForm.HasPassword, prevForm.HasToken
			}
		}
		h.renderOutlets(w, r, http.StatusBadRequest, form, "Проверьте название и настройки: адреса почты, SMTP-сервер, URL или данные Telegram.")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, base(r)+"/outlets/"+saved.ID.String())
}

func (h *Handler) outletDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = h.Service.DeleteOutlet(r.Context(), project(r), id)
	}
	if errors.Is(err, app.ErrConflict) {
		o, gerr := h.Service.GetOutlet(r.Context(), project(r), id)
		if gerr != nil {
			h.fail(w, r, gerr)
			return
		}
		h.renderOutlets(w, r, http.StatusConflict, formFromOutlet(o), "Канал назначен ожиданиям — сначала уберите его из них.")
		return
	}
	if err != nil && !errors.Is(err, app.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, base(r)+"/outlets")
}

func outletKindLabel(kind domain.OutletKind) string {
	switch kind {
	case domain.OutletHTTP:
		return "HTTP"
	case domain.OutletTelegram:
		return "TG"
	default:
		return "SMTP"
	}
}
