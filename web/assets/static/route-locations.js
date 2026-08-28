const routeLocationCoordinate = (value, minimum, maximum) => {
  const number = Number(String(value || "").trim().replace(",", "."));
  return Number.isFinite(number) && number >= minimum && number <= maximum ? number : null;
};

const routeLocationFormatCoordinate = value => value.toFixed(6);

function initializeRouteLocationEditor(editor) {
  const picker = editor.closest("[data-route-location-picker]");
  const form = editor.closest("form");
  const label = picker?.querySelector("[data-route-location-label]") || editor.querySelector("[data-route-location-label]");
  const address = picker?.querySelector("[data-route-location-address]") || editor.querySelector("[data-route-location-address]");
  const latitude = picker?.querySelector("[data-route-location-latitude]") || editor.querySelector("[data-route-location-latitude]");
  const longitude = picker?.querySelector("[data-route-location-longitude]") || editor.querySelector("[data-route-location-longitude]");
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
  const invalidate = () => {
    confirmed.value = "";
    if (nativeConfirmed) nativeConfirmed.checked = false;
    latitude.setAttribute("aria-invalid", "false");
    longitude.setAttribute("aria-invalid", "false");
    setError("");
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
    setError("");
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
  const searchAddress = async () => {
    const query = String(search?.value || "").trim();
    if (query.length < 3) {
      clearResults();
      showSearchStatus("Bitte mindestens drei Zeichen eingeben.");
      search?.focus();
      return;
    }
    const csrf = form?.querySelector("[name='csrf_token']")?.value;
    if (!csrf) {
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
      });
      const payload = await response.json().catch(() => ({}));
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
        button.textContent = text;
        button.addEventListener("click", () => {
          address.value = text;
          setCoordinateDraft(lat, lon);
          clearResults();
          showSearchStatus(`„${text}“ vorbereitet. Bitte prüfen und Standort übernehmen.`);
          if (message) message.textContent = "Adresse und Koordinaten vorbereitet; noch nicht übernommen.";
          confirm.focus({ preventScroll: true });
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
      clearResults();
      showSearchStatus(cause instanceof Error ? cause.message : "Die Adresssuche ist derzeit nicht verfügbar.");
    } finally {
      searchButton.disabled = false;
      searchButton.removeAttribute("aria-busy");
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
  const setActive = choice => {
    const kind = choice?.dataset.routeLocationKind || "custom";
    custom.hidden = kind !== "custom";
    if (kind === "custom") {
      id.value = "";
      version.value = "";
      latitude.value = "";
      longitude.value = "";
      lastStop.value = "";
      return;
    }
    if (kind === "saved") {
      id.value = choice.dataset.routeLocationId || "";
      version.value = choice.dataset.routeLocationVersion || "";
      latitude.value = choice.dataset.routeLocationLatitude || "";
      longitude.value = choice.dataset.routeLocationLongitude || "";
      confirmed.value = "";
      if (nativeConfirmed) nativeConfirmed.checked = false;
      lastStop.value = "";
      return;
    }
    id.value = "";
    version.value = "";
    latitude.value = "";
    longitude.value = "";
    confirmed.value = "";
    if (nativeConfirmed) nativeConfirmed.checked = false;
    lastStop.value = "true";
  };
  choices.forEach(choice => choice.addEventListener("change", () => setActive(choice)));
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

document.querySelectorAll("[data-route-location-editor]").forEach(editor => {
  if (!editor.closest("[data-route-location-picker]")) initializeRouteLocationEditor(editor);
});
