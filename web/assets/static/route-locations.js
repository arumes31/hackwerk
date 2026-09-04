const routeLocationCoordinate = (value, minimum, maximum) => {
  const number = Number(String(value || "").trim().replace(",", "."));
  return Number.isFinite(number) && number >= minimum && number <= maximum ? number : null;
};

const routeLocationFormatCoordinate = value => value.toFixed(6);

function initializeRouteLocationEditor(editor) {
  const picker = editor.closest("[data-route-location-picker]");
  const form = editor.closest("form");
  const custom = editor.closest("[data-route-location-custom]") || editor;
  const label = custom.querySelector("[data-route-location-label]");
  const address = custom.querySelector("[data-route-location-address]");
  const latitude = editor.querySelector("[data-route-location-latitude]");
  const longitude = editor.querySelector("[data-route-location-longitude]");
  const confirmed = picker?.querySelector("[data-route-location-confirmed]") || editor.querySelector("[data-route-location-confirmed]");
  const nativeConfirmed = picker?.querySelector("[data-route-location-native-confirmed]") || editor.querySelector("[data-route-location-native-confirmed]");
  const hiddenLatitude = picker?.querySelector("[data-route-location-hidden-latitude]");
  const hiddenLongitude = picker?.querySelector("[data-route-location-hidden-longitude]");
  const message = picker?.querySelector("[data-route-location-message]") || editor.querySelector("[data-route-location-message]");
  const error = picker?.querySelector("[data-route-location-error]") || editor.querySelector("[data-route-location-error]");
  const search = editor.querySelector("[data-route-location-search-input]");
  const searchButton = editor.querySelector("[data-route-location-search-submit]");
  const searchStatus = editor.querySelector("[data-route-location-search-status]");
  const results = editor.querySelector("[data-route-location-search-results]");
  const confirm = picker?.querySelector("[data-route-location-confirm]") || editor.querySelector("[data-route-location-confirm]");
  if (!label || !address || !latitude || !longitude || !confirmed || !confirm) return null;

  const setError = text => {
    if (!error) return;
    error.textContent = text;
    error.hidden = !text;
  };
  const notifyStatus = () => picker?.dispatchEvent(new Event("route-location-status", { bubbles: true }));
  const setSelectedResultState = text => {
    const state = results?.querySelector("[data-route-location-selected-state]");
    if (state) state.textContent = text;
  };
  const invalidate = () => {
    confirmed.value = "";
    if (nativeConfirmed) nativeConfirmed.checked = false;
    latitude.setAttribute("aria-invalid", "false");
    longitude.setAttribute("aria-invalid", "false");
    setError("");
    setSelectedResultState("Noch nicht übernommen");
    notifyStatus();
  };
  const point = () => {
    const lat = routeLocationCoordinate(latitude.value, -90, 90);
    const lon = routeLocationCoordinate(longitude.value, -180, 180);
    return lat === null || lon === null ? null : { lat, lon };
  };
  const setCoordinateDraft = (lat, lon) => {
    latitude.value = routeLocationFormatCoordinate(lat);
    longitude.value = routeLocationFormatCoordinate(lon);
    latitude.dispatchEvent(new Event("input", { bubbles: true }));
    longitude.dispatchEvent(new Event("input", { bubbles: true }));
    latitude.dispatchEvent(new Event("change", { bubbles: true }));
    longitude.dispatchEvent(new Event("change", { bubbles: true }));
  };
  const confirmLocation = () => {
    const location = point();
    const locationLabel = String(label.value || "").trim();
    if (!locationLabel) {
      setError("Bitte eine verständliche Bezeichnung für den Ort eingeben.");
      label.focus();
      return false;
    }
    if (Array.from(locationLabel).length > 120) {
      setError("Die Bezeichnung darf höchstens 120 Zeichen lang sein.");
      label.focus();
      return false;
    }
    if (!String(address.value || "").trim()) {
      setError("Bitte die geprüfte Adresse für den Ort angeben.");
      address.focus();
      return false;
    }
    if (!location) {
      setError("Bitte gültige Breiten- und Längengrade eingeben.");
      latitude.setAttribute("aria-invalid", "true");
      longitude.setAttribute("aria-invalid", "true");
      latitude.focus();
      return false;
    }
    latitude.value = routeLocationFormatCoordinate(location.lat);
    longitude.value = routeLocationFormatCoordinate(location.lon);
    if (hiddenLatitude) hiddenLatitude.value = latitude.value;
    if (hiddenLongitude) hiddenLongitude.value = longitude.value;
    confirmed.value = "true";
    if (nativeConfirmed) nativeConfirmed.checked = true;
    if (message) message.textContent = "Standort übernommen. Änderungen an Adresse oder Koordinaten müssen erneut bestätigt werden.";
    setSelectedResultState("Übernommen");
    setError("");
    notifyStatus();
    return true;
  };

  [label, address, latitude, longitude].forEach(input => input.addEventListener("input", invalidate));
  confirm.addEventListener("click", confirmLocation);

  const clearResults = () => {
    if (!results) return;
    results.replaceChildren();
    results.hidden = true;
  };
  const showSearchStatus = text => {
    if (searchStatus) searchStatus.textContent = text;
  };
  const showSelectedResult = text => {
    if (!results) return;
    results.replaceChildren();
    const item = document.createElement("li");
    const selected = document.createElement("div");
    selected.className = "location-search__result location-search__result--selected";
    selected.setAttribute("data-route-location-selected-result", "");
    const title = document.createElement("strong");
    title.textContent = "Ausgewählte Adresse";
    const addressText = document.createElement("span");
    addressText.textContent = text;
    const state = document.createElement("small");
    state.setAttribute("data-route-location-selected-state", "");
    state.textContent = "Zur Übernahme auswählen";
    selected.append(title, addressText, state);
    item.append(selected);
    results.append(item);
    results.hidden = false;
  };
  let searchSequence = 0;
  let searchController;
  const stopSearchBusy = () => {
    if (!searchButton) return;
    searchButton.disabled = false;
    searchButton.removeAttribute("aria-busy");
  };
  const searchAddress = async () => {
    const sequence = ++searchSequence;
    searchController?.abort();
    searchController = new AbortController();
    const query = String(search?.value || "").trim();
    if (query.length < 3) {
      stopSearchBusy();
      clearResults();
      showSearchStatus("Bitte mindestens drei Zeichen eingeben.");
      search?.focus();
      return;
    }
    const csrf = form?.querySelector("[name='csrf_token']")?.value;
    if (!csrf) {
      stopSearchBusy();
      showSearchStatus("Die Sicherheitsprüfung ist abgelaufen. Bitte laden Sie die Seite neu.");
      return;
    }
    clearResults();
    searchButton.disabled = true;
    searchButton.setAttribute("aria-busy", "true");
    showSearchStatus("Adresse wird gesucht …");
    try {
      const response = await fetch("/api/v1/geocoding/search", {
        method: "POST",
        headers: { "X-CSRF-Token": csrf, Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8" },
        body: new URLSearchParams({ query }),
        credentials: "same-origin",
        signal: searchController.signal,
      });
      const payload = await response.json().catch(() => ({}));
      if (sequence !== searchSequence) return;
      if (!response.ok) throw new Error(payload?.error?.message || "Die Adresssuche ist derzeit nicht verfügbar.");
      const matches = Array.isArray(payload.results) ? payload.results.slice(0, 10) : [];
      for (const match of matches) {
        const lat = routeLocationCoordinate(match?.latitude, -90, 90);
        const lon = routeLocationCoordinate(match?.longitude, -180, 180);
        const text = String(match?.label || "").trim();
        if (lat === null || lon === null || !text) continue;
        const item = document.createElement("li");
        const button = document.createElement("button");
        button.type = "button";
        button.className = "location-search__result";
        button.textContent = `Adresse übernehmen: ${text}`;
        button.addEventListener("click", () => {
          address.value = text;
          if (!String(label.value || "").trim()) label.value = text.split(",")[0].trim().slice(0, 120);
          setCoordinateDraft(lat, lon);
          showSelectedResult(text);
          if (!confirmLocation()) return;
          showSearchStatus("Adresse und Koordinaten wurden gemeinsam übernommen.");
          if (message) message.textContent = "Adresse und Koordinaten übernommen.";
          notifyStatus();
        });
        item.append(button);
        results?.append(item);
      }
      if (!results || results.children.length === 0) {
        showSearchStatus("Keine nutzbaren Treffer erhalten. Koordinaten können Sie direkt eintragen.");
        return;
      }
      results.hidden = false;
      showSearchStatus(`${results.children.length} Treffer gefunden. Bitte einen Ort auswählen.`);
    } catch (cause) {
      if (sequence !== searchSequence || cause?.name === "AbortError") return;
      clearResults();
      showSearchStatus(cause instanceof Error ? cause.message : "Die Adresssuche ist derzeit nicht verfügbar.");
    } finally {
      if (sequence !== searchSequence) return;
      stopSearchBusy();
    }
  };
  searchButton?.addEventListener("click", searchAddress);
  search?.addEventListener("keydown", event => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    searchAddress();
  });

  if (!picker) {
    form?.addEventListener("submit", event => {
      if (confirmed.value === "true") return;
      if (confirmLocation()) return;
      event.preventDefault();
      setError("Bitte den Standort nach Änderungen ausdrücklich übernehmen.");
      confirm.focus({ preventScroll: true });
    });
  }
  return { confirmLocation };
}

