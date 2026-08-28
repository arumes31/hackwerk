document.documentElement.classList.add("js");

const liveStatus = () => document.querySelector("[data-live-status]");
const announce = (message) => {
  const target = liveStatus();
  if (target) target.textContent = message;
};

function safePreferenceGet(key) {
  try { return window.localStorage.getItem(key); } catch { return null; }
}

function safePreferenceSet(key, value) {
  try { window.localStorage.setItem(key, value); } catch { /* Presentation preference stays optional. */ }
}

function applyPresentationPreferences() {
  const comfortable = safePreferenceGet("hackwerk:density") === "comfortable";
  const outdoor = safePreferenceGet("hackwerk:outdoor") === "true";
  document.documentElement.classList.toggle("density-comfortable", comfortable);
  document.documentElement.classList.toggle("outdoor-contrast", outdoor);
  document.querySelectorAll("[data-density-toggle]").forEach((button) => {
    button.textContent = comfortable ? "Kompakte Dichte" : "Komfortable Dichte";
    button.setAttribute("aria-pressed", String(comfortable));
  });
  document.querySelectorAll("[data-outdoor-toggle]").forEach((button) => {
    button.textContent = outdoor ? "Standard-Kontrast" : "Outdoor-Kontrast";
    button.setAttribute("aria-pressed", String(outdoor));
  });
}

document.querySelectorAll("[data-density-toggle]").forEach((button) => {
  button.addEventListener("click", () => {
    safePreferenceSet("hackwerk:density", document.documentElement.classList.contains("density-comfortable") ? "compact" : "comfortable");
    applyPresentationPreferences();
  });
});
document.querySelectorAll("[data-outdoor-toggle]").forEach((button) => {
  button.addEventListener("click", () => {
    safePreferenceSet("hackwerk:outdoor", document.documentElement.classList.contains("outdoor-contrast") ? "false" : "true");
    applyPresentationPreferences();
  });
});
applyPresentationPreferences();

function updateConnectivityBanner() {
  document.querySelectorAll("[data-connectivity-banner]").forEach((banner) => {
    banner.hidden = navigator.onLine;
    banner.textContent = navigator.onLine
      ? "Verbindung wiederhergestellt. Nicht gespeicherte Änderungen können jetzt gesendet werden."
      : "Offline: Lesen bleibt teilweise möglich, Änderungen werden nicht zwischengespeichert. Bitte erst bei Verbindung speichern.";
  });
  if (navigator.onLine) announce("Verbindung wiederhergestellt.");
}
window.addEventListener("online", updateConnectivityBanner);
window.addEventListener("offline", updateConnectivityBanner);
updateConnectivityBanner();

async function operationFailureMessage(response) {
  if (response.status === 401) {
    return "Ihre Sitzung ist abgelaufen. Bitte laden Sie die Seite neu und melden Sie sich erneut an.";
  }
  if (response.status === 403) {
    return "Die Sicherheitsprüfung ist fehlgeschlagen oder die Berechtigung wurde entzogen. Bitte laden Sie die Seite neu.";
  }
  const body = (await response.text()).trim();
  if (!body) return "Die Änderung konnte nicht gespeichert werden.";
  if (!(response.headers.get("content-type") || "").toLowerCase().includes("text/html")) return body;
  const document = new DOMParser().parseFromString(body, "text/html");
  const message = document.querySelector("[role='alert'], .error-page p, main p")?.textContent?.trim();
  return message || "Die Änderung konnte nicht gespeichert werden. Bitte laden Sie die Seite neu.";
}

document.addEventListener("submit", async (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || !form.closest("[data-operation-page]") || String(form.method).toLowerCase() === "get") return;
  const confirmationMessage = event.submitter?.dataset.confirmMessage;
  if (confirmationMessage && !window.confirm(confirmationMessage)) {
    event.preventDefault();
    announce("Aktion abgebrochen.");
    return;
  }
  event.preventDefault();
  if (form.dataset.operationSubmitting === "true") {
    announce("Diese Änderung wird bereits gespeichert.");
    return;
  }
  form.dataset.operationSubmitting = "true";
  const submitters = form.querySelectorAll('button[type="submit"], input[type="submit"]');
  submitters.forEach((control) => { control.disabled = true; control.setAttribute("aria-disabled", "true"); });
  let alert = form.querySelector("[data-operation-error]");
  if (!alert) {
    alert = document.createElement("div");
    alert.className = "form-alert";
    alert.dataset.operationError = "true";
    alert.setAttribute("role", "alert");
    alert.tabIndex = -1;
    alert.id = `operation-error-${Math.random().toString(36).slice(2)}`;
    form.setAttribute("aria-describedby", alert.id);
    form.prepend(alert);
  }
  alert.hidden = true;
  try {
    const response = await fetch(form.action, {
      method: form.method,
      body: new URLSearchParams(new FormData(form)),
      credentials: "same-origin",
      headers: { Accept: "text/html" },
    });
    if (response.redirected) {
      dirtyForms.delete(form);
      window.location.assign(response.url);
      return;
    }
    if (!response.ok) {
      alert.textContent = await operationFailureMessage(response);
      alert.hidden = false;
      alert.focus();
      return;
    }
    dirtyForms.delete(form);
    window.location.reload();
  } catch {
    alert.textContent = "Die Änderung konnte wegen eines Verbindungsfehlers nicht gespeichert werden. Ihre Eingaben bleiben erhalten.";
    alert.hidden = false;
    alert.focus();
  } finally {
    delete form.dataset.operationSubmitting;
    submitters.forEach((control) => { control.disabled = false; control.removeAttribute("aria-disabled"); });
  }
});

const dirtyForms = new Set();
const dirtyDialogs = new Set();
document.querySelectorAll("form").forEach((form) => {
  const method = String(form.method || "get").toLowerCase();
  if (method === "get" || form.hasAttribute("data-no-dirty-warning")) return;
  const markDirty = (event) => {
    if (event.target instanceof HTMLInputElement && ["hidden", "submit", "button"].includes(event.target.type)) return;
    dirtyForms.add(form);
  };
  form.addEventListener("input", markDirty);
  form.addEventListener("change", markDirty);
  form.addEventListener("reset", () => dirtyForms.delete(form));
});
window.addEventListener("beforeunload", (event) => {
  if (dirtyForms.size === 0 && dirtyDialogs.size === 0) return;
  event.preventDefault();
  event.returnValue = "";
});

function dirtyDialogForms(dialog) {
  if (!(dialog instanceof HTMLDialogElement)) return [];
  return Array.from(dialog.querySelectorAll("form")).filter((form) => dirtyForms.has(form));
}

function clearDialogDirtyState(dialog) {
  dirtyDialogForms(dialog).forEach((form) => dirtyForms.delete(form));
  dirtyDialogs.delete(dialog);
}

function closeDialogWithDirtyCheck(dialog) {
  const hadFocus = document.activeElement;
  const dirty = dirtyDialogForms(dialog).length > 0 || dirtyDialogs.has(dialog);
  if (dirty && !window.confirm("Ungespeicherte Änderungen verwerfen und Dialog schließen?")) {
    announce("Dialog bleibt geöffnet. Ihre Eingaben wurden nicht verworfen.");
    queueMicrotask(() => {
      if (dialog?.open && hadFocus instanceof HTMLElement) hadFocus.focus();
    });
    return false;
  }
  clearDialogDirtyState(dialog);
  dialog?.close();
  return true;
}

document.querySelectorAll("dialog").forEach((dialog) => {
  const markDirty = (event) => {
    const control = event.target;
    if (!(control instanceof HTMLInputElement || control instanceof HTMLSelectElement || control instanceof HTMLTextAreaElement)) return;
    if (control.closest("form")) return;
    if (control instanceof HTMLInputElement && ["hidden", "submit", "button"].includes(control.type)) return;
    dirtyDialogs.add(dialog);
  };
  dialog.addEventListener("input", markDirty);
  dialog.addEventListener("change", markDirty);
  dialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    closeDialogWithDirtyCheck(dialog);
  });
});

document.addEventListener("submit", (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || String(form.method).toLowerCase() === "get" || form.dataset.allowMultipleSubmit === "true") return;
  if (event.defaultPrevented) return;
  const confirmationMessage = event.submitter?.dataset.confirmMessage;
  if (confirmationMessage && !window.confirm(confirmationMessage)) {
    event.preventDefault();
    announce("Aktion abgebrochen.");
    return;
  }
  const submitters = form.querySelectorAll('button[type="submit"], input[type="submit"]');
  if (form.dataset.submitting === "true") {
    event.preventDefault();
    announce("Diese Änderung wird bereits gespeichert.");
    return;
  }
  form.dataset.submitting = "true";
  submitters.forEach((control) => {
    control.disabled = true;
    control.setAttribute("aria-disabled", "true");
    if (control instanceof HTMLButtonElement && !control.dataset.originalLabel) {
      control.dataset.originalLabel = control.textContent;
      control.textContent = "Wird gespeichert …";
    }
  });
  window.setTimeout(() => {
    if (!event.defaultPrevented) return;
    delete form.dataset.submitting;
    submitters.forEach((control) => {
      control.disabled = false;
      control.removeAttribute("aria-disabled");
      if (control instanceof HTMLButtonElement && control.dataset.originalLabel) {
        control.textContent = control.dataset.originalLabel;
        delete control.dataset.originalLabel;
      }
    });
  }, 0);
});

document.addEventListener("submit", (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement)) return;
  queueMicrotask(() => {
    if (!event.defaultPrevented) dirtyForms.delete(form);
  });
});

window.addEventListener("pageshow", () => {
  document.querySelectorAll('form[data-submitting="true"]').forEach((form) => {
    delete form.dataset.submitting;
    form.querySelectorAll('button[type="submit"], input[type="submit"]').forEach((control) => {
      control.disabled = false;
      control.removeAttribute("aria-disabled");
      if (control instanceof HTMLButtonElement && control.dataset.originalLabel) control.textContent = control.dataset.originalLabel;
    });
  });
});

document.querySelectorAll("form[data-track-change][data-record-id]").forEach((form) => {
  form.addEventListener("submit", (event) => {
    queueMicrotask(() => {
      if (event.defaultPrevented) return;
      try { window.sessionStorage.setItem("hackwerk:changed-record", form.dataset.recordId); } catch { /* Highlight is optional. */ }
    });
  });
});
try {
  const changedRecord = window.sessionStorage.getItem("hackwerk:changed-record");
  if (changedRecord) {
    const changed = document.querySelector(`[data-record-id="${CSS.escape(changedRecord)}"]`);
    if (changed) {
      changed.classList.add("row-changed");
      changed.scrollIntoView({ block: "nearest" });
      window.sessionStorage.removeItem("hackwerk:changed-record");
    }
  }
} catch { /* Session-only visual feedback is optional. */ }

document.addEventListener("keydown", (event) => {
  if (event.key !== "/" || event.ctrlKey || event.metaKey || event.altKey) return;
  const active = document.activeElement;
  if (active && (active.matches("input, textarea, select, [contenteditable='true']"))) return;
  const search = document.querySelector('main input[type="search"]:not(:disabled), main [data-primary-search]:not(:disabled)');
  if (!search) return;
  event.preventDefault();
  search.focus();
  if (typeof search.select === "function") search.select();
  announce("Suche fokussiert.");
});

function highlightRelatedJob(jobID, active) {
  if (!jobID) return;
  document.querySelectorAll(`[data-job-id="${CSS.escape(jobID)}"]`).forEach((element) => {
    element.classList.toggle("related-job-highlight", active);
  });
}

document.addEventListener("pointerover", (event) => {
  const item = event.target.closest?.("[data-job-id]");
  if (item) highlightRelatedJob(item.dataset.jobId, true);
});
document.addEventListener("pointerout", (event) => {
  const item = event.target.closest?.("[data-job-id]");
  if (item && !item.contains(event.relatedTarget)) highlightRelatedJob(item.dataset.jobId, false);
});
document.addEventListener("focusin", (event) => {
  const item = event.target.closest?.("[data-job-id]");
  if (item) highlightRelatedJob(item.dataset.jobId, true);
});
document.addEventListener("focusout", (event) => {
  const item = event.target.closest?.("[data-job-id]");
  if (item && !item.contains(event.relatedTarget)) highlightRelatedJob(item.dataset.jobId, false);
});

document.querySelectorAll("[data-search-highlight]").forEach((element) => {
  const term = String(element.dataset.searchHighlight || new URLSearchParams(window.location.search).get("q") || "").trim();
  if (term.length < 2) return;
  const lowerTerm = term.toLocaleLowerCase("de-AT");
  const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      if (!node.nodeValue?.trim() || node.parentElement?.closest("mark, script, style, button, input, textarea")) return NodeFilter.FILTER_REJECT;
      return node.nodeValue.toLocaleLowerCase("de-AT").includes(lowerTerm) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
    },
  });
  const matches = [];
  while (walker.nextNode()) matches.push(walker.currentNode);
  matches.forEach((node) => {
    const text = node.nodeValue;
    const fragment = document.createDocumentFragment();
    let cursor = 0;
    let index = text.toLocaleLowerCase("de-AT").indexOf(lowerTerm);
    while (index >= 0) {
      fragment.append(document.createTextNode(text.slice(cursor, index)));
      const mark = document.createElement("mark");
      mark.textContent = text.slice(index, index + term.length);
      fragment.append(mark);
      cursor = index + term.length;
      index = text.toLocaleLowerCase("de-AT").indexOf(lowerTerm, cursor);
    }
    fragment.append(document.createTextNode(text.slice(cursor)));
    node.replaceWith(fragment);
  });
});

document.addEventListener("htmx:responseError", () => {
  const status = document.querySelector("[data-live-status]");
  if (status) status.textContent = "Die Anfrage ist fehlgeschlagen. Bitte erneut versuchen.";
});

const errorSummary = document.querySelector("[data-error-summary]");
if (errorSummary instanceof HTMLElement) errorSummary.focus();

if (window.location.hash.length > 1) {
  try {
    const target = document.getElementById(decodeURIComponent(window.location.hash.slice(1)));
    if (target instanceof HTMLDetailsElement) target.open = true;
  } catch (_) {
    // Ignore malformed user-provided fragments; the remaining UI stays usable.
  }
}

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

function viennaDateWithOffset(date, days) {
  const parts = new Intl.DateTimeFormat("sv-SE", {
    timeZone: "Europe/Vienna", year: "numeric", month: "2-digit", day: "2-digit",
  }).formatToParts(date).reduce((values, part) => ({ ...values, [part.type]: part.value }), {});
  const shifted = new Date(Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day) + days, 12));
  return `${shifted.getUTCFullYear()}-${String(shifted.getUTCMonth() + 1).padStart(2, "0")}-${String(shifted.getUTCDate()).padStart(2, "0")}`;
}

function viennaLocalDate(value) {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(String(value || ""));
  if (!match) return null;
  const target = Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]), Number(match[4]), Number(match[5]));
  let timestamp = target;
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const represented = localInputValue(new Date(timestamp));
    const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(represented);
    const representedUTC = Date.UTC(Number(parts[1]), Number(parts[2]) - 1, Number(parts[3]), Number(parts[4]), Number(parts[5]));
    const correction = target - representedUTC;
    if (correction === 0) break;
    timestamp += correction;
  }
  const result = new Date(timestamp);
  return localInputValue(result) === value ? result : null;
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
  if (isError) {
    const reload = document.createElement("a");
    reload.className = "button button--quiet calendar-message__reload";
    reload.href = window.location.pathname + window.location.search;
    reload.textContent = "Kalenderstand neu laden";
    target.append(document.createTextNode(" "), reload);
  }
}

function clearAppointmentError() {
  const target = document.querySelector("[data-appointment-error]");
  if (!target) return;
  target.hidden = true;
  target.textContent = "";
}

function appointmentConflictCause(failure) {
  return ({
    driver_unavailable: "Fahrer-Verfügbarkeit",
    reservation_conflict: "Fahrer- oder Ressourcenreservierung",
    version_conflict: "Zwischenzeitliche Terminänderung",
  })[failure?.code] || "";
}

function showAppointmentError(message, focusTarget, conflictCause = "") {
  const target = document.querySelector("[data-appointment-error]");
  if (target) {
    target.replaceChildren();
    if (conflictCause) {
      const badge = document.createElement("span");
      badge.className = "status-badge status-badge--failed appointment-conflict-badge";
      badge.textContent = "Konflikt";
      badge.title = conflictCause;
      target.append(badge, document.createTextNode(` ${conflictCause}: ${message}`));
    } else {
      target.textContent = message;
    }
    target.hidden = false;
    (focusTarget || target).focus();
  }
  announceCalendar(conflictCause ? `Konflikt – ${conflictCause}: ${message}` : message, true);
}

function showAppointmentFailure(failure) {
  showAppointmentError(failure.message, null, appointmentConflictCause(failure));
}

