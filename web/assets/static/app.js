document.documentElement.classList.add("js");

document.addEventListener("htmx:responseError", () => {
  const status = document.querySelector("[data-live-status]");
  if (status) status.textContent = "Die Anfrage ist fehlgeschlagen. Bitte erneut versuchen.";
});

function syncTransportForm(form) {
  const type = form.querySelector("[data-job-type]");
  const fallback = form.querySelector("[data-transport-default]");
  const mode = form.querySelector("[data-transport-mode]");
  const confirmation = form.querySelector("[data-external-confirmation]");
  if (!type || !fallback || !mode || !confirmation) return;

  const hasTransport = type.value === "chipping_with_transport";
  fallback.disabled = hasTransport;
  form.querySelectorAll("[data-transport-field]").forEach((field) => {
    field.hidden = !hasTransport;
  });
  form.querySelectorAll("[data-transport-control]").forEach((control) => {
    control.disabled = !hasTransport;
  });
  mode.required = hasTransport;

  const external = hasTransport && mode.value === "external";
  confirmation.hidden = !external;
  const checkbox = confirmation.querySelector("input[type='checkbox']");
  if (checkbox) {
    checkbox.disabled = !external;
    if (!external) checkbox.checked = false;
  }
}

document.querySelectorAll("[data-transport-form]").forEach((form) => {
  syncTransportForm(form);
  form.addEventListener("change", (event) => {
    if (event.target.matches("[data-job-type], [data-transport-mode]")) {
      syncTransportForm(form);
    }
  });
});