document.querySelectorAll("[data-route-location-picker]").forEach(picker => {
  const custom = picker.querySelector("[data-route-location-custom]");
  const choices = Array.from(picker.querySelectorAll("[data-route-location-choice]"));
  const id = picker.querySelector("[data-route-location-id-value]");
  const version = picker.querySelector("[data-route-location-version-value]");
  const latitude = picker.querySelector("[data-route-location-hidden-latitude]");
  const longitude = picker.querySelector("[data-route-location-hidden-longitude]");
  const confirmed = picker.querySelector("[data-route-location-confirmed]");
  const nativeConfirmed = picker.querySelector("[data-route-location-native-confirmed]");
  const lastStop = picker.querySelector("[data-route-location-last-stop]");
  const editor = picker.querySelector("[data-route-location-editor]");
  const editorAPI = editor ? initializeRouteLocationEditor(editor) : null;
  const setActive = (choice, selectionChanged = false) => {
    const kind = choice?.dataset.routeLocationKind || "custom";
    custom.hidden = kind !== "custom";
    if (kind === "custom") {
      id.value = "";
      version.value = "";
      latitude.value = "";
      longitude.value = "";
      lastStop.value = "";
      if (selectionChanged) {
        confirmed.value = "";
        if (nativeConfirmed) nativeConfirmed.checked = false;
      }
      picker.dispatchEvent(new Event("route-location-status", { bubbles: true }));
      return;
    }
    if (kind === "saved") {
      id.value = choice.dataset.routeLocationId || "";
      version.value = choice.dataset.routeLocationVersion || "";
      latitude.value = choice.dataset.routeLocationSavedLatitude || "";
      longitude.value = choice.dataset.routeLocationSavedLongitude || "";
      confirmed.value = "";
      if (nativeConfirmed) nativeConfirmed.checked = false;
      lastStop.value = "";
      picker.dispatchEvent(new Event("route-location-status", { bubbles: true }));
      return;
    }
    id.value = "";
    version.value = "";
    latitude.value = "";
    longitude.value = "";
    confirmed.value = "";
    if (nativeConfirmed) nativeConfirmed.checked = false;
    lastStop.value = "true";
    picker.dispatchEvent(new Event("route-location-status", { bubbles: true }));
  };
  choices.forEach(choice => choice.addEventListener("change", () => setActive(choice, true)));
  setActive(choices.find(choice => choice.checked));
  picker.closest("form")?.addEventListener("submit", event => {
    const active = choices.find(choice => choice.checked);
    if (active?.dataset.routeLocationKind !== "custom" || confirmed.value === "true" || nativeConfirmed?.checked) return;
    event.preventDefault();
    const error = picker.querySelector("[data-route-location-error]");
    if (error) {
      error.textContent = "Bitte den individuellen Standort ausdrücklich übernehmen.";
      error.hidden = false;
    }
    picker.querySelector("[data-route-location-confirm]")?.focus({ preventScroll: true });
    picker.querySelector("[data-route-location-error]")?.scrollIntoView({ block: "center", behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  });
});

const routePlannerForms = Array.from(document.querySelectorAll("form[action='/planning/routes']"));
routePlannerForms.forEach(form => {
  const feedback = form.querySelector("[data-route-form-feedback]");
  const feedbackTitle = feedback?.querySelector("[data-route-form-feedback-title]");
  const feedbackList = feedback?.querySelector("[data-route-form-feedback-list]");
  const jobs = Array.from(form.querySelectorAll("input[name='job_id']"));
  if (!feedback || !feedbackTitle || !feedbackList || jobs.length === 0) return;

  const endpointProblem = (prefix, heading) => {
    const picker = form.querySelector(`[data-route-location-prefix="${prefix}"]`);
    const choice = picker?.querySelector("[data-route-location-choice]:checked");
    if (!picker || !choice) return { message: `${heading} auswählen.`, target: picker };
    if (choice.dataset.routeLocationKind !== "custom") return null;
    const label = picker.querySelector("[data-route-location-label]");
    const address = picker.querySelector("[data-route-location-address]");
    const latitude = picker.querySelector("[data-route-location-latitude]");
    const longitude = picker.querySelector("[data-route-location-longitude]");
    const confirmed = picker.querySelector("[data-route-location-confirmed]")?.value === "true" || picker.querySelector("[data-route-location-native-confirmed]")?.checked;
    const hasLabel = Boolean(String(label?.value || "").trim());
    const hasAddress = Boolean(String(address?.value || "").trim());
    const hasLatitude = Boolean(String(latitude?.value || "").trim());
    const hasLongitude = Boolean(String(longitude?.value || "").trim());
    if (!hasLabel && !hasAddress && !hasLatitude && !hasLongitude) return { message: `${heading} auswählen oder Adresse übernehmen.`, target: picker };
    if (!hasLabel) return { message: `${heading}: Bezeichnung eingeben.`, target: label };
    if (!hasAddress) return { message: `${heading}: Adresse eingeben oder suchen.`, target: address };
    const lat = routeLocationCoordinate(latitude?.value, -90, 90);
    const lon = routeLocationCoordinate(longitude?.value, -180, 180);
    if (lat === null || lon === null) return { message: `${heading}: gültige Koordinaten eingeben.`, target: latitude };
    if (!confirmed) return { message: `${heading}: manuell geänderten Standort übernehmen.`, target: picker.querySelector("[data-route-location-confirm]") };
    return null;
  };

  const requiredProblems = () => {
    const problems = [];
    if (!jobs.some(input => input.checked)) problems.push({ message: "Bitte mindestens einen Auftrag auswählen.", target: jobs[0] });
    for (const endpoint of [["start", "Startort"], ["end", "Endort"]]) {
      const problem = endpointProblem(...endpoint);
      if (problem) problems.push(problem);
    }
    for (const [name, message] of [["driver_id", "Fahrer auswählen."], ["departure_date", "Abfahrtsdatum eingeben."], ["departure_time", "Abfahrtszeit eingeben."]]) {
      const input = form.elements.namedItem(name);
      if (!String(input?.value || "").trim()) problems.push({ message, target: input });
    }
    return problems;
  };

  const renderFeedback = force => {
    const selected = jobs.some(input => input.checked);
    const problems = requiredProblems();
    if (!force && !selected) {
      feedback.hidden = true;
      return problems;
    }
    feedback.hidden = false;
    feedback.classList.toggle("route-form-feedback--complete", problems.length === 0);
    feedback.classList.toggle("route-form-feedback--error", force && problems.length > 0);
    feedback.setAttribute("role", force && problems.length > 0 ? "alert" : "status");
    feedbackTitle.textContent = problems.length === 0 ? "Bereit zur Berechnung" : "Vor der Routenberechnung fehlt noch:";
    feedbackList.replaceChildren();
    if (problems.length === 0) {
      const item = document.createElement("li");
      item.textContent = "Aufträge, Orte, Fahrer und Abfahrt sind vollständig. Eine Hackmaschine kann optional zugewiesen werden.";
      feedbackList.append(item);
      return problems;
    }
    for (const problem of problems) {
      const item = document.createElement("li");
      item.textContent = problem.message;
      feedbackList.append(item);
    }
    return problems;
  };

  form.addEventListener("change", () => renderFeedback(false));
  form.addEventListener("input", () => renderFeedback(false));
  form.addEventListener("route-location-status", () => renderFeedback(false));
  form.addEventListener("submit", event => {
    const problems = renderFeedback(true);
    if (problems.length === 0) return;
    event.preventDefault();
    feedback.scrollIntoView({ block: "center", behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    problems[0].target?.focus?.({ preventScroll: true });
  });
});

document.querySelectorAll("[data-route-location-editor]").forEach(editor => {
  if (!editor.closest("[data-route-location-picker]")) initializeRouteLocationEditor(editor);
});

document.documentElement.dataset.routeLocationsReady = "true";
