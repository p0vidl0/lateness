// Progressive enhancement for Lateness pages. Every page works without it.
(function () {
  "use strict";

  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
  const pad = (n) => String(n).padStart(2, "0");

  // Absolute times arrive in UTC; show them in the viewer's zone.
  function localizeTimes() {
    const now = new Date();
    for (const el of $$("time[data-local]")) {
      const d = new Date(el.getAttribute("datetime"));
      if (isNaN(d)) continue;
      const sameYear = d.getFullYear() === now.getFullYear();
      el.textContent = pad(d.getDate()) + "." + pad(d.getMonth() + 1) + (sameYear ? "" : "." + d.getFullYear()) +
        " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
      el.title = d.toLocaleString("ru-RU", { dateStyle: "long", timeStyle: "medium" });
    }
  }

  function copyText(text, btn) {
    const done = () => {
      const label = btn.dataset.label || btn.textContent;
      btn.dataset.label = label;
      btn.textContent = "Скопировано";
      setTimeout(() => { btn.textContent = label; }, 1600);
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done, () => {});
      return;
    }
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); done(); } finally { ta.remove(); }
  }

  function initCopy() {
    for (const btn of $$("[data-copy], [data-copy-from]")) {
      btn.hidden = false;
      btn.addEventListener("click", () => {
        const from = btn.dataset.copyFrom && document.getElementById(btn.dataset.copyFrom);
        copyText(from ? from.textContent.trim() : btn.dataset.copy, btn);
      });
    }
  }

  function initTabs() {
    for (const list of $$("[role=tablist]")) {
      const tabs = $$("[role=tab]", list);
      const select = (tab) => {
        for (const t of tabs) {
          const on = t === tab;
          t.setAttribute("aria-selected", on ? "true" : "false");
          t.tabIndex = on ? 0 : -1;
          const panel = document.getElementById(t.getAttribute("aria-controls"));
          if (panel) panel.hidden = !on;
        }
      };
      tabs.forEach((tab, i) => {
        tab.addEventListener("click", () => select(tab));
        tab.addEventListener("keydown", (e) => {
          if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
          const next = tabs[(i + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length];
          next.focus();
          select(next);
        });
      });
    }
  }

  function initDialogs() {
    for (const btn of $$("[data-open-dialog]")) {
      const dlg = document.getElementById(btn.dataset.openDialog);
      if (!dlg || typeof dlg.showModal !== "function") continue;
      // The fallback form submits directly; with <dialog> support we confirm first.
      btn.type = "button";
      btn.addEventListener("click", (e) => { e.preventDefault(); dlg.showModal(); });
    }
    for (const btn of $$("[data-close-dialog]")) {
      btn.addEventListener("click", () => btn.closest("dialog").close());
    }
  }

  // Expectation list: status filter + name search, all client side.
  function initListFilter() {
    const rows = $$("[data-row]");
    if (!rows.length) return;
    const buttons = $$("[data-filter]");
    const search = document.querySelector("[data-search]");
    const empty = document.querySelector("[data-filter-empty]");
    let status = "all";
    const apply = () => {
      const q = (search && search.value || "").trim().toLowerCase();
      let shown = 0;
      for (const row of rows) {
        const ok = (status === "all" || row.dataset.status === status) && (!q || row.dataset.name.toLowerCase().includes(q));
        row.hidden = !ok;
        if (ok) shown++;
      }
      for (const b of buttons) b.setAttribute("aria-pressed", b.dataset.filter === status ? "true" : "false");
      if (empty) empty.hidden = shown !== 0;
    };
    for (const b of buttons) {
      b.addEventListener("click", () => {
        status = status === b.dataset.filter && b.dataset.filter !== "all" ? "all" : b.dataset.filter;
        apply();
      });
    }
    if (search) search.addEventListener("input", apply);
    apply();
  }

  function initTimelineFilter() {
    const items = $$("[data-ev]");
    const buttons = $$("[data-ev-filter]");
    if (!items.length || !buttons.length) return;
    for (const b of buttons) {
      b.addEventListener("click", () => {
        const f = b.dataset.evFilter;
        for (const x of buttons) x.setAttribute("aria-pressed", x === b ? "true" : "false");
        for (const it of items) {
          it.hidden = !(f === "all" || it.dataset.ev === f || (f === "delivery" && it.dataset.delivery === "1"));
        }
      });
    }
  }

  // Show sections whose data-when="field=value" matches the form state.
  function initSwitches() {
    for (const form of $$("form[data-switch]")) {
      const sync = () => {
        for (const sec of $$("[data-when]", form)) {
          const [name, value] = sec.dataset.when.split("=");
          const field = form.elements[name];
          sec.hidden = !(field && field.value === value);
        }
      };
      form.addEventListener("change", sync);
      sync();
    }
  }

  const unitWords = { 1: "с", 60: "мин", 3600: "ч", 86400: "дн" };
  function human(value, unit) {
    const n = Number(value);
    if (!isFinite(n) || n < 0) return "";
    if (n === 0) return "0";
    return n + " " + (unitWords[unit] || "с");
  }

  const cronWords = {
    "0 * * * *": "каждый час в :00",
    "*/5 * * * *": "каждые 5 минут",
    "*/15 * * * *": "каждые 15 минут",
    "0 3 * * *": "каждый день в 03:00",
    "0 9 * * 1-5": "по будням в 09:00",
    "0 0 * * 1": "по понедельникам в 00:00",
    "0 0 1 * *": "первого числа месяца в 00:00",
  };

  function initScheduleForm() {
    const form = document.querySelector("form[data-schedule-form]");
    if (!form) return;
    const el = (n) => form.elements[n];
    const summary = document.querySelector("[data-summary]");
    const cronHint = document.querySelector("[data-cron-hint]");
    const tz = el("timezone");
    if (tz && !tz.value) {
      try { tz.value = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"; } catch (e) { tz.value = "UTC"; }
    }
    for (const b of $$("[data-preset]", form)) {
      b.addEventListener("click", () => {
        el("cron").value = b.dataset.preset;
        update();
      });
    }
    function update() {
      const kind = el("kind").value;
      const slack = human(el("slack_value").value, el("slack_unit").value);
      const slackPart = slack === "0" || !slack ? "без допуска" : "в течение " + slack;
      const expr = (el("cron").value || "").trim().replace(/\s+/g, " ");
      const words = cronWords[expr];
      if (cronHint) cronHint.textContent = words ? "≈ " + words : (expr.split(" ").length === 5 ? "Пять полей: минута, час, день, месяц, день недели" : "Нужно пять полей через пробел");
      for (const b of $$("[data-preset]", form)) b.setAttribute("aria-pressed", b.dataset.preset === expr ? "true" : "false");
      if (!summary) return;
      const probe = el("source") && el("source").value === "probe";
      const subject = probe ? "Сервис запрашивает " + ((el("probe_url").value || "").trim() || "URL") : "Задача должна присылать ok";
      const miss = probe ? "Если за это время ни одна проверка не пройдёт," : "Иначе";
      if (kind === "cron") {
        summary.textContent = (probe ? "Сервис запрашивает URL" : "Задача должна прислать ok") + " после каждого запуска по расписанию «" +
          (words || expr || "…") + "» (" + (tz.value || "UTC") + ") " + slackPart + ". " + miss + " ожидание станет просроченным.";
      } else {
        const period = human(el("period_value").value, el("period_unit").value);
        summary.textContent = subject + " не реже чем раз в " + (period || "…") +
          (slack && slack !== "0" ? ", плюс допуск " + slack : "") + ". " + miss + " ожидание станет просроченным и придёт уведомление.";
      }
    }
    form.addEventListener("input", update);
    form.addEventListener("change", update);
    update();
  }

  document.addEventListener("DOMContentLoaded", () => {
    localizeTimes();
    initCopy();
    initTabs();
    initDialogs();
    initListFilter();
    initTimelineFilter();
    initSwitches();
    initScheduleForm();
  });
})();