async function showConflictAlternatives(appointmentID, startsAt, endsAt) {
  if (!appointmentID || !startsAt || !endsAt) return;
  const query = new URLSearchParams({ starts_at: startsAt.toISOString(), ends_at: endsAt.toISOString() });
  const response = await fetch(`/api/v1/appointments/${encodeURIComponent(appointmentID)}/alternatives?${query}`, { credentials: "same-origin", headers: { Accept: "application/json" } });
  if (!response.ok) return;
  const result = await response.json();
  const target = document.querySelector("[data-appointment-error]");
  if (!target) return;
  const block = document.createElement("div"); block.className = "appointment-alternatives";
  const affected = new Map();
  (result.Conflicts || result.conflicts || []).forEach((item) => affected.set(item.AppointmentID || item.appointment_id, `${item.JobNumber || item.job_number || "Termin"} · ${item.CustomerName || item.customer_name || "Kunde"} · ${item.SubjectName || item.subject_name || "Belegung"}`));
  if (affected.size) {
    const heading = document.createElement("strong"); heading.textContent = "Betroffen"; block.append(heading);
    const list = document.createElement("ul"); affected.forEach((label) => { const item = document.createElement("li"); item.textContent = label; list.append(item); }); block.append(list);
  }
  const alternatives = result.Alternatives || result.alternatives || [];
  if (alternatives.length) {
    const heading = document.createElement("strong"); heading.textContent = "Konfliktfreie Alternativen"; block.append(heading);
    const actions = document.createElement("div"); actions.className = "action-row";
    const formatter = new Intl.DateTimeFormat("de-AT", { timeZone: "Europe/Vienna", weekday: "short", day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
    alternatives.forEach((item) => {
      const start = new Date(item.StartsAt || item.starts_at); const end = new Date(item.EndsAt || item.ends_at);
      const button = document.createElement("button"); button.type = "button"; button.className = "button button--quiet"; button.textContent = formatter.format(start);
      button.addEventListener("click", () => {
        const input = document.querySelector("[data-appointment-start]");
        const duration = document.querySelector("[data-appointment-duration]");
        if (input) {
          input.value = localInputValue(start);
          input.dispatchEvent(new Event("input", { bubbles: true }));
        }
        if (duration) {
          duration.value = String(Math.round((end-start)/60000));
          duration.dispatchEvent(new Event("input", { bubbles: true }));
        }
        input?.focus();
      });
      actions.append(button);
    });
    block.append(actions);
  } else { const note = document.createElement("p"); note.textContent = "Im nächsten 14-Tage-Fenster wurde keine konfliktfreie Alternative gefunden."; block.append(note); }
  target.append(block);
}

function clearPlanningError(form) {
  const error = form?.querySelector("[data-planning-error]");
  if (!error) return;
  error.hidden = true;
  error.replaceChildren();
  form.querySelectorAll('[aria-errormessage="planning-error"]').forEach((field) => {
    field.removeAttribute("aria-invalid");
    field.removeAttribute("aria-errormessage");
  });
}

function planningErrorTarget(form, failure) {
  const selector = ({
    driver_unavailable: "#planning-primary-driver",
    reservation_conflict: "#planning-start",
    version_conflict: "#planning-start",
    invalid_local_time: "#planning-start",
  })[failure?.code];
  return (selector ? form.querySelector(selector) : null)
    || form.querySelector(":invalid");
}

function showPlanningError(form, failure) {
  const summary = form?.querySelector("[data-planning-error]");
  if (!summary) return;
  clearPlanningError(form);
  const heading = document.createElement("strong");
  heading.textContent = "Vorschlag konnte nicht gespeichert werden.";
  const message = document.createElement("p");
  message.textContent = failure?.message || "Die Planung konnte nicht gespeichert werden.";
  summary.append(heading, message);
  const field = planningErrorTarget(form, failure);
  if (field?.id) {
    field.setAttribute("aria-invalid", "true");
    field.setAttribute("aria-errormessage", "planning-error");
    const link = document.createElement("a");
    link.href = `#${field.id}`;
    link.textContent = "Zugehörige Eingabe prüfen";
    link.addEventListener("click", () => field.focus());
    summary.append(link);
  }
  summary.hidden = false;
  summary.focus();
}

function openPlanning(jobID, title, duration, start, transportMode, externalConfirmed) {
  const dialog = document.querySelector("[data-planning-dialog]");
  if (!dialog) return;
  const form = dialog.querySelector("[data-planning-form]");
  form.reset();
  form.querySelector("[data-planning-job]").value = jobID;
  form.querySelector("[data-planning-title]").textContent = title || "Auftrag einplanen";
  form.querySelector("[data-planning-duration]").value = duration || "180";
	form.querySelector("[data-planning-start]").value = start
    ? localInputValue(start)
		: `${viennaDateWithOffset(new Date(), 1)}T08:00`;
	const transport = form.querySelector("[data-planning-transport-resource]");
	const transportNote = form.querySelector("[data-planning-transport-note]");
	if (transport) transport.required = transportMode === "internal";
	if (transportNote) {
		transportNote.textContent = transportMode === "internal"
			? "Interner Transport: Fahrzeug auswählen."
			: transportMode === "external" && externalConfirmed === "true"
				? "Externer Transport ist im Auftrag bestätigt; kein internes Fahrzeug nötig."
				: transportMode === "external"
					? "Externer Transport muss zuerst im Auftrag bestätigt werden."
					: transportMode === "undecided"
						? "Transportmodus muss zuerst im Auftrag festgelegt werden."
						: "";
	}
  clearPlanningError(form);
  dialog.showModal();
}

document.querySelectorAll("[data-plan-job]").forEach((button) => {
  button.addEventListener("click", (event) => {
    event.preventDefault();
		openPlanning(button.dataset.planJob, button.dataset.title, button.dataset.duration, undefined, button.dataset.transportMode, button.dataset.externalConfirmed);
  });
});

const requestedPlanningJob = new URLSearchParams(window.location.search).get("job");
if (requestedPlanningJob) {
  document.querySelector(`[data-plan-job="${CSS.escape(requestedPlanningJob)}"]`)?.click();
}

document.querySelectorAll("[data-dialog-close]").forEach((button) => {
  button.addEventListener("click", () => closeDialogWithDirtyCheck(button.closest("dialog")));
});

const planningForm = document.querySelector("[data-planning-form]");
if (planningForm) {
  planningForm.addEventListener("submit", async (event) => {
    if (!window.fetch) return;
    event.preventDefault();
    const submit = planningForm.querySelector("button[type='submit']");
    submit.disabled = true;
    clearPlanningError(planningForm);
    try {
      await calendarRequest("/api/v1/calendar/plan", new FormData(planningForm), planningForm.elements.csrf_token.value);
      dirtyForms.delete(planningForm);
      const jobID = planningForm.elements.job_id.value;
      document.querySelector(`[data-calendar-job="${CSS.escape(jobID)}"]`)?.remove();
      planningForm.closest("dialog")?.close();
      window.hackWerkCalendar?.refetchEvents();
      announceCalendar("Terminvorschlag gespeichert. Der Termin ist noch nicht fixiert.");
    } catch (failure) {
      showPlanningError(planningForm, failure);
    } finally {
      submit.disabled = false;
    }
  });
}

async function loadAppointmentDetail(appointmentID) {
  const response = await fetch(`/api/v1/appointments/${encodeURIComponent(appointmentID)}`, {
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (!response.ok) {
    const failure = await response.json().catch(() => ({}));
    throw new Error(failure.message || "Termindetails konnten nicht geladen werden.");
  }
  return response.json();
}

let appointmentDetailSequence = 0;

function maskNotificationTarget(value, channel) {
  const target = String(value || "").trim();
  if (!target) return "nicht hinterlegt";
  if (channel === "E-Mail") {
    const [name, domain] = target.split("@");
    if (!domain) return "ungültig hinterlegt";
    return `${name.slice(0, 1)}***@${domain}`;
  }
  const digits = target.replace(/\s+/g, "");
  return digits.length > 4 ? `${digits.slice(0, 3)}…${digits.slice(-3)}` : "***";
}

function notificationPlanningSummary(props) {
  if (Array.isArray(props.notification_targets) || props.notification_warning || props.notification_suggestion) {
    return {
      targets: (props.notification_targets || []).map((target) => `${target.channel} an ${target.recipient}`),
      reasons: props.notification_warning ? [props.notification_warning] : [],
      alternatives: props.notification_suggestion ? [props.notification_suggestion] : [],
    };
  }
  const preference = String(props.notification_preference || "none");
  const configured = new Set(props.notification_channels || []);
  const desired = preference === "both" ? ["E-Mail", "SMS"] : preference === "email" ? ["E-Mail"] : preference === "sms" ? ["SMS"] : [];
  const targets = [];
  if (configured.has("E-Mail")) targets.push(`E-Mail an ${maskNotificationTarget(props.email, "E-Mail")}`);
  if (configured.has("SMS")) targets.push(`SMS an ${maskNotificationTarget(props.phone, "SMS")}`);
  const reasons = [];
  if (preference === "none") reasons.push("Kundenpräferenz: keine automatische Nachricht");
  desired.forEach((channel) => {
    if (configured.has(channel)) return;
    const contact = channel === "E-Mail" ? props.email : props.phone;
    reasons.push(contact ? `${channel}-Versand ist nicht aktiviert` : `${channel}-Kontaktdaten fehlen`);
  });
  const alternatives = [];
  if (!configured.has("E-Mail") && props.email) alternatives.push("E-Mail-Kontakt ist als mögliche Alternative vorhanden");
  if (!configured.has("SMS") && props.phone) alternatives.push("SMS-Kontakt ist als mögliche Alternative vorhanden");
  return { targets, reasons, alternatives };
}

function notificationDateLabel(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() < 2000) return "";
  return new Intl.DateTimeFormat("de-AT", { timeZone: "Europe/Vienna", dateStyle: "short", timeStyle: "short" }).format(date);
}

function notificationStateLabel(value) {
  return ({ queued: "Eingereiht", sending: "Wird gesendet", retry_wait: "Wartet auf Wiederholung", sent: "Gesendet", failed: "Fehlgeschlagen" })[value] || value;
}

async function appointmentDetail(event, loadedProps) {
  const dialog = document.querySelector("[data-appointment-dialog]");
  if (!dialog) return;
  const requestSequence = ++appointmentDetailSequence;
  let props = loadedProps;
  if (!props) {
    try {
      props = await loadAppointmentDetail(event.id);
    } catch (failure) {
      if (requestSequence !== appointmentDetailSequence) return;
      announceCalendar(failure.message || "Termindetails konnten nicht geladen werden.", true);
      return;
    }
  }
  if (requestSequence !== appointmentDetailSequence) return;
  clearDialogDirtyState(dialog);
  dialog.dataset.appointmentId = event.id;
  dialog.dataset.version = props.version;
  dialog.dataset.lifecycle = props.lifecycle;
  dialog.dataset.notificationChannels = (props.notification_channels || []).join(" und ");
  const planningSummary = notificationPlanningSummary(props);
  dialog.dataset.notificationTargets = planningSummary.targets.join(" und ");
  dialog.dataset.appointmentSummary = `${props.title}; ${props.status_label}`;
  clearAppointmentError();
  dialog.querySelectorAll([
    "[data-appointment-move-override]",
    "[data-without-notification-reason]",
    "[data-confirmation-admin-reason]",
    "[data-appointment-cancel-reason]",
    "[data-appointment-reopen-reason]",
    "[data-appointment-reopen-override]",
    "[data-appointment-complete-override-reason]",
  ].join(",")).forEach((field) => { field.value = ""; });
  const withoutNotification = dialog.querySelector("[data-without-notification]");
  if (withoutNotification) withoutNotification.hidden = (props.notification_channels || []).length > 0;
  dialog.querySelector("[data-appointment-title]").textContent = props.title;
  const detail = dialog.querySelector("[data-appointment-detail]");
  detail.replaceChildren();
  const start = new Date(props.start);
  const end = new Date(props.end);
  const dateTime = new Intl.DateTimeFormat("de-AT", {
    timeZone: "Europe/Vienna", dateStyle: "medium", timeStyle: "short",
  });
  const time = new Intl.DateTimeFormat("de-AT", {
    timeZone: "Europe/Vienna", hour: "2-digit", minute: "2-digit",
  });
  const channels = (props.notification_channels || []).join(" und ") || "Keine automatische Benachrichtigung";
  const rows = [
    ["Status", props.status_label],
    ["Zeit", `${dateTime.format(start)} – ${time.format(end)}`],
    ["Ort", props.locality], ["Menge", `${props.volume_m3} m³`],
    ["Fahrer", (props.drivers || []).map((item) => item.Name).join(", ") || "Nicht zugewiesen"],
    ["Ressourcen", (props.resources || []).map((item) => item.Name).join(", ") || "Nicht zugewiesen"],
    ["Telefon", props.phone || "Nicht hinterlegt"], ["E-Mail", props.email || "Nicht hinterlegt"],
    ["Versand bei Fixierung", channels],
    ["Versandziel", planningSummary.targets.join(" · ") || "Kein Versandziel verfügbar"],
    ["E-Mail-Betreff", "Ihr HackWerk-Termin"],
  ];
  if ((props.notes || []).length === 0) rows.push(["Interne Bemerkungen", "Noch keine Bemerkung vorhanden"]);
  (props.notes || []).forEach((note) => {
    const author = note.AuthorName || "Unbekannt";
    const created = notificationDateLabel(note.CreatedAt);
    rows.push([`Bemerkung · ${author}${created ? ` · ${created}` : ""}`, note.Body]);
  });
  planningSummary.reasons.forEach((reason) => rows.push(["Warum kein Wunschkanal?", reason]));
  planningSummary.alternatives.forEach((alternative) => rows.push(["Alternative (nur Vorschlag)", alternative]));
  (props.notifications || []).forEach((item) => {
    const timeline = [
      notificationStateLabel(item.status), item.recipient,
      `Versuch ${item.attempt_count}/${item.max_attempts}`,
      notificationDateLabel(item.created_at) ? `erstellt ${notificationDateLabel(item.created_at)}` : "",
      item.status === "retry_wait" && notificationDateLabel(item.available_at) ? `nächster Versuch ${notificationDateLabel(item.available_at)}` : "",
      notificationDateLabel(item.sent_at) ? `gesendet ${notificationDateLabel(item.sent_at)}` : "",
      notificationDateLabel(item.expires_at) ? `Link gültig bis ${notificationDateLabel(item.expires_at)}` : "",
      notificationDateLabel(item.responded_at) ? `Antwort ${notificationDateLabel(item.responded_at)}` : "",
    ].filter(Boolean);
    rows.push([`Versand ${item.channel === "sms" ? "SMS" : "E-Mail"}`, timeline.join(" · ")]);
  });
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
  if (props.customer_id && props.job_id) {
    const job = document.createElement("a");
    job.className = "button button--quiet";
    job.href = `/customers/${encodeURIComponent(props.customer_id)}#job-${encodeURIComponent(props.job_id)}`;
    job.dataset.appointmentJobLink = "";
    job.textContent = "Auftrag öffnen";
    detail.append(job);
  }
  const fix = dialog.querySelector("[data-appointment-fix]");
  const cancel = dialog.querySelector("[data-appointment-cancel]");
  const reschedule = dialog.querySelector("[data-appointment-reschedule]");
	const swapPanel = dialog.querySelector("[data-appointment-swap-panel]");
  const confirmationAdmin = dialog.querySelector("[data-confirmation-admin]");
  const reissue = dialog.querySelector("[data-confirmation-reissue]");
  const resetConfirmation = dialog.querySelector("[data-confirmation-reset]");
	const reopenPanel = dialog.querySelector("[data-appointment-reopen-panel]");
  const completePanel = dialog.querySelector("[data-appointment-complete-panel]");
  const completeOverride = dialog.querySelector("[data-appointment-complete-override]");
  if (fix) fix.hidden = !props.can_fix;
  if (cancel) cancel.hidden = !props.can_cancel;
  if (reschedule) reschedule.hidden = !props.can_reschedule;
	if (swapPanel) swapPanel.hidden = !props.can_swap;
	const swapTarget = dialog.querySelector("[data-appointment-swap-target]");
	if (swapTarget) {
	  swapTarget.replaceChildren(new Option("Bitte wählen", ""));
	  (window.hackWerkCalendar?.getEvents?.() || []).filter((candidate) => candidate.id !== event.id && ["draft","proposal"].includes(candidate.extendedProps.lifecycle)).forEach((candidate) => {
	    const option = new Option(`${candidate.title} · ${new Intl.DateTimeFormat("de-AT", { timeZone: "Europe/Vienna", dateStyle: "short", timeStyle: "short" }).format(candidate.start)}`, candidate.id);
	    option.dataset.version = candidate.extendedProps.version; swapTarget.add(option);
	  });
	}
  const startInput = dialog.querySelector("[data-appointment-start]");
  const durationInput = dialog.querySelector("[data-appointment-duration]");
  const localStart = localInputValue(start);
  const durationMinutes = String(Math.round((end.getTime() - start.getTime()) / 60000));
  dialog.dataset.originalStart = localStart;
  dialog.dataset.originalDuration = durationMinutes;
  if (startInput) startInput.value = localStart;
  if (durationInput) durationInput.value = durationMinutes;
  if (confirmationAdmin) confirmationAdmin.hidden = !props.can_reissue;
  if (reissue) reissue.hidden = !props.can_reissue;
  if (resetConfirmation) resetConfirmation.hidden = !props.can_reset_confirmation;
	if (reopenPanel) reopenPanel.hidden = !props.can_reopen;
  if (completePanel) completePanel.hidden = !props.can_complete;
  if (completeOverride) completeOverride.hidden = !props.complete_requires_override;
  dialog.dataset.completeRequiresOverride = props.complete_requires_override ? "true" : "false";
  dialog.showModal();
}

document.querySelectorAll("[data-appointment-close]").forEach((button) => {
  button.addEventListener("click", () => {
    if (closeDialogWithDirtyCheck(button.closest("dialog"))) appointmentDetailSequence += 1;
  });
});

async function appointmentAction(action, extra = {}) {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const csrf = dialog.querySelector("[data-appointment-csrf]").value;
  const form = new FormData();
  form.set("csrf_token", csrf); form.set("version", dialog.dataset.version);
  Object.entries(extra).forEach(([key, value]) => form.set(key, value));
  const result = await calendarRequest(`/api/v1/appointments/${encodeURIComponent(dialog.dataset.appointmentId)}/${action}`, form, csrf);
  clearDialogDirtyState(dialog);
  dialog.close(); window.hackWerkCalendar?.refetchEvents();
  const message = action === "fix"
    ? "Termin wurde ausdrücklich fixiert; der Versand erfolgt später über die Outbox."
    : action === "complete"
      ? "Termin und Auftrag wurden als erledigt markiert."
      : "Termin wurde aktualisiert.";
  announceCalendar(message);
  return result;
}

document.querySelector("[data-appointment-reschedule-submit]")?.addEventListener("click", async () => {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const start = dialog?.querySelector("[data-appointment-start]");
  const duration = dialog?.querySelector("[data-appointment-duration]");
  if (!start?.value || !duration?.value || Number(duration.value) < 15) {
    showAppointmentError("Bitte geben Sie einen gültigen Beginn und eine Dauer ab 15 Minuten ein.", !start?.value ? start : duration);
    return;
  }
  const reason = dialog.querySelector("[data-without-notification-reason]")?.value.trim() || "";
  if (dialog.dataset.lifecycle === "fixed" && !dialog.dataset.notificationChannels && !reason) {
    const reasonInput = dialog.querySelector("[data-without-notification-reason]");
    showAppointmentError("Bitte begründen Sie die Verschiebung ohne Benachrichtigung.", reasonInput);
    return;
  }
  clearAppointmentError();
  try {
    const action = start.value === dialog.dataset.originalStart ? "resize" : "move";
    await appointmentAction(action, {
      starts_at_local: start.value,
      duration_minutes: duration.value,
      override_reason: dialog.querySelector("[data-appointment-move-override]")?.value.trim() || "",
      without_notification_reason: reason,
    });
  } catch (failure) {
	showAppointmentFailure(failure);
	if (failure.code === "reservation_conflict") {
	  const proposedStart = viennaLocalDate(start.value);
	  if (proposedStart) {
	    const proposedEnd = new Date(proposedStart.getTime() + Number(duration.value) * 60000);
	    showConflictAlternatives(dialog.dataset.appointmentId, proposedStart, proposedEnd).catch(() => {});
	  }
	}
  }
});

document.querySelector("[data-appointment-swap]")?.addEventListener("click", async () => {
  const dialog = document.querySelector("[data-appointment-dialog]"); const target = dialog?.querySelector("[data-appointment-swap-target]"); const option = target?.selectedOptions?.[0];
  if (!target?.value || !option?.dataset.version) { showAppointmentError("Bitte wählen Sie einen anderen Entwurf oder Vorschlag.", target); return; }
  if (!window.confirm("Die Zeitfenster beider Vorschläge atomisch tauschen? Es wird kein Termin fixiert und keine Nachricht versendet.")) return;
  try { await appointmentAction("swap", { other_appointment_id: target.value, other_version: option.dataset.version }); } catch (failure) { showAppointmentFailure(failure); }
});

document.querySelectorAll("[data-appointment-duration-adjust]").forEach((button) => {
  button.addEventListener("click", () => {
    const input = document.querySelector("[data-appointment-duration]");
    if (!input) return;
    const current = Number(input.value);
    const adjustment = Number(button.dataset.appointmentDurationAdjust);
    if (!Number.isFinite(current) || !Number.isFinite(adjustment)) return;
    input.value = String(Math.max(15, Math.min(10080, current + adjustment)));
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
});

document.querySelector("[data-appointment-fix]")?.addEventListener("click", async () => {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const channels = dialog?.dataset.notificationChannels || "keinen automatisch konfigurierten Kanal";
  const targets = dialog?.dataset.notificationTargets || "kein erreichbares Ziel";
  const reason = dialog?.querySelector("[data-without-notification-reason]")?.value.trim() || "";
  if (!dialog?.dataset.notificationChannels && !reason) {
    const reasonInput = dialog?.querySelector("[data-without-notification-reason]");
    showAppointmentError("Bitte begründen Sie die Fixierung ohne Benachrichtigung.", reasonInput);
    return;
  }
  if (!window.confirm(`Termin mit den angezeigten Fahrern und Ressourcen fixieren? Versandvormerkung: ${channels}; ${targets}.`)) return;
  clearAppointmentError();
  try { await appointmentAction("fix", { without_notification_reason: reason }); } catch (failure) { showAppointmentFailure(failure); }
});

document.querySelector("[data-appointment-complete]")?.addEventListener("click", async () => {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const reasonInput = dialog?.querySelector("[data-appointment-complete-override-reason]");
  const reason = reasonInput?.value.trim() || "";
  if (dialog?.dataset.completeRequiresOverride === "true" && !reason) {
    showAppointmentError("Bitte begründen Sie den Abschluss vor dem geplanten Terminbeginn.", reasonInput);
    return;
  }
  if (!window.confirm("Termin und Auftrag endgültig als erledigt markieren?")) return;
  clearAppointmentError();
  try { await appointmentAction("complete", { override_reason: reason }); } catch (failure) { showAppointmentFailure(failure); }
});

document.querySelector("[data-appointment-cancel]")?.addEventListener("click", async () => {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const reason = document.querySelector("[data-appointment-cancel-reason]")?.value || "";
  const summary = dialog?.dataset.appointmentSummary || "diesen Termin";
  const targets = dialog?.dataset.notificationTargets || "kein automatisches Versandziel";
  if (!window.confirm(`${summary} wirklich absagen? Begründung: ${reason.trim() || "nicht angegeben"}. Versandziel: ${targets}.`)) return;
  clearAppointmentError();
  try { await appointmentAction("cancel", { reason }); } catch (failure) { showAppointmentFailure(failure); }
});

document.querySelector("[data-appointment-reopen]")?.addEventListener("click", async () => {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const reasonInput = dialog?.querySelector("[data-appointment-reopen-reason]");
  const reason = reasonInput?.value.trim() || "";
  if (!reason) {
    showAppointmentError("Bitte geben Sie eine Begründung für das Wiederöffnen an.", reasonInput);
    return;
  }
  const summary = dialog?.dataset.appointmentSummary || "Diesen abgesagten Termin";
  if (!window.confirm(`${summary} als unverbindlichen Vorschlag wieder öffnen? Begründung: ${reason}. Es wird keine Nachricht versendet.`)) return;
  clearAppointmentError();
  try {
    await appointmentAction("reopen", {
      reason,
      override_reason: dialog?.querySelector("[data-appointment-reopen-override]")?.value.trim() || "",
    });
  } catch (failure) {
    showAppointmentFailure(failure);
  }
});

async function confirmationAdminAction(action, question) {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const reason = dialog?.querySelector("[data-confirmation-admin-reason]")?.value.trim() || "";
  if (!reason) {
    showAppointmentError("Bitte geben Sie eine Begründung an.", dialog?.querySelector("[data-confirmation-admin-reason]"));
    return;
  }
  if (!window.confirm(question)) return;
  clearAppointmentError();
  try { await appointmentAction(`confirmation/${action}`, { reason }); } catch (failure) { showAppointmentFailure(failure); }
}

document.querySelector("[data-confirmation-reissue]")?.addEventListener("click", () => confirmationAdminAction("reissue", "Aktiven Link widerrufen und eine neue Benachrichtigung einreihen?"));
document.querySelector("[data-confirmation-reset]")?.addEventListener("click", () => confirmationAdminAction("reset", "Gespeicherte Kundenantwort wirklich zurücksetzen?"));

function calendarMutation(info, action, csrf) {
	const proposedStart = new Date(info.event.start);
	const proposedEnd = new Date(info.event.end);
  const form = new FormData();
  form.set("csrf_token", csrf);
  form.set("version", info.event.extendedProps.version);
  form.set("starts_at", info.event.start.toISOString());
  form.set("ends_at", info.event.end.toISOString());
  const send = () => calendarRequest(`/api/v1/appointments/${encodeURIComponent(info.event.id)}/${action}`, form, csrf);
  const accept = (payload, message) => {
    if (Number.isInteger(payload?.version)) info.event.setExtendedProp("version", payload.version);
    window.hackWerkCalendar?.refetchEvents();
    announceCalendar(message);
  };
  send().then((payload) => {
    accept(payload, action === "resize" ? "Termindauer gespeichert." : "Terminzeit gespeichert.");
  }).catch(async (failure) => {
    if (failure.code === "driver_unavailable") {
      const reason = window.prompt("Fahrer ist nicht verfügbar. Begründung für Admin-Override eingeben oder abbrechen:");
      if (reason?.trim()) {
        form.set("override_reason", reason.trim());
        try {
          const payload = await send();
          accept(payload, "Terminzeit mit begründetem Verfügbarkeits-Override gespeichert.");
          return;
        } catch (retryFailure) { failure = retryFailure; }
      }
    }
    info.revert();
    const cause = appointmentConflictCause(failure);
    announceCalendar(`${cause ? `Konflikt – ${cause}: ` : ""}${failure.message} Die Änderung wurde zurückgesetzt.`, true);
	if (failure.code === "reservation_conflict") showConflictAlternatives(info.event.id, proposedStart, proposedEnd).catch(() => {});
  });
}

const calendarElement = document.querySelector("[data-calendar]");
if (calendarElement && window.FullCalendar) {
  const editable = calendarElement.dataset.editable === "true";
  const compact = window.matchMedia("(max-width: 680px)").matches;
  const calendarParameters = new URLSearchParams(window.location.search);
  const requestedAppointment = calendarParameters.get("appointment");
  const requestedView = { day: "timeGridDay", week: "timeGridWeek", month: "dayGridMonth", agenda: "listWeek" }[calendarParameters.get("view")]
    || (compact ? "timeGridDay" : "timeGridWeek");
  const requestedWeekends = calendarParameters.get("weekends") !== "false";
  const viewParameter = (view) => ({ timeGridDay: "day", timeGridWeek: "week", dayGridMonth: "month", listWeek: "agenda" }[view] || "week");
  const calendar = new FullCalendar.Calendar(calendarElement, {
    themeSystem: "classic",
    locale: "de-AT",
    timeZone: "Europe/Vienna",
    firstDay: 1,
    initialView: requestedView,
    initialDate: calendarParameters.get("date") || undefined,
    weekends: requestedWeekends,
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
    droppable: editable,
    eventStartEditable: editable,
    eventDurationEditable: editable,
    eventResizableFromStart: editable,
    snapDuration: "00:15:00",
    dayMaxEvents: compact ? 1 : 3,
    moreLinkText: (count) => `+${count} weitere`,
    headerToolbar: { start: "prev,next today", center: "title", end: compact ? "timeGridDay,dayGridMonth,listWeek" : "timeGridDay,timeGridWeek,dayGridMonth,listWeek" },
    buttons: {
      today: { text: "Heute" },
      timeGridDay: { text: "Tag" },
      timeGridWeek: { text: "Woche" },
      dayGridMonth: { text: "Monat" },
      listWeek: { text: "Agenda" },
    },
    events(info, success, failure) {
      const url = new URL(calendarElement.dataset.eventSource, window.location.origin);
      url.searchParams.set("from", info.start.toISOString());
      url.searchParams.set("to", info.end.toISOString());
      fetch(url, { credentials: "same-origin", headers: { Accept: "application/json" } })
        .then((response) => response.ok ? response.json() : Promise.reject(new Error("Kalenderdaten nicht verfügbar")))
        .then((events) => {
          success(events);
          if (calendarElement.dataset.loadFailed === "true") {
            delete calendarElement.dataset.loadFailed;
            announceCalendar("Kalenderdaten wurden wieder geladen.");
          }
        }).catch((loadFailure) => {
          calendarElement.dataset.loadFailed = "true";
          announceCalendar("Kalenderdaten konnten nicht geladen werden. Bitte versuchen Sie es erneut.", true);
          failure(loadFailure);
        });
    },
    eventDrop(info) { calendarMutation(info, "move", calendarElement.dataset.csrf); },
    eventResize(info) { calendarMutation(info, "resize", calendarElement.dataset.csrf); },
    eventClick(info) { appointmentDetail(info.event); },
    eventDidMount(info) {
      info.el.tabIndex = 0;
      info.el.setAttribute("role", "button");
      info.el.setAttribute("aria-label", `${info.event.title}, ${info.event.extendedProps.status_label || "Termin"}. Details öffnen`);
      info.el.addEventListener("keydown", (event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        appointmentDetail(info.event);
      });
      const jobID = String(info.event.extendedProps.job_id || "");
      if (jobID) info.el.dataset.jobId = jobID;
      if (editable && info.event.startEditable !== false && info.event.durationEditable !== false) {
        info.el.querySelectorAll("*").forEach((element) => {
          const cursor = window.getComputedStyle(element).cursor;
          if (cursor !== "n-resize" && cursor !== "s-resize") return;
          element.classList.add("calendar-resize-handle");
          element.title = "Ziehen, um den Termin zu verlängern oder zu verkürzen";
        });
      }
    },
    eventContent(info) {
      const content = document.createElement("div");
      content.className = `calendar-event-content${info.view.type === "dayGridMonth" ? " calendar-event-content--month" : ""}`;
      const time = document.createElement("strong"); time.textContent = info.timeText;
      const title = document.createElement("span"); title.textContent = info.event.title;
      const status = document.createElement("small"); status.textContent = info.event.extendedProps.status_label;
      content.append(time, title, status); return { domNodes: [content] };
    },
    drop(info) {
      if (!editable) return;
      openPlanning(info.draggedEl.dataset.calendarJob, info.draggedEl.dataset.title, info.draggedEl.dataset.duration, info.date, info.draggedEl.dataset.transportMode, info.draggedEl.dataset.externalConfirmed);
    },
    datesSet(info) {
      const dateInput = document.querySelector("[data-calendar-date]");
      if (dateInput) dateInput.value = localDateValue(info.view.currentStart);
      const url = new URL(window.location.href);
      url.searchParams.set("date", localDateValue(info.view.currentStart));
      url.searchParams.set("view", viewParameter(info.view.type));
      url.searchParams.set("weekends", String(calendar.getOption("weekends")));
      url.searchParams.delete("appointment");
      window.history.replaceState({}, "", url);
    },
  });
  calendar.render();
  window.hackWerkCalendar = calendar;
  const controls = document.querySelector("[data-calendar-controls]");
  const calendarDate = controls?.querySelector("[data-calendar-date]");
  const weekendToggle = controls?.querySelector("[data-calendar-weekends]");
  if (weekendToggle) {
    weekendToggle.checked = requestedWeekends;
    weekendToggle.addEventListener("change", () => {
      calendar.setOption("weekends", weekendToggle.checked);
      announceCalendar(weekendToggle.checked ? "Wochenenden werden angezeigt." : "Wochenenden sind in dieser Ansicht ausgeblendet.");
    });
  }
  calendarDate?.addEventListener("change", () => {
    if (calendarDate.value) calendar.gotoDate(calendarDate.value);
  });
  controls?.querySelectorAll("[data-calendar-jump]").forEach((button) => {
    button.addEventListener("click", () => {
      const target = new Date();
      if (button.dataset.calendarJump === "tomorrow") target.setDate(target.getDate() + 1);
      if (button.dataset.calendarJump === "next-week") target.setDate(target.getDate() + 7);
      calendar.gotoDate(target);
    });
  });
  if (requestedAppointment) {
    loadAppointmentDetail(requestedAppointment).then(async (props) => {
      const startsAt = new Date(props.start);
      if (!Number.isNaN(startsAt.getTime())) calendar.gotoDate(startsAt);
      await appointmentDetail({ id: requestedAppointment }, props);
      calendarElement.dataset.focusedAppointment = requestedAppointment;
      announceCalendar("Übernommener Vorschlag im Kalender geöffnet.");
    }).catch((failure) => {
      announceCalendar(failure.message || "Der übernommene Vorschlag konnte nicht geöffnet werden.", true);
    });
  }
  const waitlist = document.querySelector("[data-calendar-waitlist]");
  if (editable && waitlist && FullCalendar.Interaction?.Draggable) {
    new FullCalendar.Interaction.Draggable(waitlist, {
      itemSelector: "[data-calendar-job]",
      eventData(element) { return { title: element.dataset.title, duration: { minutes: Number(element.dataset.duration) }, create: false }; },
    });
    waitlist.dataset.dragReady = "true";
  }
}

const popoverMenus = Array.from(document.querySelectorAll("[data-mobile-menu], [data-popover-menu]"));
popoverMenus.forEach((menu) => {
  const summary = menu.querySelector("summary");
  menu.addEventListener("toggle", () => {
    if (!menu.open) return;
    popoverMenus.forEach((otherMenu) => {
      if (otherMenu !== menu) otherMenu.open = false;
    });
  });
  menu.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !menu.open) return;
    menu.open = false;
    summary?.focus();
  });
  document.addEventListener("pointerdown", (event) => {
    if (menu.open && !menu.contains(event.target)) menu.open = false;
  });
});

const dashboardStarts = Array.from(document.querySelectorAll("[data-dashboard-start]"));
function updateDashboardCountdown() {
  if (document.hidden || dashboardStarts.length === 0) return;
  document.querySelectorAll("[data-dashboard-countdown]").forEach((node) => node.remove());
  const now = Date.now();
  const next = dashboardStarts
    .map((node) => ({ node, startsAt: Date.parse(node.dataset.dashboardStart) }))
    .filter((item) => Number.isFinite(item.startsAt) && item.startsAt > now)
    .sort((left, right) => left.startsAt - right.startsAt)[0];
  if (!next) return;
  const minutes = Math.max(1, Math.ceil((next.startsAt - now) / 60000));
  const label = document.createElement("small");
  label.dataset.dashboardCountdown = "true";
  label.className = "dashboard-countdown";
  label.textContent = minutes < 60 ? `beginnt in ${minutes} Min.` : `beginnt in ${Math.floor(minutes / 60)} Std. ${minutes % 60} Min.`;
  next.node.parentElement?.append(label);
}
updateDashboardCountdown();
window.setInterval(updateDashboardCountdown, 30000);
document.addEventListener("visibilitychange", updateDashboardCountdown);

document.querySelectorAll("[data-copy-source]").forEach((button) => {
  button.addEventListener("click", async () => {
    const input = document.getElementById(button.dataset.copySource);
    if (!input) return;
    const original = button.textContent;
    try {
      await navigator.clipboard.writeText(input.value);
      button.textContent = "Kopiert";
    } catch {
      input.select();
      document.execCommand("copy");
      button.textContent = "Kopiert";
    }
    announce("In die Zwischenablage kopiert.");
    window.setTimeout(() => { button.textContent = original; }, 1800);
  });
});

document.querySelectorAll("[data-copy-value]").forEach((button) => {
  button.addEventListener("click", async () => {
    const value = String(button.dataset.copyValue || "").trim();
    if (!value) return;
    const original = button.textContent;
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      const fallback = document.createElement("textarea");
      fallback.value = value;
      fallback.className = "visually-hidden";
      document.body.append(fallback);
      fallback.select();
      document.execCommand("copy");
      fallback.remove();
    }
    button.textContent = "Kopiert";
    announce("In die Zwischenablage kopiert.");
    window.setTimeout(() => { button.textContent = original; }, 1800);
  });
});

document.querySelectorAll("[data-print], [data-print-page]").forEach((button) => {
  button.addEventListener("click", () => window.print());
});

document.querySelectorAll("textarea[maxlength]").forEach((field) => {
  const counter = document.createElement("small");
  counter.className = "character-counter";
  counter.setAttribute("aria-live", "polite");
  const update = () => { counter.textContent = `${field.value.length}/${field.maxLength} Zeichen`; };
  field.insertAdjacentElement("afterend", counter);
  field.addEventListener("input", update);
  update();
});

function formControl(button, selector, fallbackName) {
  const form = button.closest("form");
  if (!form) return null;
  return form.querySelector(selector) || (fallbackName ? form.elements.namedItem(fallbackName) : null);
}

document.querySelectorAll("[data-volume-preset]").forEach((button) => {
  button.addEventListener("click", () => {
    const field = formControl(button, "[data-volume-input]", "volume_m3");
    if (!field) return;
    field.value = button.dataset.volumePreset;
    field.dispatchEvent(new Event("input", { bubbles: true }));
    field.focus();
  });
});

document.querySelectorAll("[data-duration-adjust]").forEach((button) => {
  button.addEventListener("click", () => {
    const field = formControl(button, `[data-duration-input="${CSS.escape(button.dataset.durationTarget || "hack")}"]`, "hack_duration");
    if (!field) return;
    const parts = String(field.value || "0").split(":").map(Number);
    const current = parts.length === 2 && parts.every(Number.isFinite) ? parts[0] * 60 + parts[1] : Number(field.value || 0);
    const next = Math.max(15, (Number.isFinite(current) ? current : 0) + Number(button.dataset.durationAdjust || 0));
    field.value = `${Math.floor(next / 60)}:${String(next % 60).padStart(2, "0")}`;
    field.dispatchEvent(new Event("input", { bubbles: true }));
    field.focus();
  });
});

function localDateValue(date) {
  const parts = new Intl.DateTimeFormat("sv-SE", { timeZone: "Europe/Vienna", year: "numeric", month: "2-digit", day: "2-digit" })
    .formatToParts(date).reduce((values, part) => ({ ...values, [part.type]: part.value }), {});
  return `${parts.year}-${parts.month}-${parts.day}`;
}

document.querySelectorAll("[data-date-range-preset]").forEach((button) => {
  button.addEventListener("click", () => {
    const form = button.closest("form");
    const start = form?.elements.namedItem("preferred_start");
    const end = form?.elements.namedItem("preferred_end");
    if (!start || !end) return;
    const today = new Date();
    const offset = Number(button.dataset.startOffset || 0);
    const span = Number(button.dataset.daySpan || 0);
    start.value = viennaDateWithOffset(today, offset);
    end.value = viennaDateWithOffset(today, offset + span);
    start.dispatchEvent(new Event("change", { bubbles: true }));
    end.dispatchEvent(new Event("change", { bubbles: true }));
  });
});

document.querySelectorAll("[data-copy-customer-region]").forEach((button) => {
  button.addEventListener("click", () => {
    const field = formControl(button, "[name='region']", "region");
    if (!field) return;
    field.value = button.dataset.copyCustomerRegion || "";
    field.dispatchEvent(new Event("input", { bubbles: true }));
    field.focus();
  });
});

document.querySelectorAll("[data-note-template]").forEach((button) => {
  button.addEventListener("click", () => {
    const field = formControl(button, "textarea[name='body'], textarea[name='note']", "body");
    if (!field) return;
    const text = String(button.dataset.noteTemplate || "").trim();
    field.value = [field.value.trim(), text].filter(Boolean).join("\n");
    field.dispatchEvent(new Event("input", { bubbles: true }));
    field.focus();
  });
});

function durationMinutes(value) {
  const normalized = String(value || "").trim();
  if (!normalized) return 0;
  const parts = normalized.split(":").map(Number);
  if (parts.length === 2 && parts.every(Number.isFinite)) return Math.max(0, parts[0] * 60 + parts[1]);
  const minutes = Number(normalized);
  return Number.isFinite(minutes) ? Math.max(0, minutes) : 0;
}

document.querySelectorAll("[data-job-duration-summary]").forEach((summary) => {
  const form = summary.closest("form");
  const update = () => {
    const hack = durationMinutes(form?.elements.namedItem("hack_duration")?.value);
    const transport = durationMinutes(form?.elements.namedItem("transport_duration")?.value);
    const total = hack + transport;
    summary.textContent = total ? `Gesamtdauer: ${Math.floor(total / 60)} Std. ${total % 60} Min. (Hackarbeit ${hack} Min., Transport ${transport} Min.)` : "Gesamtdauer wird aus Hack- und Transportzeit berechnet.";
  };
  form?.querySelectorAll("[name='hack_duration'], [name='transport_duration']").forEach((field) => field.addEventListener("input", update));
  update();
});

document.querySelectorAll("[data-availability-preset]").forEach((button) => {
  button.addEventListener("click", () => {
    const form = button.closest("form");
    const from = form?.elements.namedItem("starts_at_local") || form?.elements.namedItem("start_time") || form?.elements.namedItem("local_start");
    const until = form?.elements.namedItem("ends_at_local") || form?.elements.namedItem("end_time") || form?.elements.namedItem("local_end");
    if (!from || !until) return;
    const presets = { morning: ["07:00", "12:00"], afternoon: ["12:00", "18:00"], day: ["07:00", "18:00"] };
    const values = presets[button.dataset.availabilityPreset];
    if (!values) return;
    const setTime = (field, value) => {
      field.value = field.type === "datetime-local" && field.value.includes("T") ? `${field.value.split("T")[0]}T${value}` : value;
      field.dispatchEvent(new Event("change", { bubbles: true }));
    };
    setTime(from, values[0]); setTime(until, values[1]);
  });
});

function clockMinutes(value) {
  const [hour, minute] = String(value || "").split(":").map(Number);
  return Number.isFinite(hour) && Number.isFinite(minute) ? hour * 60 + minute : Number.NaN;
}

document.querySelectorAll("[data-availability-rule-form]").forEach((form) => {
  const hint = form.querySelector("[data-availability-overlap-hint]");
  const conflicts = () => {
    const weekday = String(form.elements.namedItem("weekday")?.value || "");
    const start = clockMinutes(form.elements.namedItem("local_start")?.value);
    const end = clockMinutes(form.elements.namedItem("local_end")?.value);
    if (!weekday || !Number.isFinite(start) || !Number.isFinite(end) || start >= end) return [];
    return Array.from(document.querySelectorAll(`[data-availability-rule][data-weekday="${CSS.escape(weekday)}"]`)).filter((rule) => {
      const existingStart = clockMinutes(rule.dataset.localStart);
      const existingEnd = clockMinutes(rule.dataset.localEnd);
      return Number.isFinite(existingStart) && Number.isFinite(existingEnd) && start < existingEnd && end > existingStart;
    });
  };
  const update = () => {
    const count = conflicts().length;
    if (!hint) return;
    hint.classList.toggle("warning-copy", count > 0);
    hint.textContent = count > 0
      ? `Dieser Zeitraum überschneidet sich mit ${count} bestehender Regel${count === 1 ? "" : "n"}. Zeiten oder Gültigkeitsbereich anpassen; der Server prüft erneut.`
      : "Keine sichtbare Zeitüberschneidung. HackWerk prüft beim Speichern erneut einschließlich Gültigkeitszeiträumen.";
  };
  form.addEventListener("input", update);
  form.addEventListener("change", update);
  form.addEventListener("submit", (event) => {
    if (conflicts().length === 0) return;
    event.preventDefault();
    delete form.dataset.submitting;
    form.querySelectorAll('button[type="submit"]').forEach((button) => { button.disabled = false; });
    update();
    hint?.focus();
  });
  if (hint) hint.tabIndex = -1;
  update();
});

document.querySelectorAll("[data-clear-availability-day]").forEach((form) => {
  form.addEventListener("submit", (event) => {
    if (!window.confirm("Alle sichtbaren Verfügbarkeitsregeln dieses Tages wirklich gemeinsam löschen?")) event.preventDefault();
  });
});

document.querySelectorAll("[data-route-date-preset]").forEach((button) => {
  button.addEventListener("click", () => {
    const input = document.querySelector(button.dataset.routeDateTarget || "[data-route-day-filter]");
    if (!input) return;
    const date = new Date();
    if (button.dataset.routeDatePreset === "tomorrow") date.setDate(date.getDate() + 1);
    if (button.dataset.routeDatePreset === "business-day") {
      do { date.setDate(date.getDate() + 1); } while ([0, 6].includes(date.getDay()));
    }
    input.value = localDateValue(date);
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
});

document.querySelectorAll("[data-planning-workbench]").forEach((workbench) => {
  const rows = Array.from(workbench.querySelectorAll("[data-planning-order]"));
  const search = workbench.querySelector("[data-planning-search]");
  const radius = workbench.querySelector("[data-planning-radius]");
	const region = workbench.querySelector("[data-planning-region]");
  const single = workbench.querySelector("[data-planning-single]");
  const singleSubmit = workbench.querySelector("[data-planning-single-submit]");
  const routeButton = workbench.querySelector("[data-planning-route]");
  const detail = workbench.querySelector("[data-planning-detail-panel]");
  const results = workbench.querySelector("[data-planning-results]");
  const planningSteps = Array.from(workbench.querySelectorAll("[data-planning-step]"));
  const setCurrentStep = (number) => planningSteps.forEach((step) => {
    const current = step.dataset.planningStep === String(number);
    step.closest("li")?.classList.toggle("is-active", current);
    if (current) step.setAttribute("aria-current", "step");
    else step.removeAttribute("aria-current");
  });
  planningSteps.filter((step) => step.matches("a[href^='#']")).forEach((step) => {
    step.addEventListener("click", (event) => {
      const target = document.querySelector(step.hash);
      if (!target) return;
      event.preventDefault();
      window.history.pushState(null, "", step.hash);
      setCurrentStep(step.dataset.planningStep);
      target.focus({ preventScroll: true });
      target.scrollIntoView({ block: "start", behavior: "auto" });
    });
  });
  workbench.querySelectorAll("form[action$='/adopt']").forEach((form) => {
    form.addEventListener("submit", () => setCurrentStep(4));
  });
  const radiusOrigin = mapPoint(workbench.dataset.planningRadiusLatitude, workbench.dataset.planningRadiusLongitude);
  if (radius && !radiusOrigin) {
    radius.value = "";
    radius.disabled = true;
    radius.setAttribute("aria-describedby", "planning-radius-unavailable");
    const hint = document.createElement("small");
    hint.id = "planning-radius-unavailable";
    hint.className = "form-hint";
    hint.textContent = "Radiusfilter ist ohne konfigurierten Standard-Startort nicht verfügbar.";
    radius.closest("label")?.append(hint);
  }
  const radians = (value) => value * Math.PI / 180;
  const distanceKM = (row) => {
    const latitude = Number(row.dataset.latitude); const longitude = Number(row.dataset.longitude);
    if (!Number.isFinite(latitude) || !Number.isFinite(longitude)) return Number.POSITIVE_INFINITY;
    if (!radiusOrigin) return Number.POSITIVE_INFINITY;
    const dLat = radians(latitude - radiusOrigin.latitude); const dLon = radians(longitude - radiusOrigin.longitude);
    const a = Math.sin(dLat / 2) ** 2 + Math.cos(radians(radiusOrigin.latitude)) * Math.cos(radians(latitude)) * Math.sin(dLon / 2) ** 2;
    return 6371 * 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a));
  };
  const visible = (row) => {
    const query = String(search?.value || "").trim().toLocaleLowerCase("de-AT");
    const maximum = Number(radius?.value || 0);
	const selectedRegion = String(region?.value || "");
    return (!query || String(row.dataset.search || "").includes(query)) && (!selectedRegion || row.dataset.region === selectedRegion) && (!maximum || (radiusOrigin && distanceKM(row) <= maximum));
  };
  const renderFilters = () => rows.forEach((row) => { row.hidden = !visible(row); });
  const checkedRows = () => rows.filter((row) => row.querySelector('input[name="job_id"]')?.checked);
  const update = ({ syncSingle = true } = {}) => {
    const selected = checkedRows();
    const volume = selected.reduce((sum, row) => sum + (Number(String(row.dataset.volume || "0").replace(",", ".")) || 0), 0);
    const duration = selected.reduce((sum, row) => sum + (Number(row.dataset.durationMinutes) || 0), 0);
    const set = (selector, value) => { const target = workbench.querySelector(selector); if (target) target.textContent = value; };
    set("[data-planning-count]", String(selected.length));
    set("[data-planning-volume]", `${volume.toLocaleString("de-AT", { maximumFractionDigits: 2 })} m³`);
    set("[data-planning-duration]", duration >= 60 ? `${Math.floor(duration / 60)} Std. ${duration % 60} Min.` : `${duration} Min.`);
    set("[data-planning-selection-title]", selected.length === 0 ? "Noch nichts gewählt" : selected.length === 1 ? selected[0].dataset.label : `${selected.length} Aufträge gewählt`);
    if (routeButton) routeButton.disabled = selected.length === 0;
    if (syncSingle && single) single.value = selected.length === 1 ? selected[0].dataset.jobId : "";
    if (singleSubmit) singleSubmit.disabled = !single?.value;
    rows.forEach((row) => row.classList.toggle("route-candidate--selected", selected.includes(row)));
    setCurrentStep(results ? 3 : selected.length === 1 ? 2 : 1);
  };
  rows.forEach((row) => {
    row.querySelector('input[name="job_id"]')?.addEventListener("change", update);
    row.querySelector("[data-planning-detail]")?.addEventListener("click", () => {
      const selectedBox = row.querySelector('input[name="job_id"]');
      if (selectedBox && !selectedBox.disabled) {
        rows.forEach((candidate) => {
          const box = candidate.querySelector('input[name="job_id"]');
          if (box) box.checked = candidate === row;
        });
        update();
      }
      if (!detail) return;
      detail.replaceChildren();
      const title = document.createElement("strong"); title.textContent = `${row.dataset.label} · ${row.dataset.customer}`;
      const facts = document.createElement("p"); facts.textContent = `${row.dataset.region || "Ohne Region"} · ${row.dataset.volume || "0"} m³ · ${row.dataset.durationMinutes || "0"} Min. Arbeit`;
      const hint = document.createElement("p"); hint.className = "form-hint"; hint.textContent = "Karte, Verfügbarkeit und Ressourcen werden serverseitig erneut geprüft.";
      detail.append(title, facts, hint);
      row.querySelector('input[name="job_id"]')?.focus();
    });
  });
	search?.addEventListener("input", renderFilters); radius?.addEventListener("input", renderFilters); region?.addEventListener("change", renderFilters);
  single?.addEventListener("change", () => {
    rows.forEach((row) => {
      const box = row.querySelector('input[name="job_id"]');
      if (box) box.checked = Boolean(single.value) && row.dataset.jobId === single.value;
    });
    update({ syncSingle: false });
  });
  workbench.querySelector("[data-planning-select-visible]")?.addEventListener("click", () => { rows.filter(visible).forEach((row) => { const box = row.querySelector('input[name="job_id"]'); if (box && !box.disabled) box.checked = true; }); update(); });
	workbench.querySelector("[data-planning-reset]")?.addEventListener("click", () => { rows.forEach((row) => { const box = row.querySelector('input[name="job_id"]'); if (box) box.checked = false; row.hidden = false; }); if (search) search.value = ""; if (radius) radius.value = ""; if (region) region.value = ""; update(); });
  if (single?.value) {
    const selectedRow = rows.find((row) => row.dataset.jobId === single.value);
    const selectedBox = selectedRow?.querySelector('input[name="job_id"]');
    if (selectedBox && !selectedBox.disabled) selectedBox.checked = true;
  }
  renderFilters(); update({ syncSingle: false });
  if (results && !window.location.hash) requestAnimationFrame(() => {
    results.focus({ preventScroll: true });
    results.scrollIntoView({ block: "start", behavior: "auto" });
  });
  workbench.dataset.planningReady = "true";
});

document.querySelectorAll(".parallel-move-form").forEach((form) => {
  const target = form.querySelector("[data-route-target]");
  const version = form.querySelector("[data-target-version]");
  const update = () => { if (version) version.value = target?.selectedOptions?.[0]?.dataset.version || ""; };
  target?.addEventListener("change", update);
  form.addEventListener("submit", (event) => {
    update();
    if (!target?.value || !version?.value) { event.preventDefault(); target?.focus(); }
  });
  update();
});

document.querySelectorAll("[data-route-date]").forEach((button) => {
  button.addEventListener("click", () => {
    const form = button.closest("form");
    const departure = form?.elements.namedItem("departure");
    if (!departure) return;
    const currentTime = String(departure.value || "").split("T")[1] || "07:00";
    departure.value = `${button.dataset.routeDate}T${currentTime}`;
    departure.dispatchEvent(new Event("change", { bubbles: true }));
    departure.focus();
  });
});

document.querySelectorAll("[data-wake-lock]").forEach((button) => {
  let lock;
  const update = () => {
    button.textContent = lock ? "Bildschirm darf schlafen" : "Bildschirm wach halten";
    button.setAttribute("aria-pressed", String(Boolean(lock)));
  };
  const release = async () => {
    if (!lock) return;
    try { await lock.release(); } catch { /* The browser may already have released it. */ }
    lock = undefined; update();
  };
  button.hidden = !("wakeLock" in navigator);
  button.addEventListener("click", async () => {
    if (lock) { await release(); return; }
    try {
      lock = await navigator.wakeLock.request("screen");
      lock.addEventListener("release", () => { lock = undefined; update(); }, { once: true });
      announce("Der Bildschirm bleibt für diese geöffnete Route wach.");
    } catch { announce("Der Bildschirm kann in diesem Browser nicht wach gehalten werden."); }
    update();
  });
  window.addEventListener("pagehide", release);
  document.addEventListener("visibilitychange", () => { if (document.hidden) release(); });
  update();
});

const voiceCapture = document.querySelector("[data-voice-capture]");
if (voiceCapture) {
  const startButton = voiceCapture.querySelector("[data-voice-start]");
  const pauseButton = voiceCapture.querySelector("[data-voice-pause]");
  const stopButton = voiceCapture.querySelector("[data-voice-stop]");
  const cancelButton = voiceCapture.querySelector("[data-voice-cancel]");
  const timer = voiceCapture.querySelector("[data-voice-timer]");
  const status = voiceCapture.querySelector("[data-voice-status]");
  const uploadForm = voiceCapture.querySelector("[data-voice-upload]");
  const maxSeconds = Number(voiceCapture.dataset.maxSeconds);
  let recorder;
  let stream;
  let chunks = [];
  let accumulatedMs = 0;
  let segmentStartedAt = 0;
  let interval;
  let cancelled = false;

  const announce = (message) => { status.textContent = message; };
  const resetControls = () => {
    startButton.disabled = false;
    pauseButton.disabled = true;
    pauseButton.textContent = "Pause";
    stopButton.disabled = true;
    cancelButton.disabled = true;
  };
  const stopTracks = () => {
    stream?.getTracks().forEach((track) => track.stop());
    stream = undefined;
    window.clearInterval(interval);
  };
  const elapsedMs = () => accumulatedMs + (recorder?.state === "recording" && segmentStartedAt ? Date.now() - segmentStartedAt : 0);
  const freezeElapsed = () => {
	if (segmentStartedAt) accumulatedMs += Date.now() - segmentStartedAt;
	accumulatedMs = Math.min(maxSeconds * 1000, accumulatedMs);
    segmentStartedAt = 0;
  };
  const updateTimer = () => {
    const elapsed = Math.min(maxSeconds, Math.max(0, Math.ceil(elapsedMs() / 1000)));
    const minutes = Math.floor(elapsed / 60);
    const seconds = elapsed % 60;
    timer.textContent = `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
    if (elapsed >= maxSeconds && recorder?.state !== "inactive") { freezeElapsed(); recorder.stop(); }
  };
  const uploadAudio = async (blob, durationMs) => {
    const form = new FormData();
    form.append("duration_ms", String(Math.max(1, Math.min(maxSeconds * 1000, durationMs))));
    form.append("audio", blob, "aufnahme.webm");
    announce("Aufnahme wird sicher übertragen und verarbeitet …");
    const response = await fetch("/api/v1/voice/drafts", { method: "POST", headers: { "X-CSRF-Token": voiceCapture.dataset.csrf, Accept: "application/json" }, credentials: "same-origin", body: form });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload?.error?.message || "Die Aufnahme konnte nicht verarbeitet werden.");
    dirtyForms.delete(uploadForm);
    window.location.assign(payload.location);
  };
  const supportedType = () => ["audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus"].find((type) => window.MediaRecorder?.isTypeSupported(type));

  if (!navigator.mediaDevices?.getUserMedia || !window.MediaRecorder || !supportedType()) {
    startButton.disabled = true;
    announce("Dieser Browser unterstützt die Mikrofonaufnahme nicht. Sie können eine Datei hochladen oder vollständig manuell erfassen.");
  } else {
    startButton.addEventListener("click", async () => {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false });
        chunks = [];
        cancelled = false;
		accumulatedMs = 0;
        recorder = new MediaRecorder(stream, { mimeType: supportedType() });
        recorder.addEventListener("dataavailable", (event) => { if (event.data.size) chunks.push(event.data); });
        recorder.addEventListener("stop", async () => {
		  freezeElapsed();
          const durationMs = Math.max(1, accumulatedMs);
          const blob = new Blob(chunks, { type: recorder.mimeType });
          stopTracks(); resetControls();
          if (cancelled) return;
          try { await uploadAudio(blob, durationMs); } catch (failure) { announce(`${failure.message} Audio wurde nicht dauerhaft gespeichert; manuelle Erfassung bleibt möglich.`); }
        }, { once: true });
        recorder.start(1000); segmentStartedAt = Date.now(); updateTimer(); interval = window.setInterval(updateTimer, 500);
        startButton.disabled = true; pauseButton.disabled = false; stopButton.disabled = false; cancelButton.disabled = false;
        announce("Aufnahme läuft sichtbar. Stoppen Sie spätestens nach dem angezeigten Limit.");
      } catch { stopTracks(); resetControls(); announce("Mikrofonzugriff wurde nicht erteilt. Datei-Upload und manuelle Erfassung bleiben verfügbar."); }
    });
    pauseButton.addEventListener("click", () => {
      if (recorder?.state === "recording") { freezeElapsed(); recorder.pause(); pauseButton.textContent = "Fortsetzen"; updateTimer(); announce("Aufnahme pausiert."); }
      else if (recorder?.state === "paused") { recorder.resume(); segmentStartedAt = Date.now(); pauseButton.textContent = "Pause"; announce("Aufnahme läuft wieder."); }
    });
    stopButton.addEventListener("click", () => { if (recorder?.state !== "inactive") { freezeElapsed(); recorder.stop(); } });
    cancelButton.addEventListener("click", () => {
      cancelled = true;
      if (recorder?.state !== "inactive") recorder.stop();
      chunks = []; stopTracks(); resetControls(); timer.textContent = "00:00"; announce("Aufnahme verworfen; nichts wurde hochgeladen.");
    });
  }
  uploadForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const audio = uploadForm.elements.audio.files[0];
    const seconds = Number(uploadForm.elements.duration_seconds.value);
    if (!audio || !Number.isFinite(seconds) || seconds <= 0 || seconds > maxSeconds) { announce("Bitte Datei und gültige Dauer innerhalb des Limits angeben."); return; }
    uploadForm.querySelector("button[type='submit']").disabled = true;
    try { await uploadAudio(audio, seconds * 1000); } catch (failure) { announce(`${failure.message} Bitte manuell erfassen oder eine andere Datei wählen.`); uploadForm.querySelector("button[type='submit']").disabled = false; }
  });
  window.addEventListener("pagehide", stopTracks);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden && recorder?.state === "recording") {
      freezeElapsed(); recorder.pause(); pauseButton.textContent = "Fortsetzen"; updateTimer(); announce("Aufnahme beim Tabwechsel pausiert.");
    }
  });
}

const mapLibreState = { promise: null };

function sameOriginMapAsset(value) {
  if (!value) return "";
  try {
    const resolved = new URL(value, window.location.origin);
    return resolved.origin === window.location.origin ? resolved.href : "";
  } catch {
    return "";
  }
}

function mapAssetPaths() {
  const candidates = Array.from(document.querySelectorAll("[data-map-assets]"));
  for (const candidate of candidates) {
    const script = sameOriginMapAsset(candidate.dataset.mapScript);
    const worker = sameOriginMapAsset(candidate.dataset.mapWorker);
    const css = sameOriginMapAsset(candidate.dataset.mapCss);
    const attribution = String(candidate.dataset.mapAttribution || "Kartendaten").trim();
    if (script && worker && css) return { script, worker, css, attribution };
  }
  return null;
}

function ensureMapLibreCSS(path) {
  if (document.querySelector("link[data-maplibre-css]")) return;
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = path;
  link.dataset.maplibreCss = "true";
  document.head.append(link);
}

function loadMapLibre() {
  if (window.maplibregl) return Promise.resolve(window.maplibregl);
  if (mapLibreState.promise) return mapLibreState.promise;
  const assets = mapAssetPaths();
  if (!assets) return Promise.reject(new Error("Kartenbibliothek nicht konfiguriert"));
  ensureMapLibreCSS(assets.css);
  mapLibreState.promise = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = assets.script;
    script.async = true;
    script.dataset.maplibreScript = "true";
    script.addEventListener("load", () => {
      if (!window.maplibregl) {
        reject(new Error("Kartenbibliothek konnte nicht gestartet werden"));
        return;
      }
      window.maplibregl.workerUrl = assets.worker;
      resolve(window.maplibregl);
    }, { once: true });
    script.addEventListener("error", () => reject(new Error("Kartenbibliothek konnte nicht geladen werden")), { once: true });
    document.head.append(script);
  });
  return mapLibreState.promise;
}

function hackWerkRasterStyle() {
  const attributionElement = document.createElement("span");
  attributionElement.textContent = mapAssetPaths()?.attribution || "Kartendaten";
  return {
    version: 8,
    sources: {
      "hackwerk-streets": {
        type: "raster",
        tiles: [`${window.location.origin}/map/tiles/{z}/{x}/{y}`],
        tileSize: 256,
        attribution: attributionElement.innerHTML,
      },
    },
    layers: [{ id: "hackwerk-streets", type: "raster", source: "hackwerk-streets" }],
  };
}

function mapPoint(latitude, longitude) {
  const latitudeValue = String(latitude ?? "").trim();
  const longitudeValue = String(longitude ?? "").trim();
  if (!latitudeValue || !longitudeValue) return null;
  const normalizedLatitude = Number(latitudeValue.replace(",", "."));
  const normalizedLongitude = Number(longitudeValue.replace(",", "."));
  if (!Number.isFinite(normalizedLatitude) || !Number.isFinite(normalizedLongitude)) return null;
  if (normalizedLatitude < -90 || normalizedLatitude > 90 || normalizedLongitude < -180 || normalizedLongitude > 180) return null;
  return { latitude: normalizedLatitude, longitude: normalizedLongitude };
}

function displayCoordinate(value) {
  return Number(value).toFixed(6);
}

function pileMarkerElement() {
  const marker = document.createElement("span");
  marker.className = "pile-map-marker";
  marker.setAttribute("aria-hidden", "true");
  return marker;
}

function markMapReady(canvas) {
  canvas.dataset.mapReady = "true";
  canvas.querySelector("[data-map-fallback]")?.setAttribute("hidden", "");
}

function markMapUnavailable(canvas) {
  if (!canvas) return;
  const fallback = canvas.querySelector("[data-map-fallback]");
  if (!fallback) return;
  fallback.hidden = false;
  fallback.textContent = "Die Straßenkarte ist derzeit nicht verfügbar. Koordinaten und Google-Maps-Link bleiben nutzbar.";
}

function createJobLocationMap(maplibregl, canvas, point, interactive) {
  const hasPoint = Boolean(point);
  const map = new maplibregl.Map({
    container: canvas,
    style: hackWerkRasterStyle(),
    ...(hasPoint ? { center: [point.longitude, point.latitude], zoom: 16 } : {
      bounds: [[9.5, 46.3], [17.3, 49.2]],
      fitBoundsOptions: { padding: 36, maxZoom: 7 },
    }),
    interactive,
    dragRotate: false,
    pitchWithRotate: false,
    attributionControl: true,
  });
  if (interactive) map.addControl(new maplibregl.NavigationControl({ showCompass: false }), "top-right");
  map.on("load", () => markMapReady(canvas));
  if ("ResizeObserver" in window) {
    const resizeObserver = new ResizeObserver(() => map.resize());
    resizeObserver.observe(canvas);
    map.on("remove", () => resizeObserver.disconnect());
  }
  return map;
}

function initializeJobLocationEditor(editor, maplibregl) {
  const canvas = editor.querySelector("[data-map-canvas]");
  const latitudeInput = editor.querySelector("[data-location-latitude]");
  const longitudeInput = editor.querySelector("[data-location-longitude]");
  const committedLatitude = editor.querySelector("[data-location-committed-latitude]");
  const committedLongitude = editor.querySelector("[data-location-committed-longitude]");
  const committedSource = editor.querySelector("[data-location-committed-source]");
  const badge = editor.querySelector("[data-location-badge]");
  const message = editor.querySelector("[data-location-message]");
  if (!canvas || !latitudeInput || !longitudeInput || !committedLatitude || !committedLongitude || !committedSource) return;

  let draftSource = editor.dataset.initialSource || "coordinates";
  let marker = null;
  const initialPoint = mapPoint(editor.dataset.initialLatitude, editor.dataset.initialLongitude);
  const map = maplibregl ? createJobLocationMap(maplibregl, canvas, initialPoint, true) : null;

  const announce = (text, status = "Ungespeichert") => {
    if (message) message.textContent = text;
    if (badge) badge.textContent = status;
  };
  const setInputValidity = (valid) => {
    latitudeInput.setAttribute("aria-invalid", String(!valid));
    longitudeInput.setAttribute("aria-invalid", String(!valid));
  };
  const setMarker = (point, center = true) => {
    if (!map || !maplibregl) return;
    if (!point) {
      marker?.remove();
      marker = null;
      return;
    }
    if (!marker) {
      marker = new maplibregl.Marker({ element: pileMarkerElement(), draggable: true, anchor: "bottom" })
        .setLngLat([point.longitude, point.latitude])
        .addTo(map);
      marker.on("dragend", () => {
        const next = marker.getLngLat();
        latitudeInput.value = displayCoordinate(next.lat);
        longitudeInput.value = displayCoordinate(next.lng);
        draftSource = "map_pin";
        setInputValidity(true);
        announce("Marker verschoben. Mit „Standort übernehmen“ in das Formular übernehmen.");
      });
    } else {
      marker.setLngLat([point.longitude, point.latitude]);
    }
    if (center) map.easeTo({ center: [point.longitude, point.latitude], zoom: Math.max(map.getZoom(), 15) });
  };
  const setDraft = (point, source, text) => {
    latitudeInput.value = displayCoordinate(point.latitude);
    longitudeInput.value = displayCoordinate(point.longitude);
    draftSource = source;
    setInputValidity(true);
    setMarker(point);
    announce(text);
  };
  const readDraft = () => mapPoint(latitudeInput.value, longitudeInput.value);

  const searchInput = editor.querySelector("[data-location-search-input]");
  const searchSubmit = editor.querySelector("[data-location-search-submit]");
  const searchStatus = editor.querySelector("[data-location-search-status]");
  const searchResults = editor.querySelector("[data-location-search-results]");
  const clearSearchResults = () => {
    searchResults?.replaceChildren();
    if (searchResults) searchResults.hidden = true;
  };
  const showSearchStatus = (text) => {
    if (searchStatus) searchStatus.textContent = text;
  };
  const searchAddress = async () => {
    const query = String(searchInput?.value || "").trim();
    if (query.length < 3) {
      clearSearchResults();
      showSearchStatus("Bitte mindestens drei Zeichen eingeben.");
      searchInput?.focus();
      return;
    }
    const csrf = editor.closest("form")?.querySelector("[name='csrf_token']")?.value;
    if (!csrf) {
      showSearchStatus("Die Sicherheitsprüfung ist abgelaufen. Bitte laden Sie die Seite neu.");
      return;
    }
    clearSearchResults();
    searchSubmit.disabled = true;
    searchSubmit.setAttribute("aria-busy", "true");
    showSearchStatus("Adresse wird gesucht …");
    try {
      const response = await fetch("/api/v1/geocoding/search", {
        method: "POST",
        headers: { "X-CSRF-Token": csrf, "Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8" },
        body: new URLSearchParams({ query }),
        credentials: "same-origin",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.error?.message || "Die Adresssuche ist derzeit nicht verfügbar.");
      const results = Array.isArray(payload.results) ? payload.results.slice(0, 10) : [];
      if (results.length === 0) {
        showSearchStatus("Keine passende Adresse gefunden. Versuchen Sie Ort, Straße und Hausnummer gemeinsam.");
        return;
      }
      for (const result of results) {
        const point = mapPoint(result?.latitude, result?.longitude);
        const label = String(result?.label || "").trim();
        if (!point || !label) continue;
        const item = document.createElement("li");
        const button = document.createElement("button");
        button.type = "button";
        button.className = "location-search__result";
        button.textContent = label;
        button.addEventListener("click", () => {
          if (!map) {
            setDraft(point, "coordinates", `Koordinaten aus „${label}“ vorbereitet. Mit „Standort übernehmen“ in das Formular übernehmen.`);
            showSearchStatus(`Koordinaten aus „${label}“ vorbereitet.`);
            latitudeInput.focus({ preventScroll: true });
            return;
          }
          const bounds = Array.isArray(result.bounds) ? result.bounds.map(Number) : [];
          const boundsValid = bounds.length === 4 && bounds.every(Number.isFinite) && bounds[0] <= bounds[1] && bounds[2] <= bounds[3];
          if (boundsValid && (bounds[0] !== bounds[1] || bounds[2] !== bounds[3])) {
            map.fitBounds([[bounds[2], bounds[0]], [bounds[3], bounds[1]]], { padding: 48, maxZoom: 16 });
          } else {
            map.easeTo({ center: [point.longitude, point.latitude], zoom: Math.max(map.getZoom(), 15) });
          }
          showSearchStatus(`Karte auf „${label}“ ausgerichtet.`);
          announce("Karte zur gefundenen Adresse bewegt. Klicken Sie den tatsächlichen Haufenstandort an oder geben Sie die Koordinaten ein.", badge?.textContent || "Fehlt");
        });
        item.append(button);
        searchResults.append(item);
      }
      searchResults.hidden = searchResults.children.length === 0;
      showSearchStatus(searchResults.hidden ? "Keine nutzbaren Treffer erhalten." : `${searchResults.children.length} Treffer gefunden. Wählen Sie eine Adresse.`);
    } catch (error) {
      clearSearchResults();
      showSearchStatus(error instanceof Error ? error.message : "Die Adresssuche ist derzeit nicht verfügbar.");
    } finally {
      searchSubmit.disabled = false;
      searchSubmit.removeAttribute("aria-busy");
    }
  };

  searchSubmit?.addEventListener("click", searchAddress);
  searchInput?.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    searchAddress();
  });

  if (initialPoint) setMarker(initialPoint, false);
  map?.on("click", (event) => setDraft(
    { latitude: event.lngLat.lat, longitude: event.lngLat.lng },
    "map_pin",
    "Position auf der Karte gewählt. Mit „Standort übernehmen“ in das Formular übernehmen.",
  ));

  const coordinateChanged = () => {
    const point = readDraft();
    if (!point) {
      setInputValidity(false);
      announce("Bitte gültige Koordinaten eingeben.", badge?.textContent || "Fehlt");
      return;
    }
    draftSource = "coordinates";
    setInputValidity(true);
    setMarker(point);
    announce("Koordinaten geändert. Mit „Standort übernehmen“ in das Formular übernehmen.");
  };
  latitudeInput.addEventListener("change", coordinateChanged);
  longitudeInput.addEventListener("change", coordinateChanged);

  editor.querySelector("[data-location-customer]")?.addEventListener("click", () => {
    const point = mapPoint(editor.dataset.customerLatitude, editor.dataset.customerLongitude);
    if (!point) {
      announce("Für die Kundenadresse sind keine nutzbaren Koordinaten vorhanden.", badge?.textContent || "Fehlt");
      return;
    }
    setDraft(point, "customer_address", "Kundenadresse geladen. Bitte prüfen und anschließend übernehmen.");
  });

  editor.querySelector("[data-location-device]")?.addEventListener("click", () => {
    if (!navigator.geolocation) {
      announce("Dieses Gerät unterstützt keine Standortbestimmung.", badge?.textContent || "Fehlt");
      return;
    }
    announce("Gerätestandort wird ermittelt …", badge?.textContent || "Fehlt");
    navigator.geolocation.getCurrentPosition(
      (position) => setDraft(
        { latitude: position.coords.latitude, longitude: position.coords.longitude },
        "device_location",
        "Gerätestandort geladen. Bitte prüfen und anschließend übernehmen.",
      ),
      () => announce("Der Gerätestandort konnte nicht ermittelt werden. Setzen Sie den Marker oder geben Sie Koordinaten ein.", badge?.textContent || "Fehlt"),
      { enableHighAccuracy: true, timeout: 10000, maximumAge: 30000 },
    );
  });

  editor.querySelector("[data-location-commit]")?.addEventListener("click", () => {
    const point = readDraft();
    if (!point) {
      setInputValidity(false);
      announce("Bitte zuerst einen gültigen Standort wählen.", badge?.textContent || "Fehlt");
      latitudeInput.focus();
      return;
    }
    const latitude = displayCoordinate(point.latitude);
    const longitude = displayCoordinate(point.longitude);
    latitudeInput.value = latitude;
    longitudeInput.value = longitude;
    committedLatitude.value = latitude;
    committedLongitude.value = longitude;
    committedSource.value = draftSource || "coordinates";
    setInputValidity(true);
    announce("Haufenstandort in das Formular übernommen. Speichern Sie nun den Auftrag.", "Übernommen");
  });

  editor.querySelector("[data-location-clear]")?.addEventListener("click", () => {
    latitudeInput.value = "";
    longitudeInput.value = "";
    committedLatitude.value = "";
    committedLongitude.value = "";
    committedSource.value = "";
    setInputValidity(true);
    setMarker(null);
    announce("Haufenstandort aus dem Formular entfernt. Speichern Sie den Auftrag, um die Änderung zu übernehmen.", "Entfernt");
  });

  if (!map) markMapUnavailable(canvas);
}

function markRouteLocationMapUnavailable(canvas) {
  if (!canvas) return;
  const fallback = canvas.querySelector("[data-map-fallback]");
  if (!fallback) return;
  fallback.hidden = false;
  fallback.textContent = "Die Straßenkarte ist derzeit nicht verfügbar. Koordinaten können weiterhin direkt eingegeben werden.";
}

function initializeRouteLocationCoordinateMap(canvas, maplibregl) {
  const editor = canvas.closest("[data-route-location-editor]");
  const latitudeInput = editor?.querySelector("[data-route-location-latitude]");
  const longitudeInput = editor?.querySelector("[data-route-location-longitude]");
  const confirmedInput = editor?.querySelector("[data-route-location-confirmed]");
  const message = editor?.querySelector("[data-route-location-message]");
  if (!editor || !latitudeInput || !longitudeInput || !confirmedInput) return;

  const initialPoint = mapPoint(latitudeInput.value, longitudeInput.value);
  const map = createJobLocationMap(maplibregl, canvas, initialPoint, true);
  let marker = null;

  const setMarker = (point, center = false) => {
    if (!marker) {
      marker = new maplibregl.Marker({ element: pileMarkerElement(), draggable: true, anchor: "bottom" })
        .setLngLat([point.longitude, point.latitude])
        .addTo(map);
      marker.on("dragend", () => {
        const next = marker.getLngLat();
        setDraft({ latitude: next.lat, longitude: next.lng }, "Marker verschoben. Bitte Adresse prüfen und Standort übernehmen.");
      });
    } else {
      marker.setLngLat([point.longitude, point.latitude]);
    }
    if (center) map.easeTo({ center: [point.longitude, point.latitude], zoom: Math.max(map.getZoom(), 14) });
  };
  const setDraft = (point, text) => {
    latitudeInput.value = displayCoordinate(point.latitude);
    longitudeInput.value = displayCoordinate(point.longitude);
    confirmedInput.value = "";
    latitudeInput.dispatchEvent(new Event("input", { bubbles: true }));
    longitudeInput.dispatchEvent(new Event("input", { bubbles: true }));
    setMarker(point);
    if (message) message.textContent = text;
  };
  const syncInputsToMap = () => {
    const point = mapPoint(latitudeInput.value, longitudeInput.value);
    if (!point) return;
    setMarker(point, true);
  };

  if (initialPoint) setMarker(initialPoint);
  map.on("click", event => setDraft(
    { latitude: event.lngLat.lat, longitude: event.lngLat.lng },
    "Kartenposition vorbereitet. Bitte Adresse prüfen und Standort übernehmen.",
  ));
  latitudeInput.addEventListener("change", syncInputsToMap);
  longitudeInput.addEventListener("change", syncInputsToMap);
  canvas.dataset.mapSelectionEnabled = "true";
}

function initializeJobLocationPreview(canvas, maplibregl) {
  const point = mapPoint(canvas.dataset.latitude, canvas.dataset.longitude);
  if (!point) {
    markMapUnavailable(canvas);
    return;
  }
  const map = createJobLocationMap(maplibregl, canvas, point, false);
  new maplibregl.Marker({ element: pileMarkerElement(), anchor: "bottom" })
    .setLngLat([point.longitude, point.latitude])
    .addTo(map);
}

function routeCoordinate(value) {
  if (!Array.isArray(value) || value.length < 2) return null;
  const point = mapPoint(value[1], value[0]);
  return point ? [point.longitude, point.latitude] : null;
}

function routeLineFeature(rawGeometry) {
  const value = String(rawGeometry || "").trim();
  if (!value) return null;
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch {
    return null;
  }

  let geometry = parsed;
  if (parsed?.type === "Feature") geometry = parsed.geometry;
  if (parsed?.type === "FeatureCollection") {
    geometry = parsed.features?.find((feature) => feature?.geometry?.type === "LineString")?.geometry;
  }
  if (Array.isArray(geometry)) geometry = { type: "LineString", coordinates: geometry };
  if (geometry?.type !== "LineString" || !Array.isArray(geometry.coordinates)) return null;

  const coordinates = geometry.coordinates.map(routeCoordinate);
  if (coordinates.length < 2 || coordinates.some((coordinate) => !coordinate)) return null;
  return {
    type: "Feature",
    properties: {},
    geometry: { type: "LineString", coordinates },
  };
}

function routeStops(context) {
  return Array.from(context.querySelectorAll("[data-route-stop]"))
    .map((element, index) => ({
      element,
      point: mapPoint(element.dataset.latitude, element.dataset.longitude),
      position: Number(element.dataset.position) || index + 1,
      label: String(element.dataset.label || "").trim() || `Stopp ${index + 1}`,
      jobID: String(element.dataset.jobId || "").trim(),
      customer: String(element.dataset.customer || "").trim(),
      volume: String(element.dataset.volume || "").trim(),
      region: String(element.dataset.region || "").trim(),
    }))
    .filter((stop) => stop.point)
    .sort((left, right) => left.position - right.position);
}

function routeCandidates(context) {
  return Array.from(context.querySelectorAll("[data-route-candidate]"))
    .map((element, index) => ({
      element,
      checkbox: element.querySelector('input[type="checkbox"][name="job_id"]'),
      point: mapPoint(element.dataset.latitude, element.dataset.longitude),
      position: index + 1,
      label: String(element.dataset.label || "").trim() || `Auftrag ${index + 1}`,
      jobID: String(element.dataset.jobId || "").trim(),
      customer: String(element.dataset.customer || "").trim(),
      volume: String(element.dataset.volume || "").trim(),
      region: String(element.dataset.region || "").trim(),
      unavailableReason: String(element.dataset.unavailableReason || "").trim(),
    }))
    .filter((candidate) => candidate.point && candidate.checkbox);
}

function routeMarkerElement(stop, kind = "stop") {
  const marker = document.createElement("button");
  marker.type = "button";
  marker.className = `route-map-marker route-map-marker--${kind}`;
  marker.dataset.markerLabel = String(stop.position);
  marker.title = `${stop.position}. ${stop.label}`;
  marker.setAttribute("aria-label", `${stop.position}. ${stop.label} auf der Karte öffnen`);
  if (stop.jobID) marker.dataset.jobId = stop.jobID;
  return marker;
}

function startMarkerElement() {
  const marker = document.createElement("button");
  marker.type = "button";
  marker.className = "route-map-marker route-map-marker--start";
  marker.dataset.markerLabel = "S";
  marker.title = "Startort";
  marker.setAttribute("aria-label", "Startort auf der Karte öffnen");
  return marker;
}

function endMarkerElement() {
  const marker = document.createElement("button");
  marker.type = "button";
  marker.className = "route-map-marker route-map-marker--end";
  marker.dataset.markerLabel = "E";
  marker.title = "Endort";
  marker.setAttribute("aria-label", "Endort auf der Karte öffnen");
  return marker;
}

function routePopupContent(item, actionLabel = "") {
  const content = document.createElement("div");
  content.className = "route-map-popup";
  const heading = document.createElement("strong");
  heading.textContent = item.label;
  content.append(heading);
  if (item.customer) {
    const customer = document.createElement("span");
    customer.textContent = item.customer;
    content.append(customer);
  }
  const facts = [item.volume ? `${item.volume} m³` : "", item.region].filter(Boolean);
  if (facts.length) {
    const detail = document.createElement("small");
    detail.textContent = facts.join(" · ");
    content.append(detail);
  }
  let action = null;
  if (actionLabel) {
    action = document.createElement("button");
    action.type = "button";
    action.className = "button button--quiet route-map-popup__action";
    action.textContent = actionLabel;
    content.append(action);
  }
  return { content, action, heading };
}

function routeMapNotice(context, message) {
  let notice = context.querySelector("[data-route-map-notice]");
  if (!notice) {
    notice = document.createElement("p");
    notice.className = "route-map-notice";
    notice.dataset.routeMapNotice = "true";
    context.querySelector("[data-route-map]")?.insertAdjacentElement("afterend", notice);
  }
  notice.textContent = message;
}

function markRouteMapUnavailable(canvas, message = "Die Routenkarte ist derzeit nicht verfügbar. Die geordnete Stoppliste bleibt vollständig nutzbar.") {
  if (!canvas) return;
  let fallback = canvas.querySelector("[data-map-fallback]");
  if (!fallback) {
    fallback = document.createElement("div");
    fallback.className = "map-fallback";
    fallback.dataset.mapFallback = "true";
    canvas.append(fallback);
  }
  fallback.hidden = false;
  fallback.textContent = message;
}

function initializeRouteLineOverlay(container, map, geometry, routeSource, context) {
  if (!geometry) return;
  const lineCanvas = document.createElement("canvas");
  lineCanvas.className = "route-map-line-overlay";
  lineCanvas.dataset.routeLineOverlay = "true";
  lineCanvas.setAttribute("aria-hidden", "true");
  container.append(lineCanvas);
  const drawing = lineCanvas.getContext("2d");
  if (!drawing) {
    container.dataset.routeLineState = "failed";
    routeMapNotice(context, "Die berechnete Routenlinie konnte nicht gezeichnet werden. Start, Ende und Stopps bleiben als Punkte sichtbar.");
    return;
  }

  let announced = false;
  const draw = () => {
    const width = Math.max(1, Math.round(container.clientWidth));
    const height = Math.max(1, Math.round(container.clientHeight));
    const pixelRatio = Math.min(2, Math.max(1, window.devicePixelRatio || 1));
    const pixelWidth = Math.round(width * pixelRatio);
    const pixelHeight = Math.round(height * pixelRatio);
    if (lineCanvas.width !== pixelWidth || lineCanvas.height !== pixelHeight) {
      lineCanvas.width = pixelWidth;
      lineCanvas.height = pixelHeight;
    }
    lineCanvas.style.width = `${width}px`;
    lineCanvas.style.height = `${height}px`;
    drawing.setTransform(pixelRatio, 0, 0, pixelRatio, 0, 0);
    drawing.clearRect(0, 0, width, height);

    const points = geometry.geometry.coordinates.map((coordinate) => map.project(coordinate));
    if (points.length < 2 || points.some((point) => !Number.isFinite(point.x) || !Number.isFinite(point.y))) {
      container.dataset.routeLineState = "failed";
      return;
    }
    drawing.beginPath();
    drawing.moveTo(points[0].x, points[0].y);
    points.slice(1).forEach((point) => drawing.lineTo(point.x, point.y));
    drawing.setLineDash([]);
    drawing.lineCap = "round";
    drawing.lineJoin = "round";
    drawing.strokeStyle = "#fffdf7";
    drawing.lineWidth = 15;
    drawing.globalAlpha = .97;
    drawing.stroke();
    drawing.setLineDash(routeSource === "osrm" ? [] : [12, 9]);
    drawing.strokeStyle = "#a13f22";
    drawing.lineWidth = 9;
    drawing.globalAlpha = 1;
    drawing.stroke();
    drawing.setLineDash([]);
    const arrowSpacing = Math.max(1, Math.floor(points.length / 7));
    drawing.strokeStyle = "#fffdf7";
    drawing.lineWidth = 3;
    for (let index = arrowSpacing; index < points.length; index += arrowSpacing) {
      const tip = points[index];
      const previous = points[Math.max(0, index - arrowSpacing)];
      const dx = tip.x - previous.x;
      const dy = tip.y - previous.y;
      const length = Math.hypot(dx, dy);
      if (!Number.isFinite(length) || length < 12) continue;
      const ux = dx / length;
      const uy = dy / length;
      const size = Math.min(12, Math.max(7, length * .15));
      const baseX = tip.x - ux * size;
      const baseY = tip.y - uy * size;
      drawing.beginPath();
      drawing.moveTo(baseX - uy * size * .55, baseY + ux * size * .55);
      drawing.lineTo(tip.x, tip.y);
      drawing.lineTo(baseX + uy * size * .55, baseY - ux * size * .55);
      drawing.stroke();
    }

    const sample = points[Math.floor(points.length / 2)];
    let paintedPixels = 0;
    if (sample.x >= 0 && sample.y >= 0 && sample.x < width && sample.y < height) {
      const sampleX = Math.max(0, Math.round(sample.x * pixelRatio) - 5);
      const sampleY = Math.max(0, Math.round(sample.y * pixelRatio) - 5);
      const sampleWidth = Math.min(11, pixelWidth - sampleX);
      const sampleHeight = Math.min(11, pixelHeight - sampleY);
      const pixels = drawing.getImageData(sampleX, sampleY, sampleWidth, sampleHeight).data;
      for (let index = 3; index < pixels.length; index += 4) {
        if (pixels[index] > 0) paintedPixels++;
      }
    }
    container.dataset.routeLineRenderedPixels = String(paintedPixels);
    if (paintedPixels > 0) container.dataset.routeLineState = "drawn";
    else if (container.dataset.routeLineState !== "drawn") container.dataset.routeLineState = "pending";
    if (paintedPixels > 0 && !announced) {
      announced = true;
      const lineKind = routeSource === "osrm" ? "Straßenroute" : "geschätzte Routenlinie";
      routeMapNotice(context, `Die ${lineKind} ist sichtbar. Start, Stopps und Ende bleiben zusätzlich als beschriftete Punkte bedienbar.`);
    }
  };
  map.on("render", draw);
  map.on("remove", () => lineCanvas.remove());
  window.requestAnimationFrame(draw);
}

function initializeRouteMap(canvas, maplibregl) {
  const context = canvas.closest("[data-route-context]");
  if (!context) {
    markRouteMapUnavailable(canvas);
    return;
  }
  const stops = routeStops(context);
  const candidates = routeCandidates(context);
  const geometry = routeLineFeature(canvas.dataset.routeGeometry);
  const routeSource = String(canvas.dataset.routeSource || "").trim().toLowerCase();
  canvas.dataset.routeLineState = geometry ? "pending" : "missing";
  canvas.dataset.routeLineRenderedPixels = "0";
  const geometryCoordinates = geometry?.geometry.coordinates || [];
  const selectedStart = context.querySelector('[data-route-location-prefix="start"] [data-route-location-choice]:checked');
  const selectedEnd = context.querySelector('[data-route-location-prefix="end"] [data-route-location-choice]:checked');
  const routeStart = mapPoint(canvas.dataset.routeStartLatitude, canvas.dataset.routeStartLongitude);
  const routeEnd = mapPoint(canvas.dataset.routeEndLatitude, canvas.dataset.routeEndLongitude);
  const start = routeStart || mapPoint(selectedStart?.dataset.routeLocationLatitude, selectedStart?.dataset.routeLocationLongitude);
  const end = routeEnd || mapPoint(selectedEnd?.dataset.routeLocationLatitude, selectedEnd?.dataset.routeLocationLongitude);
  const sameEndpoint = start && end && Math.abs(start.latitude - end.latitude) < 1e-7 && Math.abs(start.longitude - end.longitude) < 1e-7;
  const stopJobIDs = new Set(stops.map((stop) => stop.jobID).filter(Boolean));
  const visibleCandidates = candidates.filter((candidate) => !stopJobIDs.has(candidate.jobID));
  candidates.forEach((candidate) => {
    const syncRow = () => candidate.element.classList.toggle("route-candidate--selected", candidate.checkbox.checked);
    candidate.checkbox.addEventListener("change", syncRow);
    syncRow();
  });
  const allCoordinates = [
    ...(start ? [[start.longitude, start.latitude]] : []),
    ...(end && !sameEndpoint ? [[end.longitude, end.latitude]] : []),
    ...geometryCoordinates,
    ...stops.map((stop) => [stop.point.longitude, stop.point.latitude]),
    ...visibleCandidates.map((candidate) => [candidate.point.longitude, candidate.point.latitude]),
  ];

  const first = allCoordinates[0];
  const map = new maplibregl.Map({
    container: canvas,
    style: hackWerkRasterStyle(),
    ...(first ? { center: first, zoom: 10 } : {
      bounds: [[9.5, 46.3], [17.3, 49.2]],
      fitBoundsOptions: { padding: 36, maxZoom: 7 },
    }),
    interactive: true,
    dragRotate: false,
    pitchWithRotate: false,
    attributionControl: true,
  });
  let ready = false;
  const fitAll = () => {
    if (allCoordinates.length === 0) {
      map.fitBounds([[9.5, 46.3], [17.3, 49.2]], { padding: 36, maxZoom: 7, duration: 0 });
      return;
    }
    if (allCoordinates.length === 1) {
      map.easeTo({ center: allCoordinates[0], zoom: 11, duration: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 300 });
      return;
    }
    const bounds = new maplibregl.LngLatBounds();
    allCoordinates.forEach((coordinate) => bounds.extend(coordinate));
    map.fitBounds(bounds, {
      padding: window.matchMedia("(max-width: 680px)").matches ? 36 : 56,
      maxZoom: 15,
      duration: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 450,
    });
  };
  let toolbar = context.querySelector("[data-route-map-toolbar]");
  if (!toolbar) {
    toolbar = document.createElement("div");
    toolbar.className = "route-map-toolbar";
    toolbar.dataset.routeMapToolbar = "true";
    toolbar.setAttribute("aria-label", "Kartenwerkzeuge");
    toolbar.innerHTML = '<button class="button button--quiet" type="button" data-route-map-start>Startort</button><button class="button button--quiet" type="button" data-route-map-fit>Alle Punkte</button><button class="button button--quiet" type="button" data-route-map-labels aria-pressed="true">Beschriftungen</button><button class="button button--quiet" type="button" data-route-map-retry>Karte erneut laden</button><span class="status-badge" data-route-map-count></span>';
    canvas.insertAdjacentElement("beforebegin", toolbar);
  }
  const startButton = toolbar.querySelector("[data-route-map-start]");
  if (!start) {
    startButton?.setAttribute("disabled", "");
    startButton?.setAttribute("title", "Wählen Sie zuerst einen Startort.");
  } else startButton?.addEventListener("click", () => map.easeTo({ center: [start.longitude, start.latitude], zoom: 13 }));
  toolbar.querySelector("[data-route-map-fit]")?.addEventListener("click", fitAll);
  toolbar.querySelector("[data-route-map-labels]")?.addEventListener("click", (event) => {
    const visible = !context.classList.toggle("route-map-labels-hidden");
    if (map.getLayer("hackwerk-candidate-labels")) map.setLayoutProperty("hackwerk-candidate-labels", "visibility", visible ? "visible" : "none");
    event.currentTarget.setAttribute("aria-pressed", String(visible));
    announce(visible ? "Kartenbeschriftungen eingeblendet." : "Kartenbeschriftungen ausgeblendet.");
  });
  toolbar.querySelector("[data-route-map-retry]")?.addEventListener("click", () => {
    routeMapNotice(context, "Kartenkacheln werden erneut geladen …");
    const streets = map.getSource("hackwerk-streets");
    if (typeof streets?.setTiles === "function") streets.setTiles([`${window.location.origin}/map/tiles/{z}/{x}/{y}`]);
    else map.triggerRepaint();
  });
  if (context.dataset.routeAdmin === "true" && navigator.geolocation) {
    const locate = document.createElement("button");
    locate.type = "button";
    locate.className = "button button--quiet";
    locate.textContent = "Zu meinem Standort";
    let locationMarker;
    locate.addEventListener("click", () => navigator.geolocation.getCurrentPosition((position) => {
      const point = [position.coords.longitude, position.coords.latitude];
      if (!locationMarker) {
        const marker = document.createElement("span");
        marker.className = "route-map-marker route-map-marker--location";
        marker.dataset.markerLabel = "Ich";
        locationMarker = new maplibregl.Marker({ element: marker, anchor: "bottom" }).setLngLat(point).addTo(map);
      } else locationMarker.setLngLat(point);
      map.easeTo({ center: point, zoom: 14 });
      routeMapNotice(context, "Ihr Browserstandort wird nur auf dieser Karte gezeigt und nicht gespeichert.");
    }, () => routeMapNotice(context, "Der Browserstandort ist nicht verfügbar. Es wurden keine Standortdaten gespeichert."), { enableHighAccuracy: true, timeout: 10000, maximumAge: 60000 }));
    toolbar.insertBefore(locate, toolbar.querySelector("[data-route-map-count]"));
  }
  const updateVisibleCount = () => {
    const bounds = map.getBounds();
    const count = [...stops, ...visibleCandidates].filter((item) => bounds.contains([item.point.longitude, item.point.latitude])).length;
    const label = toolbar.querySelector("[data-route-map-count]");
    if (label) label.textContent = `${count}/${stops.length + visibleCandidates.length} Punkte sichtbar`;
  };
  map.on("moveend", updateVisibleCount);
  map.addControl(new maplibregl.NavigationControl({ showCompass: false }), "top-right");
  if (typeof maplibregl.FullscreenControl === "function") {
    map.addControl(new maplibregl.FullscreenControl(), "top-right");
  }
  if (typeof maplibregl.ScaleControl === "function") {
    map.addControl(new maplibregl.ScaleControl({ unit: "metric", maxWidth: 120 }), "bottom-left");
  }
  if ("ResizeObserver" in window) {
    const resizeObserver = new ResizeObserver(() => map.resize());
    resizeObserver.observe(canvas);
    map.on("remove", () => resizeObserver.disconnect());
  }
  const updateRouteMarkerScale = () => {
    const zoom = map.getZoom();
    context.dataset.routeMarkerScale = zoom < 10 ? "overview" : zoom >= 13 ? "detail" : "standard";
  };
  updateRouteMarkerScale();
  map.on("zoomend", updateRouteMarkerScale);
  map.on("error", (event) => {
    const mapError = String(event?.error?.message || event?.message || "Kartenfehler");
    canvas.dataset.mapError = mapError;
    if (event?.sourceId === "hackwerk-streets") {
      routeMapNotice(context, "Die Kartenkacheln sind vorübergehend nicht verfügbar. Startort, Auftrags-Pins und Auswahl bleiben bedienbar.");
      return;
    }
    if (!ready) markRouteMapUnavailable(canvas);
  });
  map.once("style.load", () => {
    if (geometry && canvas.dataset.routeLineState !== "failed") {
      routeMapNotice(context, "Die berechnete Routenlinie wird geladen …");
    } else if (geometry) {
      routeMapNotice(context, "Die Route ist berechnet, aber die Routenlinie konnte nicht gezeichnet werden. Start, Ende und Stopps bleiben als Punkte sichtbar.");
    } else if (stops.length) {
      routeMapNotice(context, "Die Routenlinie fehlt; die verfügbaren Stopps werden als Kartenpunkte gezeigt.");
    } else if (visibleCandidates.some((candidate) => !candidate.checkbox.disabled)) {
      routeMapNotice(context, "Wählen Sie Aufträge direkt in der Liste oder über einen Karten-Pin aus. Die Route wird erst mit „Route berechnen“ erzeugt.");
    } else if (candidates.length) {
      routeMapNotice(context, "Alle sichtbaren Aufträge sind bereits eingeplant. Ihre Pins bleiben zur Übersicht geöffnet, können aber nicht erneut ausgewählt werden.");
    } else {
      routeMapNotice(context, "Die Grundkarte ist aktiv. Noch kein offener Auftrag besitzt einen gespeicherten Haufenstandort.");
    }

    if (start) {
      const startLabel = String(canvas.dataset.routeStartLabel || selectedStart?.dataset.routeLocationLabel || "Startort").trim() || "Startort";
      const startPopup = routePopupContent({ label: startLabel, customer: sameEndpoint ? "Start und Ende der Route" : "Start der Route" });
      new maplibregl.Marker({ element: startMarkerElement(), anchor: "bottom" })
        .setLngLat([start.longitude, start.latitude])
        .setPopup(new maplibregl.Popup({ offset: 22 }).setDOMContent(startPopup.content))
        .addTo(map);
    }
    if (end && !sameEndpoint) {
      const endLabel = String(canvas.dataset.routeEndLabel || selectedEnd?.dataset.routeLocationLabel || "Endort").trim() || "Endort";
      const endPopup = routePopupContent({ label: endLabel, customer: "Ende der Route" });
      new maplibregl.Marker({ element: endMarkerElement(), anchor: "bottom" })
        .setLngLat([end.longitude, end.latitude])
        .setPopup(new maplibregl.Popup({ offset: 22 }).setDOMContent(endPopup.content))
        .addTo(map);
    }

    const stopMarkers = new Map();
    stops.forEach((stop) => {
      const markerElement = routeMarkerElement(stop);
      const popup = routePopupContent(stop);
      new maplibregl.Marker({ element: markerElement, anchor: "bottom" })
        .setLngLat([stop.point.longitude, stop.point.latitude])
        .setPopup(new maplibregl.Popup({ offset: 22 }).setDOMContent(popup.content))
        .addTo(map);
      if (stop.jobID) stopMarkers.set(stop.jobID, markerElement);
    });

    const useClusters = visibleCandidates.length > 25;
    let clusteredSource;
    const clusteredData = () => ({
      type: "FeatureCollection",
      features: visibleCandidates.map((candidate) => ({
        type: "Feature",
        geometry: { type: "Point", coordinates: [candidate.point.longitude, candidate.point.latitude] },
        properties: {
          jobID: candidate.jobID,
          position: String(candidate.position),
          selected: candidate.checkbox.checked ? 1 : 0,
          disabled: candidate.checkbox.disabled ? 1 : 0,
        },
      })),
    });
    if (useClusters) {
      map.addSource("hackwerk-candidates", { type: "geojson", data: clusteredData(), cluster: true, clusterMaxZoom: 11, clusterRadius: 52 });
      clusteredSource = map.getSource("hackwerk-candidates");
      map.addLayer({ id: "hackwerk-candidate-clusters", type: "circle", source: "hackwerk-candidates", filter: ["has", "point_count"], paint: { "circle-color": "#214f3a", "circle-radius": ["step", ["get", "point_count"], 22, 50, 28, 100, 34], "circle-stroke-color": "#fffdf7", "circle-stroke-width": 3 } });
      map.addLayer({ id: "hackwerk-candidate-points", type: "circle", source: "hackwerk-candidates", filter: ["!", ["has", "point_count"]], paint: { "circle-color": ["case", ["==", ["get", "disabled"], 1], "#a8ada7", ["==", ["get", "selected"], 1], "#9b4931", "#214f3a"], "circle-radius": 18, "circle-stroke-color": "#fffdf7", "circle-stroke-width": 3 } });
      map.on("click", "hackwerk-candidate-clusters", (event) => {
        const feature = event.features?.[0];
        const clusterID = feature?.properties?.cluster_id;
        if (clusterID === undefined) return;
        routeMapNotice(context, `Gruppe mit ${feature.properties.point_count} Standorten. Die Karte zoomt zur Aufteilung hinein.`);
        const result = clusteredSource.getClusterExpansionZoom(clusterID);
        Promise.resolve(result).then((zoom) => map.easeTo({ center: feature.geometry.coordinates, zoom }));
      });
      map.on("click", "hackwerk-candidate-points", (event) => {
        const jobID = String(event.features?.[0]?.properties?.jobID || "");
        const candidate = visibleCandidates.find((item) => item.jobID === jobID);
        if (!candidate || candidate.checkbox.disabled) return;
        candidate.checkbox.checked = !candidate.checkbox.checked;
        candidate.checkbox.dispatchEvent(new Event("change", { bubbles: true }));
        clusteredSource.setData(clusteredData());
      });
      ["hackwerk-candidate-clusters", "hackwerk-candidate-points"].forEach((layer) => {
        map.on("mouseenter", layer, () => { map.getCanvas().style.cursor = "pointer"; });
        map.on("mouseleave", layer, () => { map.getCanvas().style.cursor = ""; });
      });
      visibleCandidates.forEach((candidate) => candidate.checkbox.addEventListener("change", () => clusteredSource.setData(clusteredData())));
    }

    const candidateMarkers = new Map();
    visibleCandidates.forEach((candidate) => {
      if (useClusters) return;
      const selectable = !candidate.checkbox.disabled;
      const markerElement = routeMarkerElement(candidate, selectable ? "candidate" : "candidate-unavailable");
      const popup = routePopupContent(candidate, selectable ? "Für Route auswählen" : "");
      if (!selectable) {
        markerElement.setAttribute("aria-disabled", "true");
        markerElement.title = `${candidate.position}. ${candidate.label} · ${candidate.unavailableReason || "Nicht auswählbar"}`;
      }
      const syncSelection = () => {
        const selected = candidate.checkbox.checked;
        markerElement.classList.toggle("route-map-marker--selected", selected);
        if (selectable) markerElement.setAttribute("aria-pressed", String(selected));
        if (popup.action) popup.action.textContent = selected ? "Aus Auswahl entfernen" : "Für Route auswählen";
      };
      if (selectable) {
        candidate.checkbox.addEventListener("change", () => {
          syncSelection();
          if (!candidate.checkbox.checked) return;
          const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
          map.easeTo({
            center: [candidate.point.longitude, candidate.point.latitude],
            zoom: Math.max(map.getZoom(), 11),
            duration: reducedMotion ? 0 : 300,
          });
        });
        popup.action?.addEventListener("click", () => {
          candidate.checkbox.checked = !candidate.checkbox.checked;
          candidate.checkbox.dispatchEvent(new Event("change", { bubbles: true }));
          candidate.checkbox.focus({ preventScroll: true });
        });
      }
      syncSelection();
      new maplibregl.Marker({ element: markerElement, anchor: "bottom" })
        .setLngLat([candidate.point.longitude, candidate.point.latitude])
        .setPopup(new maplibregl.Popup({ offset: 22 }).setDOMContent(popup.content))
        .addTo(map);
      candidateMarkers.set(candidate.jobID, markerElement);
      const focusPair = (active) => {
        candidate.element.classList.toggle("route-candidate--focused", active);
        markerElement.classList.toggle("route-map-marker--focused", active);
      };
      markerElement.addEventListener("pointerenter", () => focusPair(true));
      markerElement.addEventListener("pointerleave", () => focusPair(false));
      markerElement.addEventListener("focus", () => focusPair(true));
      markerElement.addEventListener("blur", () => focusPair(false));
      candidate.element.addEventListener("pointerenter", () => focusPair(true));
      candidate.element.addEventListener("pointerleave", () => focusPair(false));
      candidate.element.addEventListener("focusin", () => focusPair(true));
      candidate.element.addEventListener("focusout", () => focusPair(false));
    });

    context.addEventListener("routecandidateorderchange", () => {
      routeCandidates(context).forEach((ordered, index) => {
        const candidate = visibleCandidates.find((item) => item.jobID === ordered.jobID);
        if (!candidate) return;
        candidate.position = index + 1;
        const marker = candidateMarkers.get(candidate.jobID);
        if (marker) {
          marker.dataset.markerLabel = String(candidate.position);
          marker.title = `${candidate.position}. ${candidate.label}`;
          marker.setAttribute("aria-label", `${candidate.position}. ${candidate.label} auf der Karte öffnen`);
        }
      });
      if (clusteredSource) clusteredSource.setData(clusteredData());
      routeMapNotice(context, "Manuelle Reihenfolge aktualisiert. Die Berechnung verwendet diese Reihenfolge; markierte Positionen bleiben bei Optimierung fest.");
    });

    context.addEventListener("routeorderchange", () => {
      routeStops(context).forEach((stop) => {
        const markerElement = stopMarkers.get(stop.jobID);
        if (!markerElement) return;
        markerElement.textContent = String(stop.position);
        markerElement.title = `${stop.position}. ${stop.label}`;
        markerElement.setAttribute("aria-label", `${stop.position}. ${stop.label} auf der Karte öffnen`);
      });
      routeMapNotice(context, "Die Reihenfolge wurde in der Liste geändert. Nach dem Speichern wird die Routenlinie neu berechnet.");
    });

    fitAll();
    initializeRouteLineOverlay(canvas, map, geometry, routeSource, context);
    updateVisibleCount();
    ready = true;
    markMapReady(canvas);
  });
}

function directRouteOrderStops(order) {
  return Array.from(order.querySelectorAll("[data-route-stop]"))
    .filter((stop) => stop.closest("[data-route-order]") === order);
}

function routeOrderLabel(stop, position) {
  if (stop.dataset.routeOrderLabel) return stop.dataset.routeOrderLabel;
  const label = String(
    stop.dataset.label
      || stop.querySelector("[data-route-stop-label]")?.textContent
      || `Stopp ${position}`,
  ).trim();
  stop.dataset.routeOrderLabel = label;
  return label;
}

function routeOrderButton(stop, direction) {
  const selector = direction === "up"
    ? '[data-route-move="up"], [data-route-up]'
    : '[data-route-move="down"], [data-route-down]';
  let button = stop.querySelector(selector);
  if (button) {
    button.type = "button";
    button.classList.add("route-order-button");
    button.dataset.routeMove = direction;
    return button;
  }
  let actions = stop.querySelector("[data-route-order-actions]");
  if (!actions) {
    actions = document.createElement("div");
    actions.className = "route-stop-order-actions";
    actions.dataset.routeOrderActions = "true";
    stop.append(actions);
  }
  button = document.createElement("button");
  button.type = "button";
  button.className = "button button--quiet route-order-button";
  button.dataset.routeMove = direction;
  button.textContent = direction === "up" ? "↑ Hoch" : "↓ Runter";
  actions.append(button);
  return button;
}

function updateRouteOrder(order) {
  const stops = directRouteOrderStops(order);
  stops.forEach((stop, index) => {
    const position = index + 1;
    stop.dataset.position = String(position);
    const label = routeOrderLabel(stop, position);
    const positionLabel = stop.querySelector("[data-route-position]");
    if (positionLabel) positionLabel.textContent = String(position);
    const up = routeOrderButton(stop, "up");
    const down = routeOrderButton(stop, "down");
    up.disabled = index === 0;
    down.disabled = index === stops.length - 1;
    up.setAttribute("aria-label", `${label} nach oben verschieben`);
    down.setAttribute("aria-label", `${label} nach unten verschieben`);
  });
  return stops;
}

function initializeRouteOrder(order) {
  let live = order.querySelector("[data-route-order-live], [data-route-live]");
  if (!live) {
    live = document.createElement("p");
    live.className = "visually-hidden";
    live.dataset.routeOrderLive = "true";
    live.setAttribute("aria-live", "polite");
    live.setAttribute("aria-atomic", "true");
    order.insertAdjacentElement("afterend", live);
  }
  updateRouteOrder(order);
  order.addEventListener("click", (event) => {
    const button = event.target.closest("[data-route-move]");
    if (!button || !order.contains(button)) return;
    const stop = button.closest("[data-route-stop]");
    if (!stop || stop.closest("[data-route-order]") !== order) return;
    const stops = directRouteOrderStops(order);
    const index = stops.indexOf(stop);
    const offset = button.dataset.routeMove === "up" ? -1 : 1;
    const swap = stops[index + offset];
    if (!swap || swap.parentElement !== stop.parentElement) return;
    event.preventDefault();
    if (offset < 0) stop.parentElement.insertBefore(stop, swap);
    else stop.parentElement.insertBefore(swap, stop);

    const updated = updateRouteOrder(order);
    const newPosition = updated.indexOf(stop) + 1;
    live.textContent = `${routeOrderLabel(stop, newPosition)} ist jetzt Stopp ${newPosition} von ${updated.length}.`;
    const stopIDs = updated
      .map((item) => item.querySelector('input[type="hidden"][name="stop_id"]')?.value)
      .filter(Boolean);
    order.dispatchEvent(new CustomEvent("routeorderchange", { bubbles: true, detail: { stopIDs } }));
    const movedButton = stop.querySelector(`[data-route-move="${button.dataset.routeMove}"]`);
    const alternateDirection = button.dataset.routeMove === "up" ? "down" : "up";
    const focusTarget = movedButton?.disabled
      ? stop.querySelector(`[data-route-move="${alternateDirection}"]`)
      : movedButton;
    window.requestAnimationFrame(() => focusTarget?.focus());
  });
}

document.querySelectorAll("[data-route-order]").forEach(initializeRouteOrder);

document.querySelectorAll(".route-candidate-list").forEach((list) => {
  const directCandidates = () => Array.from(list.children).filter((item) => item.matches?.("[data-route-candidate]"));
  const update = () => directCandidates().forEach((candidate, index, values) => {
    candidate.dataset.position = String(index + 1);
    const up = candidate.querySelector('[data-route-candidate-move="up"]');
    const down = candidate.querySelector('[data-route-candidate-move="down"]');
    if (up) up.disabled = index === 0;
    if (down) down.disabled = index === values.length - 1;
  });
  list.addEventListener("click", (event) => {
    const button = event.target.closest?.("[data-route-candidate-move]");
    if (!button) return;
    const candidate = button.closest("[data-route-candidate]");
    const items = directCandidates();
    const index = items.indexOf(candidate);
    const offset = button.dataset.routeCandidateMove === "up" ? -1 : 1;
    const sibling = items[index + offset];
    if (!sibling) return;
    if (offset < 0) list.insertBefore(candidate, sibling);
    else list.insertBefore(sibling, candidate);
    update();
    list.dispatchEvent(new CustomEvent("routecandidateorderchange", { bubbles: true }));
    window.requestAnimationFrame(() => button.focus());
  });
  update();
});

document.querySelectorAll('[data-route-context][data-route-own="true"]').forEach((context) => {
  const stops = Array.from(context.querySelectorAll("[data-route-stop]"));
  stops.forEach((stop, index) => {
    if (!stop.id) stop.id = `route-stop-${index + 1}`;
    const navigation = document.createElement("nav");
    navigation.className = "route-stop-pagination";
    navigation.setAttribute("aria-label", `Navigation für Stopp ${index + 1}`);
    if (index > 0) {
      const previous = document.createElement("a");
      previous.className = "button button--quiet";
      previous.href = `#${stops[index - 1].id}`;
      previous.textContent = "← Vorheriger Stopp";
      navigation.append(previous);
    }
    if (index < stops.length - 1) {
      const next = document.createElement("a");
      next.className = "button button--quiet";
      next.href = `#${stops[index + 1].id}`;
      next.textContent = "Nächster Stopp →";
      navigation.append(next);
    }
    stop.append(navigation);
  });
  const nextStop = context.querySelector("[data-route-stop][data-route-next]");
  if (!nextStop) return;
  nextStop.classList.add("route-stop--next");
  const badge = document.createElement("span");
  badge.className = "status-badge route-next-badge";
  badge.textContent = "Nächster Stopp";
  nextStop.prepend(badge);
  const navigation = nextStop.querySelector("[data-route-navigation]");
  if (navigation) {
    const sticky = document.createElement("div");
    sticky.className = "route-sticky-navigation";
    const link = navigation.cloneNode(true);
    link.classList.add("button");
    link.textContent = "Navigation zum nächsten Stopp starten";
    sticky.append(link);
    const call = nextStop.querySelector("[data-route-call]");
    if (call) {
      const callLink = call.cloneNode(true);
      callLink.classList.add("button", "button--quiet");
      callLink.textContent = "Kunden anrufen";
      sticky.append(callLink);
    }
    context.append(sticky);
  }
});