function localInputValue(date) {
  const parts = new Intl.DateTimeFormat("sv-SE", {
    timeZone: "Europe/Vienna", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", hourCycle: "h23",
  }).formatToParts(date).reduce((values, part) => ({ ...values, [part.type]: part.value }), {});
  return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`;
}

function calendarErrorMessage(error) {
  return error?.error?.message || "Die Planung konnte nicht gespeichert werden. Bitte prüfen Sie den aktuellen Kalenderstand.";
}

async function calendarRequest(url, form, csrf) {
  const body = form instanceof FormData ? new URLSearchParams(form) : form;
  const response = await fetch(url, {
    method: "POST",
    headers: { "X-CSRF-Token": csrf, "Accept": "application/json" },
    body,
    credentials: "same-origin",
  });
  const payload = await response.json().catch(() => ({ error: { code: "invalid_response" } }));
  if (!response.ok) {
    const failure = new Error(calendarErrorMessage(payload));
    failure.status = response.status;
    failure.code = payload?.error?.code;
    throw failure;
  }
  return payload;
}

function announceCalendar(message, isError = false) {
  const target = document.querySelector("[data-calendar-message]");
  if (!target) return;
  target.textContent = message;
  target.classList.toggle("calendar-message--error", isError);
}

function openPlanning(jobID, title, duration, start) {
  const dialog = document.querySelector("[data-planning-dialog]");
  if (!dialog) return;
  const form = dialog.querySelector("[data-planning-form]");
  form.reset();
  form.querySelector("[data-planning-job]").value = jobID;
  form.querySelector("[data-planning-title]").textContent = title || "Auftrag einplanen";
  form.querySelector("[data-planning-duration]").value = duration || "180";
  const defaultStart = start || new Date(Date.now() + 24 * 60 * 60 * 1000);
  defaultStart.setMinutes(0, 0, 0);
  if (!start) defaultStart.setHours(8);
  form.querySelector("[data-planning-start]").value = localInputValue(defaultStart);
  const error = form.querySelector("[data-planning-error]");
  error.hidden = true;
  error.textContent = "";
  dialog.showModal();
}

document.querySelectorAll("[data-plan-job]").forEach((button) => {
  button.addEventListener("click", () => openPlanning(button.dataset.planJob, button.dataset.title, button.dataset.duration));
});

const requestedPlanningJob = new URLSearchParams(window.location.search).get("job");
if (requestedPlanningJob) {
  document.querySelector(`[data-plan-job="${CSS.escape(requestedPlanningJob)}"]`)?.click();
}

document.querySelectorAll("[data-dialog-close]").forEach((button) => {
  button.addEventListener("click", () => button.closest("dialog")?.close());
});

const planningForm = document.querySelector("[data-planning-form]");
if (planningForm) {
  planningForm.addEventListener("submit", async (event) => {
    if (!window.fetch) return;
    event.preventDefault();
    const submit = planningForm.querySelector("button[type='submit']");
    const error = planningForm.querySelector("[data-planning-error]");
    submit.disabled = true;
    error.hidden = true;
    try {
      await calendarRequest("/api/v1/calendar/plan", new FormData(planningForm), planningForm.elements.csrf_token.value);
      const jobID = planningForm.elements.job_id.value;
      document.querySelector(`[data-calendar-job="${CSS.escape(jobID)}"]`)?.remove();
      planningForm.closest("dialog")?.close();
      window.hackWerkCalendar?.refetchEvents();
      announceCalendar("Terminvorschlag gespeichert. Der Termin ist noch nicht fixiert.");
    } catch (failure) {
      error.textContent = failure.message;
      error.hidden = false;
    } finally {
      submit.disabled = false;
    }
  });
}

function appointmentDetail(event) {
  const dialog = document.querySelector("[data-appointment-dialog]");
  if (!dialog) return;
  const props = event.extendedProps;
  dialog.dataset.appointmentId = event.id;
  dialog.dataset.version = props.version;
  dialog.querySelector("[data-appointment-title]").textContent = event.title;
  const detail = dialog.querySelector("[data-appointment-detail]");
  detail.replaceChildren();
  const rows = [
    ["Status", props.status_label],
    ["Zeit", `${event.start.toLocaleString("de-AT")} – ${event.end.toLocaleTimeString("de-AT", { hour: "2-digit", minute: "2-digit" })}`],
    ["Ort", props.locality], ["Menge", `${props.volume_m3} m³`],
    ["Fahrer", (props.drivers || []).map((item) => item.Name).join(", ") || "Nicht zugewiesen"],
    ["Ressourcen", (props.resources || []).map((item) => item.Name).join(", ") || "Nicht zugewiesen"],
  ];
  const list = document.createElement("dl");
  list.className = "appointment-detail-list";
  rows.forEach(([label, value]) => {
    const wrapper = document.createElement("div");
    const term = document.createElement("dt"); term.textContent = label;
    const description = document.createElement("dd"); description.textContent = value;
    wrapper.append(term, description); list.append(wrapper);
  });
  detail.append(list);
  if (props.maps_url) {
    const navigation = document.createElement("a");
    navigation.className = "button button--quiet"; navigation.href = props.maps_url;
    navigation.target = "_blank"; navigation.rel = "noopener noreferrer"; navigation.textContent = "Navigation öffnen";
    detail.append(navigation);
  }
  const fix = dialog.querySelector("[data-appointment-fix]");
  const cancel = dialog.querySelector("[data-appointment-cancel]");
  if (fix) fix.hidden = !props.can_fix;
  if (cancel) cancel.hidden = !props.can_cancel;
  dialog.showModal();
}

document.querySelectorAll("[data-appointment-close]").forEach((button) => {
  button.addEventListener("click", () => button.closest("dialog")?.close());
});

async function appointmentAction(action, extra = {}) {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const csrf = dialog.querySelector("[data-appointment-csrf]").value;
  const form = new FormData();
  form.set("csrf_token", csrf); form.set("version", dialog.dataset.version);
  Object.entries(extra).forEach(([key, value]) => form.set(key, value));
  const result = await calendarRequest(`/api/v1/appointments/${encodeURIComponent(dialog.dataset.appointmentId)}/${action}`, form, csrf);
  dialog.close(); window.hackWerkCalendar?.refetchEvents();
  announceCalendar(action === "fix" ? "Termin wurde ausdrücklich fixiert; der Versand erfolgt später über die Outbox." : "Termin wurde aktualisiert.");
  return result;
}

document.querySelector("[data-appointment-fix]")?.addEventListener("click", async () => {
  if (!window.confirm("Termin mit den angezeigten Fahrern und Ressourcen fixieren? Dadurch wird der Benachrichtigungsauftrag atomar vorgemerkt.")) return;
  try { await appointmentAction("fix"); } catch (failure) { announceCalendar(failure.message, true); }
});

document.querySelector("[data-appointment-cancel]")?.addEventListener("click", async () => {
  const reason = document.querySelector("[data-appointment-cancel-reason]")?.value || "";
  if (!window.confirm("Diesen Termin wirklich absagen?")) return;
  try { await appointmentAction("cancel", { reason }); } catch (failure) { announceCalendar(failure.message, true); }
});

function calendarMutation(info, action, csrf) {
  const form = new FormData();
  form.set("csrf_token", csrf);
  form.set("version", info.event.extendedProps.version);
  form.set("starts_at", info.event.start.toISOString());
  form.set("ends_at", info.event.end.toISOString());
  const send = () => calendarRequest(`/api/v1/appointments/${encodeURIComponent(info.event.id)}/${action}`, form, csrf);
  send().then(() => {
    window.hackWerkCalendar?.refetchEvents();
    announceCalendar("Terminzeit gespeichert.");
  }).catch(async (failure) => {
    if (failure.code === "driver_unavailable") {
      const reason = window.prompt("Fahrer ist nicht verfügbar. Begründung für Admin-Override eingeben oder abbrechen:");
      if (reason?.trim()) {
        form.set("override_reason", reason.trim());
        try { await send(); window.hackWerkCalendar?.refetchEvents(); announceCalendar("Terminzeit mit begründetem Verfügbarkeits-Override gespeichert."); return; } catch (retryFailure) { failure = retryFailure; }
      }
    }
    info.revert();
    announceCalendar(`${failure.message} Die Änderung wurde zurückgesetzt.`, true);
  });
}

const calendarElement = document.querySelector("[data-calendar]");
if (calendarElement && window.FullCalendar) {
  const editable = calendarElement.dataset.editable === "true";
  const compact = window.matchMedia("(max-width: 680px)").matches;
  const calendar = new FullCalendar.Calendar(calendarElement, {
    themeSystem: "classic",
    locale: "de-AT",
    firstDay: 1,
    initialView: compact ? "timeGridDay" : "timeGridWeek",
    nowIndicator: true,
    allDaySlot: false,
    slotMinTime: "06:00:00",
    slotMaxTime: "20:00:00",
    height: "auto",
	className: "hackwerk-fullcalendar",
	toolbarClass: "calendar-toolbar",
	toolbarSectionClass: "calendar-toolbar-section",
	buttonClass: "calendar-toolbar-button",
    editable,
    eventStartEditable: editable,
    eventDurationEditable: editable,
    headerToolbar: { start: "prev,next today", center: "title", end: compact ? "timeGridDay,listWeek" : "timeGridDay,timeGridWeek,listWeek" },
    buttons: {
      today: { text: "Heute" },
      timeGridDay: { text: "Tag" },
      timeGridWeek: { text: "Woche" },
      listWeek: { text: "Agenda" },
    },
    events(info, success, failure) {
      const url = new URL(calendarElement.dataset.eventSource, window.location.origin);
      url.searchParams.set("from", info.start.toISOString());
      url.searchParams.set("to", info.end.toISOString());
      fetch(url, { credentials: "same-origin", headers: { Accept: "application/json" } })
        .then((response) => response.ok ? response.json() : Promise.reject(new Error("Kalenderdaten nicht verfügbar")))
        .then(success).catch(failure);
    },
    eventDrop(info) { calendarMutation(info, "move", calendarElement.dataset.csrf); },
    eventResize(info) { calendarMutation(info, "resize", calendarElement.dataset.csrf); },
    eventClick(info) { appointmentDetail(info.event); },
    eventContent(info) {
      const content = document.createElement("div"); content.className = "calendar-event-content";
      const time = document.createElement("strong"); time.textContent = info.timeText;
      const title = document.createElement("span"); title.textContent = info.event.title;
      const status = document.createElement("small"); status.textContent = info.event.extendedProps.status_label;
      content.append(time, title, status); return { domNodes: [content] };
    },
    drop(info) {
      if (!editable) return;
      openPlanning(info.draggedEl.dataset.calendarJob, info.draggedEl.dataset.title, info.draggedEl.dataset.duration, info.date);
    },
  });
  calendar.render();
  window.hackWerkCalendar = calendar;
  const waitlist = document.querySelector("[data-calendar-waitlist]");
  if (editable && waitlist && FullCalendar.Draggable) {
    new FullCalendar.Draggable(waitlist, {
      itemSelector: "[data-calendar-job]",
      eventData(element) { return { title: element.dataset.title, duration: { minutes: Number(element.dataset.duration) }, create: false }; },
    });
  }
}