const jobLocationEditors = Array.from(document.querySelectorAll("[data-job-location-editor]"));
const jobLocationPreviews = Array.from(document.querySelectorAll("[data-map-preview]"));
const routeMapCanvases = Array.from(document.querySelectorAll("[data-route-map]"));
const routeLocationMapCanvases = Array.from(document.querySelectorAll("[data-route-location-map]"));
if (jobLocationEditors.length || jobLocationPreviews.length || routeMapCanvases.length || routeLocationMapCanvases.length) {
  const initializeEditorFallback = (editor) => {
    if (editor.dataset.mapInitialized) return;
    editor.dataset.mapInitialized = "fallback";
    try { initializeJobLocationEditor(editor, null); } catch { markMapUnavailable(editor.querySelector("[data-map-canvas]")); }
  };
  loadMapLibre().then((maplibregl) => {
    const initializeEditor = (editor) => {
      if (editor.dataset.mapInitialized) return;
      editor.dataset.mapInitialized = "true";
      try {
        initializeJobLocationEditor(editor, maplibregl);
      } catch {
        try { initializeJobLocationEditor(editor, null); } catch { markMapUnavailable(editor.querySelector("[data-map-canvas]")); }
      }
    };
    jobLocationEditors.forEach((editor) => {
      const disclosure = editor.closest("details");
      if (!disclosure || disclosure.open) {
        initializeEditor(editor);
        return;
      }
      disclosure.addEventListener("toggle", () => {
        if (disclosure.open) window.requestAnimationFrame(() => initializeEditor(editor));
      });
    });
    routeMapCanvases.forEach((canvas) => {
      if (canvas.dataset.mapInitialized) return;
      canvas.dataset.mapInitialized = "true";
      try { initializeRouteMap(canvas, maplibregl); } catch { markRouteMapUnavailable(canvas); }
    });
    routeLocationMapCanvases.forEach((canvas) => {
      if (canvas.dataset.mapInitialized) return;
      canvas.dataset.mapInitialized = "true";
      try { initializeRouteLocationCoordinateMap(canvas, maplibregl); } catch { markRouteLocationMapUnavailable(canvas); }
    });
    const initializePreview = (preview) => {
      if (preview.dataset.mapInitialized) return;
      preview.dataset.mapInitialized = "true";
      try { initializeJobLocationPreview(preview, maplibregl); } catch { markMapUnavailable(preview); }
    };
    if (!("IntersectionObserver" in window)) {
      jobLocationPreviews.forEach(initializePreview);
      return;
    }
    const observer = new IntersectionObserver((entries) => {
      entries.filter((entry) => entry.isIntersecting).forEach((entry) => {
        observer.unobserve(entry.target);
        initializePreview(entry.target);
      });
    }, { rootMargin: "240px" });
    jobLocationPreviews.forEach((preview) => observer.observe(preview));
  }).catch(() => {
    jobLocationEditors.forEach(initializeEditorFallback);
    document.querySelectorAll("[data-map-canvas], [data-map-preview]").forEach(markMapUnavailable);
    routeMapCanvases.forEach((canvas) => markRouteMapUnavailable(canvas));
    routeLocationMapCanvases.forEach((canvas) => markRouteLocationMapUnavailable(canvas));
  });
}
