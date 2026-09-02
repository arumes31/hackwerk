document.documentElement.classList.add("js");

const liveStatus = () => document.querySelector("[data-live-status]");
const announce = (message) => {
  const target = liveStatus();
  if (target) target.textContent = message;
};

async function copyText(value, sourceElement = null) {
  const text = String(value || "");
  if (!text) return false;
  try {
    if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const fallback = sourceElement || document.createElement("textarea");
    const temporary = !sourceElement;
    if (temporary) {
      document.querySelector("[data-copy-manual]")?.remove();
      fallback.value = text;
      fallback.className = "copy-manual-field";
      fallback.dataset.copyManual = "true";
      fallback.setAttribute("aria-label", "Text zum manuellen Kopieren");
      document.body.append(fallback);
    }
    fallback.focus();
    fallback.select?.();
    try {
      if (document.execCommand?.("copy")) {
        if (temporary) fallback.remove();
        return true;
      }
    } catch { /* Keep the selected text available for manual copying. */ }
    announce("Automatisches Kopieren ist nicht verfügbar. Der Text ist markiert; bitte mit Strg+C kopieren.");
    return false;
  }
}

function safePreferenceGet(key) {
  try { return window.localStorage.getItem(key); } catch { return null; }
}

function safePreferenceSet(key, value) {
  try { window.localStorage.setItem(key, value); } catch { /* Presentation preference stays optional. */ }
}

function safePreferenceRemove(key) {
  try { window.localStorage.removeItem(key); } catch { /* Privacy notice remains usable without storage. */ }
}

const privacyNoticeVisible = () => {
  const notice = document.querySelector("[data-privacy-notice]");
  return Boolean(notice && !notice.hidden);
};

function initializePrivacyNotice() {
  const notice = document.querySelector("[data-privacy-notice]");
  if (!notice) return;
  // Keep the notice prominent without covering controls near the viewport edge.
  // The template lives next to the footer so it can be reused on every page;
  // moving it to the start of the body turns it into an in-flow page banner.
  document.body.prepend(notice);
  const preferenceKey = "hackwerk:privacy-notice:v1";
  const open = ({ reset = false, focus = false } = {}) => {
    if (reset) safePreferenceRemove(preferenceKey);
		notice.hidden = false;
		window.dispatchEvent(new CustomEvent("hackwerk:privacy-notice", { detail: { open: true } }));
    if (focus) window.requestAnimationFrame(() => notice.focus({ preventScroll: true }));
  };
  const dismiss = () => {
    safePreferenceSet(preferenceKey, "read");
		notice.hidden = true;
		window.dispatchEvent(new CustomEvent("hackwerk:privacy-notice", { detail: { open: false } }));
    announce("Cookie-Hinweis geschlossen. Er kann im Footer erneut geöffnet werden.");
  };
  document.querySelectorAll("[data-privacy-notice-open]").forEach((button) => {
    button.addEventListener("click", (event) => {
      event.preventDefault();
      open({ reset: true, focus: true });
    });
  });
  notice.querySelector("[data-privacy-notice-dismiss]")?.addEventListener("click", dismiss);
  if (safePreferenceGet(preferenceKey) !== "read") open();
}

initializePrivacyNotice();

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

let lastSuccessfulConnection = navigator.onLine ? new Date() : null;
let wasOffline = !navigator.onLine;
function updateConnectivityBanner() {
  document.querySelectorAll("[data-connectivity-banner]").forEach((banner) => {
    banner.hidden = navigator.onLine;
    banner.textContent = navigator.onLine
      ? "Verbindung wiederhergestellt. Nicht gespeicherte Änderungen können jetzt gesendet werden."
      : `Offline: Lesen bleibt teilweise möglich. Letzte Verbindung${lastSuccessfulConnection ? ` um ${lastSuccessfulConnection.toLocaleTimeString("de-AT", { hour: "2-digit", minute: "2-digit" })} Uhr` : " unbekannt"}. Änderungen werden nicht zwischengespeichert.`;
  });
  document.querySelectorAll("[data-profile-connectivity]").forEach((status) => {
    status.textContent = navigator.onLine
      ? "Online – Änderungen werden direkt an HackWerk gesendet"
      : "Offline – Änderungen sind gesperrt und werden nicht vorgemerkt";
    status.dataset.state = navigator.onLine ? "online" : "offline";
  });
  const recovered = navigator.onLine && wasOffline;
  if (navigator.onLine) {
    lastSuccessfulConnection = new Date();
    wasOffline = false;
    if (recovered) {
      announce("Verbindung wiederhergestellt. Sichere Leseansichten werden aktualisiert.");
      window.dispatchEvent(new CustomEvent("hackwerk:online"));
    }
  } else {
    wasOffline = true;
  }
}
window.addEventListener("online", updateConnectivityBanner);
window.addEventListener("offline", updateConnectivityBanner);
updateConnectivityBanner();

// Keep navigation context without storing customer or job identifiers outside
// the current browser-history entry.
const currentHistoryState = { ...(window.history.state || {}) };
if (Number.isFinite(currentHistoryState.scrollY)) {
  window.requestAnimationFrame(() => window.scrollTo({ top: currentHistoryState.scrollY, behavior: "auto" }));
}
window.addEventListener("pagehide", () => {
  window.history.replaceState({ ...(window.history.state || {}), scrollY: window.scrollY }, "");
});

const safeSectionID = (id) => id && !/[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/i.test(id);
document.querySelectorAll("details[id]").forEach((details) => {
  if (!safeSectionID(details.id)) return;
  const url = new URL(window.location.href);
  if (url.searchParams.getAll("section").includes(details.id)) details.open = true;
  details.addEventListener("toggle", () => {
    const next = new URL(window.location.href);
    const openSections = Array.from(document.querySelectorAll("details[id][open]"), (item) => item.id).filter(safeSectionID).slice(0, 12);
    next.searchParams.delete("section");
    openSections.forEach((id) => next.searchParams.append("section", id));
    window.history.replaceState(window.history.state, "", next);
  });
});

document.querySelectorAll('input[type="tel"]').forEach((input) => {
  input.inputMode = "tel";
  const preview = document.createElement("small");
  preview.className = "field-preview";
  preview.setAttribute("aria-live", "polite");
  input.insertAdjacentElement("afterend", preview);
  const normalizePhone = (value) => {
    let compact = value.trim().replace(/[\s()./-]+/g, "");
    if (compact.startsWith("00")) compact = `+${compact.slice(2)}`;
    if (compact.startsWith("0")) compact = `+43${compact.slice(1)}`;
    if ((compact.match(/\+/g) || []).length > 1 || (compact.includes("+") && !compact.startsWith("+"))) return "";
    const digits = compact.startsWith("+") ? compact.slice(1) : compact;
    if (!/^\d{7,15}$/.test(digits)) return "";
    return `+${digits}`;
  };
  const update = () => {
    const raw = input.value.trim();
    const normalized = normalizePhone(raw);
    preview.textContent = !raw ? "" : normalized ? `Gespeichert als: ${normalized}` : "Bitte 7 bis 15 Ziffern als gültige Telefonnummer eingeben.";
  };
  input.addEventListener("input", update); update();
});
document.querySelectorAll('input[type="email"]').forEach((input) => {
  const warning = document.createElement("small");
  warning.className = "field-preview field-preview--warning";
  warning.setAttribute("role", "status");
  input.insertAdjacentElement("afterend", warning);
  const update = () => { warning.textContent = input.value !== input.value.trim() ? "Leerzeichen am Anfang oder Ende entfernen." : ""; };
  input.addEventListener("input", update); update();
});
document.querySelectorAll("[data-job-type]").forEach((select) => {
  const help = document.createElement("small"); help.className = "field-preview";
  select.insertAdjacentElement("afterend", help);
  const update = () => { help.textContent = select.value === "chipping_with_transport" ? "Mit Transport: Entscheiden Sie danach intern, extern oder noch offen; extern erfordert eine ausdrückliche Bestätigung." : "Nur Hackmaschine: Es wird keine Transportressource eingeplant."; };
  select.addEventListener("change", update); update();
});
document.querySelectorAll("[data-history-filter]").forEach((select) => {
  const rows = Array.from(document.querySelectorAll("[data-history-event]"));
  const initial = new URL(window.location.href).searchParams.get("history_event") || "";
  if (Array.from(select.options).some((option) => option.value === initial)) select.value = initial;
  const update = () => {
    rows.forEach((row) => { row.hidden = Boolean(select.value) && row.dataset.historyEvent !== select.value; });
    const url = new URL(window.location.href);
    if (select.value) url.searchParams.set("history_event", select.value); else url.searchParams.delete("history_event");
    window.history.replaceState(window.history.state, "", url);
  };
  select.addEventListener("change", update); update();
});
document.querySelectorAll("[data-note-input]").forEach((input) => {
  const warning = document.createElement("small"); warning.className = "field-preview field-preview--warning"; warning.setAttribute("role", "status"); input.insertAdjacentElement("afterend", warning);
  const update = () => { warning.textContent = input.value.length >= 3200 ? "Sehr lange interne Bemerkung: Bitte auf entscheidungsrelevante Informationen kürzen." : ""; };
  input.addEventListener("input", update); update();
});

document.querySelectorAll('input[type="password"]').forEach((input) => {
  const wrapper = document.createElement("span");
  wrapper.className = "password-input";
  input.before(wrapper);
  wrapper.append(input);
  const toggle = document.createElement("button");
  toggle.type = "button"; toggle.className = "password-reveal";
  toggle.setAttribute("aria-pressed", "false");
  toggle.setAttribute("aria-label", "Passwort anzeigen");
  const icon = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  icon.setAttribute("viewBox", "0 0 24 24"); icon.setAttribute("aria-hidden", "true"); icon.setAttribute("focusable", "false");
  const eye = document.createElementNS("http://www.w3.org/2000/svg", "path");
  eye.setAttribute("d", "M2.5 12s3.5-5.5 9.5-5.5 9.5 5.5 9.5 5.5-3.5 5.5-9.5 5.5S2.5 12 2.5 12Z");
  const pupil = document.createElementNS("http://www.w3.org/2000/svg", "circle");
  pupil.setAttribute("cx", "12"); pupil.setAttribute("cy", "12"); pupil.setAttribute("r", "2.5");
  const slash = document.createElementNS("http://www.w3.org/2000/svg", "path");
  slash.setAttribute("class", "password-reveal__slash"); slash.setAttribute("d", "m4 4 16 16");
  icon.append(eye, pupil, slash); toggle.append(icon); wrapper.append(toggle);
  const caps = document.createElement("small"); caps.className = "field-preview field-preview--warning"; caps.setAttribute("role", "status");
  wrapper.insertAdjacentElement("afterend", caps);
  toggle.addEventListener("click", () => {
    const reveal = input.type === "password";
    input.type = reveal ? "text" : "password";
    toggle.setAttribute("aria-pressed", String(reveal));
    toggle.setAttribute("aria-label", reveal ? "Passwort verbergen" : "Passwort anzeigen");
    input.focus();
  });
  const updateCaps = (event) => { caps.textContent = event.getModifierState?.("CapsLock") ? "Feststelltaste ist aktiv." : ""; };
  input.addEventListener("keydown", updateCaps); input.addEventListener("keyup", updateCaps); input.addEventListener("blur", () => { caps.textContent = ""; });
});

document.querySelectorAll("[data-password-strength]").forEach((input) => {
  const output = document.querySelector("[data-password-strength-output]");
  if (!output) return;
  const bar = output.querySelector("span");
  const label = output.querySelector("small");
  const update = () => {
    const value = input.value;
    const score = [value.length >= 14, value.length >= 20, /[a-z]/.test(value) && /[A-Z]/.test(value), /\d/.test(value), /[^\p{L}\p{N}]/u.test(value)].filter(Boolean).length;
    output.dataset.score = String(score);
    if (bar) bar.style.setProperty("--password-score", `${score * 20}%`);
    if (label) label.textContent = value.length < 14
      ? `Noch ${14 - value.length} Zeichen bis zur Mindestlänge.`
      : score >= 4 ? "Starkes Passwort. Die endgültige Prüfung erfolgt beim Speichern." : "Gültige Länge. Mehr Länge und unterschiedliche Zeichenarten erhöhen die Stärke.";
  };
  input.addEventListener("input", update);
  update();
});

let installEvent;
const installPrompt = document.querySelector("[data-install-prompt]");
const profileInstallButton = document.querySelector("[data-profile-install]");
const profileInstallStatus = document.querySelector("[data-profile-install-status]");
const hideInstallPrompt = () => {
  if (installPrompt) installPrompt.hidden = true;
};
const isStandalone = () => window.matchMedia("(display-mode: standalone)").matches || navigator.standalone === true;
const updateInstallState = () => {
  const installed = isStandalone();
  if (profileInstallStatus) profileInstallStatus.textContent = installed ? "Installiert" : installEvent ? "Installation unterstützt" : "In diesem Browser nicht verfügbar";
  if (profileInstallButton) profileInstallButton.hidden = installed || !installEvent;
};
const offerInstallPrompt = () => {
  const dismissed = safePreferenceGet("hackwerk:install-dismissed") === "true";
  if (!installEvent || dismissed || isStandalone() || !installPrompt || privacyNoticeVisible()) {
    hideInstallPrompt();
    return;
  }
  installPrompt.hidden = false;
  announce("HackWerk kann auf diesem Gerät installiert werden.");
};
const promptForInstall = async () => {
  if (!installEvent) {
    hideInstallPrompt();
    updateInstallState();
    return;
  }
  const promptEvent = installEvent;
  installEvent = undefined;
  hideInstallPrompt();
  updateInstallState();
  try {
    await promptEvent.prompt();
    await promptEvent.userChoice;
  } catch {
    announce("Die Installation konnte nicht geöffnet werden. Verwenden Sie bei Bedarf die Installationsfunktion des Browsers.");
  }
};

hideInstallPrompt();
updateInstallState();
window.addEventListener("beforeinstallprompt", (event) => {
  event.preventDefault();
  if (typeof event.prompt !== "function") return;
	installEvent = event;
	updateInstallState();
	offerInstallPrompt();
});
window.addEventListener("hackwerk:privacy-notice", (event) => {
	if (event.detail?.open) hideInstallPrompt();
	else offerInstallPrompt();
});
installPrompt?.querySelector("[data-install-accept]")?.addEventListener("click", async () => {
  await promptForInstall();
});
profileInstallButton?.addEventListener("click", promptForInstall);
installPrompt?.querySelector("[data-install-dismiss]")?.addEventListener("click", () => {
	safePreferenceSet("hackwerk:install-dismissed", "true");
	hideInstallPrompt();
	updateInstallState();
});
window.addEventListener("appinstalled", () => {
  installEvent = undefined;
  hideInstallPrompt();
  updateInstallState();
});

const bytesFromBase64URL = (value) => {
  const base64 = String(value).replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(String(value).length / 4) * 4, "=");
  return Uint8Array.from(window.atob(base64), (character) => character.charCodeAt(0));
};
const base64URLFromBytes = (value) => {
  if (value === null || value === undefined) return null;
  const bytes = new Uint8Array(value);
  let binary = "";
  bytes.forEach((byte) => { binary += String.fromCharCode(byte); });
  return window.btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};
const credentialCreationOptions = (options) => {
  const publicKey = options.publicKey || options;
  if (PublicKeyCredential.parseCreationOptionsFromJSON) return { publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(publicKey) };
  publicKey.challenge = bytesFromBase64URL(publicKey.challenge);
  publicKey.user.id = bytesFromBase64URL(publicKey.user.id);
  (publicKey.excludeCredentials || []).forEach((item) => { item.id = bytesFromBase64URL(item.id); });
  return options.publicKey ? { ...options, publicKey } : { publicKey };
};
const credentialRequestOptions = (options) => {
  const publicKey = options.publicKey || options;
  if (PublicKeyCredential.parseRequestOptionsFromJSON) return { publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(publicKey) };
  publicKey.challenge = bytesFromBase64URL(publicKey.challenge);
  (publicKey.allowCredentials || []).forEach((item) => { item.id = bytesFromBase64URL(item.id); });
  return options.publicKey ? { ...options, publicKey } : { publicKey };
};
const publicKeyCredentialJSON = (credential) => {
  if (typeof credential.toJSON === "function") return credential.toJSON();
  const response = {
    clientDataJSON: base64URLFromBytes(credential.response.clientDataJSON),
  };
  if (credential.response.attestationObject) response.attestationObject = base64URLFromBytes(credential.response.attestationObject);
  if (credential.response.authenticatorData) response.authenticatorData = base64URLFromBytes(credential.response.authenticatorData);
  if (credential.response.signature) response.signature = base64URLFromBytes(credential.response.signature);
  if (credential.response.userHandle) response.userHandle = base64URLFromBytes(credential.response.userHandle);
  if (credential.response.getTransports) response.transports = credential.response.getTransports();
  return { id: credential.id, rawId: base64URLFromBytes(credential.rawId), type: credential.type, response, clientExtensionResults: credential.getClientExtensionResults(), authenticatorAttachment: credential.authenticatorAttachment };
};
const passkeysSupported = () => window.isSecureContext && "PublicKeyCredential" in window && navigator.credentials;

document.querySelectorAll("[data-passkey-register]").forEach((form) => {
  const button = form.querySelector("[data-passkey-register-button]");
  const status = form.querySelector("[data-passkey-support]");
  if (!passkeysSupported()) {
    if (button) button.hidden = true;
    if (status) status.textContent = "Passkeys werden auf diesem Gerät oder in dieser Verbindung nicht unterstützt.";
    return;
  }
  if (status) status.textContent = "Unterstützt. HackWerk speichert keinen Fingerabdruck und keine Geräte-PIN.";
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (button) button.disabled = true;
    if (status) status.textContent = "Passkey wird vorbereitet …";
    try {
      const csrf = form.elements.namedItem("csrf_token")?.value || "";
      const optionsResponse = await fetch("/profile/security/passkeys/options", { method: "POST", credentials: "same-origin", headers: { "X-CSRF-Token": csrf } });
      if (!optionsResponse.ok) throw new Error("options");
      const credential = await navigator.credentials.create(credentialCreationOptions(await optionsResponse.json()));
      const passkeyName = encodeURIComponent(form.elements.namedItem("name")?.value || "Dieses Gerät");
      const finishResponse = await fetch(`/profile/security/passkeys/finish?name=${passkeyName}`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: JSON.stringify(publicKeyCredentialJSON(credential)) });
      if (!finishResponse.ok) throw new Error("finish");
      const result = await finishResponse.json();
      if (Array.isArray(result.recovery_codes) && result.recovery_codes.length) {
        const panel = document.querySelector(".recovery-panel");
        const oneTime = document.createElement("div");
        oneTime.className = "recovery-codes";
        oneTime.setAttribute("role", "status");
        oneTime.tabIndex = -1;
        const heading = document.createElement("strong"); heading.textContent = "Jetzt einmalig speichern";
        const list = document.createElement("ul");
        result.recovery_codes.forEach((value) => { const item = document.createElement("li"); const code = document.createElement("code"); code.textContent = String(value); item.append(code); list.append(item); });
        const help = document.createElement("p"); help.textContent = "Der Passkey ist aktiv. Laden Sie die Seite erst neu, nachdem Sie diese Codes gesichert haben.";
        oneTime.append(heading, list, help);
        panel?.prepend(oneTime);
        oneTime.focus();
        if (status) status.textContent = "Passkey aktiviert. Recovery-Codes jetzt sicher speichern.";
      } else {
        window.location.assign("/profile?status=passkey_added#security");
      }
    } catch (error) {
      if (status) status.textContent = error?.name === "NotAllowedError" ? "Passkey-Einrichtung abgebrochen oder nicht erlaubt." : "Passkey konnte nicht eingerichtet werden. Bitte erneut versuchen.";
    } finally {
      if (button) button.disabled = false;
    }
  });
});

document.querySelectorAll("[data-passkey-login]").forEach((button) => {
  const status = document.querySelector("[data-passkey-login-status]");
  if (!passkeysSupported()) {
    button.hidden = true;
    if (status) status.textContent = "Passkeys werden auf diesem Gerät oder in dieser Verbindung nicht unterstützt.";
    return;
  }
  button.addEventListener("click", async () => {
    button.disabled = true;
    if (status) status.textContent = "Passkey wird angefordert …";
    try {
      const optionsResponse = await fetch("/login/mfa/passkey/options", { method: "POST", credentials: "same-origin" });
      if (!optionsResponse.ok) throw new Error("options");
      const credential = await navigator.credentials.get(credentialRequestOptions(await optionsResponse.json()));
      const finishResponse = await fetch("/login/mfa/passkey/finish", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(publicKeyCredentialJSON(credential)) });
      if (!finishResponse.ok) throw new Error("finish");
      const result = await finishResponse.json();
      window.location.assign(result.redirect || "/dashboard");
    } catch (error) {
      if (status) status.textContent = error?.name === "NotAllowedError" ? "Passkey-Anmeldung abgebrochen oder nicht erlaubt." : "Passkey konnte nicht geprüft werden. Verwenden Sie eine andere Methode oder versuchen Sie es erneut.";
      button.disabled = false;
    }
  });
});

document.querySelectorAll("[data-logout-form]").forEach((form) => {
  form.addEventListener("submit", () => {
    if (!form.querySelector("[data-clear-local-preferences]")?.checked) return;
    ["hackwerk:density", "hackwerk:outdoor", "hackwerk:install-dismissed", "hackwerk:privacy-notice:v1"].forEach(safePreferenceRemove);
  });
});

document.querySelectorAll("[data-user-directory]").forEach((directory) => {
  const filter = document.querySelector("[data-user-filter]");
  const cards = Array.from(directory.querySelectorAll("[data-user-card]"));
  const status = directory.querySelector("[data-user-filter-status]");
  if (!filter) return;
  const apply = () => {
    const query = filter.value.trim().toLocaleLowerCase("de-AT");
    let visibleCount = 0;
    cards.forEach((card) => {
      const visible = query === "" || card.dataset.userSearch.includes(query);
      card.hidden = !visible;
      if (visible) visibleCount += 1;
    });
    if (status) {
      status.hidden = visibleCount !== 0;
      status.textContent = visibleCount === 0 ? "Kein Zugang passt zum Filter." : `${visibleCount} Zugänge sichtbar.`;
    }
  };
  filter.addEventListener("input", apply);
});
if (window.visualViewport) {
  const updateViewport = () => document.documentElement.style.setProperty("--visual-viewport-height", `${window.visualViewport.height}px`);
  window.visualViewport.addEventListener("resize", updateViewport); updateViewport();
}
if (!document.body.classList.contains("login-body")) {
  const scrollTopButton = document.createElement("button");
  scrollTopButton.type = "button"; scrollTopButton.className = "scroll-top button button--quiet"; scrollTopButton.textContent = "Nach oben"; scrollTopButton.hidden = true;
  scrollTopButton.addEventListener("click", () => window.scrollTo({ top: 0, behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" }));
  document.body.append(scrollTopButton);
  window.addEventListener("scroll", () => { scrollTopButton.hidden = window.scrollY < 900; }, { passive: true });
}

document.querySelectorAll("[data-confirmation-form]").forEach((form) => {
  const summary = form.querySelector("[data-confirmation-summary]");
  const note = form.elements.namedItem("response_note");
  const noteFeedback = form.querySelector("[data-confirmation-note-feedback]");
  const choices = Array.from(form.querySelectorAll("input[name='action']"));
  const selectedChoice = () => choices.find((choice) => choice.checked);
  const updateNoteFeedback = () => {
    if (!note || !noteFeedback) return;
    note.removeAttribute("aria-invalid");
    const length = note.value.length;
    noteFeedback.textContent = length === 0 ? "Die Notiz wird nur bei einer Ablehnung oder einem Rückrufwunsch übermittelt." : `${length} von 500 Zeichen. Die Notiz wird nur bei einer Ablehnung oder einem Rückrufwunsch übermittelt.`;
  };
  note?.addEventListener("input", updateNoteFeedback);
  updateNoteFeedback();
  choices.forEach((choice) => {
    const updateSummary = () => { if (summary && choice.checked) summary.textContent = `${choice.dataset.responseLabel} Gespeichert wird erst mit „Antwort verbindlich speichern“.`; };
    choice.addEventListener("change", updateSummary);
    choice.addEventListener("focus", updateSummary);
  });
  form.addEventListener("submit", (event) => {
    const choice = selectedChoice();
    const actionAllowsNote = choice?.value === "declined" || choice?.value === "callback_requested";
    if (note?.value.trim() && !actionAllowsNote) {
      event.preventDefault(); note.focus();
      note.setAttribute("aria-invalid", "true");
      if (noteFeedback) noteFeedback.textContent = "Die Rückrufnotiz kann nur mit einer Ablehnung oder einem Rückrufwunsch gesendet werden.";
      if (summary) summary.textContent = "Bitte leeren Sie die Notiz oder wählen Sie „Termin ablehnen“ beziehungsweise „Rückruf wünschen“.";
      return;
    }
    if (summary && choice?.dataset.responseLabel) summary.textContent = `${choice.dataset.responseLabel} Jetzt wird die Antwort einmalig gespeichert.`;
  });
});

document.querySelectorAll("[data-planning-results]").forEach((results) => {
  const grid = results.querySelector(".suggestion-grid");
  const cards = Array.from(results.querySelectorAll(".suggestion-card"));
  const comparison = results.querySelector("[data-suggestion-comparison]");
  const selected = () => cards.filter((card) => card.querySelector("[data-suggestion-compare]")?.checked);
  const renderComparison = () => {
    const values = selected();
    cards.forEach((card) => card.classList.toggle("suggestion-card--selected", values.includes(card)));
    if (!comparison) return;
    comparison.hidden = values.length === 0;
    comparison.replaceChildren();
    values.forEach((card) => { const item = document.createElement("p"); item.textContent = card.dataset.suggestionSummary; comparison.append(item); });
  };
  cards.forEach((card) => {
    const checkbox = card.querySelector("[data-suggestion-compare]");
    checkbox?.addEventListener("change", () => {
      if (selected().length > 2) { checkbox.checked = false; announce("Es können höchstens zwei Vorschläge verglichen werden."); }
      renderComparison();
    });
    card.querySelector("[data-copy-suggestion]")?.addEventListener("click", async () => {
      const explanation = Array.from(card.querySelectorAll("h3, .suggestion-facts dd, h4 + ul li, .planning-warning li"), (item) => item.textContent.trim()).join(" · ");
      if (await copyText(explanation)) announce("Vorschlagserklärung kopiert.");
    });
    card.querySelector("[data-suggestion-adopt]")?.closest("form")?.addEventListener("submit", (event) => {
      if (!window.confirm(`${card.dataset.suggestionSummary}\n\nAls unverbindlichen Vorschlag übernehmen? Es wird nichts fixiert oder versendet.`)) event.preventDefault();
    });
  });
  results.querySelector("[data-suggestion-sort]")?.addEventListener("change", (event) => {
    const key = event.target.value;
    const attribute = { travel: "suggestionTravel", start: "suggestionStart", duration: "suggestionDuration", rank: "suggestionRank" }[key];
    cards.sort((left, right) => key === "start" ? left.dataset[attribute].localeCompare(right.dataset[attribute]) : Number(left.dataset[attribute]) - Number(right.dataset[attribute]));
    cards.forEach((card) => grid.append(card));
  });
});

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
  const confirmationMessage = event.submitter?.dataset.confirmMessage || form.dataset.confirmMessage;
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
    if (dialog.dataset.actionPending === "true") return;
    closeDialogWithDirtyCheck(dialog);
  });
});

document.addEventListener("submit", (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || String(form.method).toLowerCase() === "get" || form.dataset.allowMultipleSubmit === "true") return;
  if (event.defaultPrevented) return;
  const confirmationMessage = event.submitter?.dataset.confirmMessage || form.dataset.confirmMessage;
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
      try { window.sessionStorage.setItem("hackwerk:return-scroll-y", String(Math.max(0, Math.round(window.scrollY)))); } catch { /* Position restoration is optional. */ }
    });
  });
});
try {
  const returnScrollY = Number(window.sessionStorage.getItem("hackwerk:return-scroll-y"));
  if (Number.isFinite(returnScrollY) && returnScrollY > 0) {
    window.requestAnimationFrame(() => window.scrollTo({ top: returnScrollY, behavior: "auto" }));
  }
  window.sessionStorage.removeItem("hackwerk:return-scroll-y");
} catch { /* Session-only position restoration is optional. */ }

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

function calendarFailure(payload, status) {
  const failure = new Error(calendarErrorMessage(payload));
  failure.status = status;
  failure.code = payload?.error?.code;
  return failure;
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
    throw calendarFailure(payload, response.status);
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
    appointment_version_conflict: "Zwischenzeitliche Terminänderung",
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

function renderAppointmentPreflight(preview) {
  const target = document.querySelector("[data-appointment-preflight]");
  if (!target) return null;
  target.replaceChildren();
  const heading = document.createElement("h3");
  heading.textContent = "Prüfung vor der Änderung";
  const timing = document.createElement("p");
  timing.textContent = `Arbeit ${preview.WorkingMinutes ?? preview.working_minutes ?? 0} Min. · Transport ${preview.TransportMinutes ?? preview.transport_minutes ?? 0} Min. · Puffer ${preview.BufferBeforeMinutes ?? preview.buffer_before_minutes ?? 0}/${preview.BufferAfterMinutes ?? preview.buffer_after_minutes ?? 0} Min.`;
  const list = document.createElement("ul");
  list.className = "preflight-check-list";
  (preview.Checks || preview.checks || []).forEach((check) => {
    const item = document.createElement("li");
    const passed = check.Passed ?? check.passed;
    item.className = passed ? "preflight-check preflight-check--passed" : "preflight-check preflight-check--failed";
    const label = document.createElement("strong");
    label.textContent = `${passed ? "Bestanden" : "Prüfen"}: ${check.Label || check.label}`;
    const detail = document.createElement("span");
    detail.textContent = check.Detail || check.detail || "";
    item.append(label, detail); list.append(item);
  });
  const conflicts = preview.Conflicts || preview.conflicts || [];
  target.append(heading, timing, list);
  if (conflicts.length) {
    const conflictHeading = document.createElement("strong");
    conflictHeading.textContent = "Betroffene Zuweisungen";
    const conflictList = document.createElement("ul");
    conflicts.forEach((conflict) => {
      const item = document.createElement("li");
      item.textContent = `${conflict.SubjectName || conflict.subject_name || "Ressource"} · ${conflict.JobNumber || conflict.job_number || "Termin"} · ${conflict.CustomerName || conflict.customer_name || ""}`;
      conflictList.append(item);
    });
    target.append(conflictHeading, conflictList);
  }
  target.hidden = false;
  target.tabIndex = -1;
  return target;
}

async function previewAppointmentMutation(appointmentID, version, action, extra, csrf) {
  const form = new FormData();
  form.set("csrf_token", csrf); form.set("version", version); form.set("action", action);
  Object.entries(extra || {}).forEach(([key, value]) => {
    if (Array.isArray(value)) value.forEach((item) => form.append(key, item));
    else form.set(key, value);
  });
  const preview = await calendarRequest(`/api/v1/appointments/${encodeURIComponent(appointmentID)}/preview`, form, csrf);
  const target = renderAppointmentPreflight(preview);
  if (target) {
    await new Promise((resolve) => window.requestAnimationFrame(resolve));
    if (target.isConnected && !target.hidden) target.focus({ preventScroll: false });
  }
  return preview;
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
    const list = document.createElement("ul"); affected.forEach((label, appointmentID) => { const item = document.createElement("li"); const link = document.createElement("a"); link.href = `/calendar?appointment=${encodeURIComponent(appointmentID)}`; link.textContent = label; item.append(link); list.append(item); }); block.append(list);
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

const commandPalette = document.querySelector("[data-command-palette]");
const shortcutDialog = document.querySelector("[data-shortcuts-dialog]");
let commandTrigger = null;
let shortcutTrigger = null;

function openCommandPalette(trigger) {
  if (!commandPalette) return;
  commandTrigger = trigger || document.activeElement;
  if (!commandPalette.open) commandPalette.showModal();
  window.requestAnimationFrame(() => commandPalette.querySelector("[data-global-search-input]")?.focus());
}

function closeCommandPalette() {
  if (!commandPalette?.open) return;
  commandPalette.close();
  commandTrigger?.focus?.();
}

document.querySelectorAll("[data-command-open]").forEach((button) => button.addEventListener("click", () => openCommandPalette(button)));
document.querySelectorAll("[data-command-close]").forEach((button) => button.addEventListener("click", closeCommandPalette));
document.querySelectorAll("[data-shortcuts-open]").forEach((button) => button.addEventListener("click", () => {
	shortcutTrigger = commandTrigger || button;
  if (commandPalette?.open) commandPalette.close();
  if (shortcutDialog && !shortcutDialog.open) shortcutDialog.showModal();
  window.requestAnimationFrame(() => shortcutDialog?.querySelector("[data-shortcuts-close]")?.focus());
}));
document.querySelectorAll("[data-shortcuts-close]").forEach((button) => button.addEventListener("click", () => shortcutDialog?.close()));
commandPalette?.addEventListener("close", () => commandTrigger?.focus?.());
shortcutDialog?.addEventListener("close", () => shortcutTrigger?.focus?.());
if (commandPalette instanceof HTMLDialogElement) document.documentElement.classList.add("command-dialog-ready");

document.addEventListener("keydown", (event) => {
  const active = document.activeElement;
  const isInput = active && active.matches("input, textarea, select, [contenteditable='true']");
  if ((event.ctrlKey || event.metaKey) && event.key.toLocaleLowerCase("de-AT") === "k") {
    event.preventDefault(); openCommandPalette(active); return;
  }
  if (!isInput && event.key === "?") {
    event.preventDefault();
	shortcutTrigger = active;
    if (commandPalette?.open) commandPalette.close();
    if (shortcutDialog && !shortcutDialog.open) shortcutDialog.showModal();
	window.requestAnimationFrame(() => shortcutDialog?.querySelector("[data-shortcuts-close]")?.focus());
  }
});

function renderGlobalSearchResults(target, results) {
  target.replaceChildren();
  if (!results.length) {
    const empty = document.createElement("p"); empty.textContent = "Keine Treffer."; target.append(empty); return;
  }
  results.forEach((result) => {
    const link = document.createElement("a"); link.className = "search-result"; link.href = result.Href || result.href;
    const label = document.createElement("span"); label.className = "status-badge";
    label.textContent = ({ customer: "Kunde", job: "Auftrag", appointment: "Termin" })[result.Kind || result.kind] || "Treffer";
    const title = document.createElement("strong"); title.textContent = result.Title || result.title;
    const subtitle = document.createElement("small"); subtitle.textContent = result.Subtitle || result.subtitle || "";
    link.append(label, title, subtitle); target.append(link);
  });
  target.querySelector("a")?.focus();
}

document.querySelector("[data-global-search-form]")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const target = commandPalette?.querySelector("[data-global-search-results]");
  if (!target || !form.reportValidity()) return;
  target.textContent = "Suche läuft …";
  try {
    const response = await fetch(form.action, { method: "POST", body: new URLSearchParams(new FormData(form)), credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error?.message || "Suche derzeit nicht verfügbar.");
    renderGlobalSearchResults(target, payload.results || []);
  } catch (failure) {
    target.replaceChildren();
    const message = document.createElement("p"); message.className = "form-alert"; message.textContent = failure.message;
    const retry = document.createElement("button"); retry.type = "button"; retry.className = "button button--quiet"; retry.textContent = "Suche erneut versuchen"; retry.addEventListener("click", () => form.requestSubmit());
    target.append(message, retry); retry.focus();
  }
});

const waitlistSelections = Array.from(document.querySelectorAll("[data-waitlist-select]"));
const selectionCount = document.querySelector("[data-selection-count]");
const selectionOpen = document.querySelector("[data-selection-open]");
function updateWaitlistSelection() {
  const selected = waitlistSelections.filter((input) => input.checked);
  if (selected.length > 20) {
    const changed = selected[selected.length - 1]; changed.checked = false;
    announce("Maximal 20 Aufträge auswählen.", "Auswahl begrenzt");
    return updateWaitlistSelection();
  }
  if (selectionCount) selectionCount.textContent = `${selected.length} ausgewählt`;
  if (selectionOpen) selectionOpen.disabled = selected.length === 0;
}
waitlistSelections.forEach((input) => input.addEventListener("change", updateWaitlistSelection));
selectionOpen?.addEventListener("click", () => {
  waitlistSelections.filter((input) => input.checked).forEach((input) => window.open(input.dataset.openHref, "_blank", "noopener"));
  announce("Ausgewählte Aufträge wurden nur lesend geöffnet. Es wurde keine Mehrfachmutation ausgeführt.");
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

function setAppointmentActionGroupVisibility(dialog, name, visible) {
  const group = dialog.querySelector(`[data-appointment-action-group="${name}"]`);
  if (group) group.hidden = !visible;
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
  const previousPreflight = dialog.querySelector("[data-appointment-preflight]");
  if (previousPreflight) { previousPreflight.hidden = true; previousPreflight.replaceChildren(); }
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
  if (withoutNotification) withoutNotification.hidden = true;
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
  const dateOnly = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Europe/Vienna", year: "numeric", month: "2-digit", day: "2-digit",
  });
  const channels = (props.notification_channels || []).join(" und ") || "Keine automatische Benachrichtigung";
  const rows = [
    ["Status", props.status_label],
    ["Zeit", `${dateTime.format(start)} – ${time.format(end)}`],
    ["Dauer", `${Math.round((end - start) / 60000)} Minuten${dateOnly.format(start) !== dateOnly.format(end) ? " · endet am Folgetag" : ""}`],
    ["Arbeitszeit", `${props.working_minutes || 0} Minuten`],
    ["Transportzeit", `${props.transport_minutes || 0} Minuten`],
    ["Puffer", `${props.buffer_before_minutes || 0} Minuten davor · ${props.buffer_after_minutes || 0} Minuten danach`],
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
    if (item.response_note) rows.push(["Rückrufnotiz zur Ablehnung", item.response_note]);
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
  if (props.message_preview) {
    const preview = document.createElement("section");
    preview.className = "message-preview";
    const heading = document.createElement("h3"); heading.textContent = "Nachrichtenvorschau vor Fixierung / neuem Link";
    const explanation = document.createElement("p"); explanation.textContent = "Nebenwirkungsfrei; [BESTÄTIGUNGSLINK] wird erst im Worker sicher ersetzt.";
    const subject = document.createElement("p"); subject.textContent = `Betreff: ${props.message_preview.Subject || props.message_preview.subject || ""}`;
    const email = document.createElement("pre"); email.textContent = props.message_preview.Text || props.message_preview.text || "";
    const sms = document.createElement("pre"); sms.textContent = props.message_preview.SMS || props.message_preview.sms || "";
    preview.append(heading, explanation, subject, email, sms); detail.append(preview);
  }
  const copyTime = document.createElement("button");
  copyTime.type = "button"; copyTime.className = "button button--quiet"; copyTime.textContent = "Beginn und Ende kopieren";
  copyTime.addEventListener("click", async () => { if (await copyText(`${dateTime.format(start)} – ${dateTime.format(end)} · Europe/Vienna`)) announce("Terminzeit kopiert."); });
  detail.append(copyTime);
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
    const customer = document.createElement("a"); customer.className = "button button--quiet"; customer.href = `/customers/${encodeURIComponent(props.customer_id)}`; customer.textContent = "Kundenakte öffnen"; detail.append(customer);
  }
  const permalink = document.createElement("a"); permalink.className = "button button--quiet"; permalink.href = `/calendar?appointment=${encodeURIComponent(event.id)}`; permalink.textContent = "Terminlink"; detail.append(permalink);
  const fix = dialog.querySelector("[data-appointment-fix]");
  const cancel = dialog.querySelector("[data-appointment-cancel]");
  const assignment = dialog.querySelector("[data-appointment-assignment]");
  const reschedule = dialog.querySelector("[data-appointment-reschedule]");
	const swapPanel = dialog.querySelector("[data-appointment-swap-panel]");
  const confirmationAdmin = dialog.querySelector("[data-confirmation-admin]");
  const reissue = dialog.querySelector("[data-confirmation-reissue]");
  const resetConfirmation = dialog.querySelector("[data-confirmation-reset]");
	const reopenPanel = dialog.querySelector("[data-appointment-reopen-panel]");
  const completePanel = dialog.querySelector("[data-appointment-complete-panel]");
  const completeOverride = dialog.querySelector("[data-appointment-complete-override]");
  const canAssign = Boolean(props.can_assign);
  const canReschedule = Boolean(props.can_reschedule);
  const canSwap = Boolean(props.can_swap);
  const canFix = Boolean(props.can_fix);
  const canComplete = Boolean(props.can_complete);
  const canCancel = Boolean(props.can_cancel);
  const canReopen = Boolean(props.can_reopen);
  const canReissue = Boolean(props.can_reissue);
  const canResetConfirmation = Boolean(props.can_reset_confirmation);
  const needsWithoutNotificationReason = (canFix || canReschedule) && (props.notification_channels || []).length === 0;
  if (fix) fix.hidden = !canFix;
  if (cancel) cancel.hidden = !canCancel;
  if (assignment) {
    assignment.hidden = !canAssign;
    const selectedDrivers = new Set((props.drivers || []).map((item) => item.ID));
    assignment.querySelectorAll("[data-appointment-driver]").forEach((input) => {
      input.checked = selectedDrivers.has(input.value);
    });
    const primary = (props.drivers || []).find((item) => item.Primary);
    const primaryInput = assignment.querySelector("[data-appointment-primary-driver]");
    if (primaryInput) primaryInput.value = primary?.ID || "";
    const resourceFor = (purpose) => (props.resources || []).find((item) => item.Purpose === purpose)?.ID || "";
    const chipper = assignment.querySelector("[data-appointment-chipper]");
    const transport = assignment.querySelector("[data-appointment-transport]");
    const trailer = assignment.querySelector("[data-appointment-trailer]");
    if (chipper) chipper.value = resourceFor("chipping");
    if (transport) transport.value = resourceFor("transport");
    if (trailer) trailer.value = resourceFor("trailer");
    const selectedOtherResources = new Set((props.resources || []).filter((item) => item.Purpose === "other").map((item) => item.ID));
    assignment.querySelectorAll("[data-appointment-other-resource]").forEach((input) => {
      input.checked = selectedOtherResources.has(input.value);
    });
    const assignmentVersion = assignment.querySelector("[data-appointment-assignment-version]");
    if (assignmentVersion) assignmentVersion.value = props.version;
  }
  if (reschedule) reschedule.hidden = !canReschedule;
	if (swapPanel) swapPanel.hidden = !canSwap;
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
  if (withoutNotification) withoutNotification.hidden = !needsWithoutNotificationReason;
  if (confirmationAdmin) confirmationAdmin.hidden = !(canReissue || canResetConfirmation);
  if (reissue) reissue.hidden = !canReissue;
  if (resetConfirmation) resetConfirmation.hidden = !canResetConfirmation;
	if (reopenPanel) reopenPanel.hidden = !canReopen;
  if (completePanel) completePanel.hidden = !canComplete;
  if (completeOverride) completeOverride.hidden = !props.complete_requires_override;
  dialog.dataset.completeRequiresOverride = props.complete_requires_override ? "true" : "false";
  setAppointmentActionGroupVisibility(dialog, "assignment", canAssign);
  setAppointmentActionGroupVisibility(dialog, "time", canReschedule || canSwap);
  setAppointmentActionGroupVisibility(dialog, "customer-communication", needsWithoutNotificationReason || canReissue || canResetConfirmation);
  setAppointmentActionGroupVisibility(dialog, "primary", canFix || canComplete);
  setAppointmentActionGroupVisibility(dialog, "danger", canCancel || canReopen);
  dialog.showModal();
}

document.querySelectorAll("[data-appointment-close]").forEach((button) => {
  button.addEventListener("click", () => {
    if (closeDialogWithDirtyCheck(button.closest("dialog"))) appointmentDetailSequence += 1;
  });
});
document.querySelector("[data-appointment-dialog]")?.addEventListener("cancel", (event) => {
  if (event.currentTarget.dataset.actionPending === "true") event.preventDefault();
});

async function appointmentAction(action, extra = {}) {
  const dialog = document.querySelector("[data-appointment-dialog]");
  if (dialog.dataset.actionPending === "true") throw new Error("appointment action pending");
  const controls = Array.from(dialog.querySelectorAll("button, input, select, textarea"));
  const controlStates = controls.map((control) => control.disabled);
  dialog.dataset.actionPending = "true";
  dialog.setAttribute("aria-busy", "true");
  controls.forEach((control) => { control.disabled = true; });
  dialog.inert = true;
  const csrf = dialog.querySelector("[data-appointment-csrf]").value;
  const form = new FormData();
  form.set("csrf_token", csrf); form.set("version", dialog.dataset.version);
  Object.entries(extra).forEach(([key, value]) => {
    if (Array.isArray(value)) {
      form.delete(key);
      value.forEach((item) => form.append(key, item));
      return;
    }
    form.set(key, value);
  });
  try {
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
  } finally {
    delete dialog.dataset.actionPending;
    dialog.removeAttribute("aria-busy");
    dialog.inert = false;
    controls.forEach((control, index) => { control.disabled = controlStates[index]; });
  }
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
  const preflight = dialog.querySelector("[data-appointment-preflight]");
  if (preflight) { preflight.hidden = true; preflight.replaceChildren(); }
  try {
    const action = start.value === dialog.dataset.originalStart ? "resize" : "move";
	const proposedStart = viennaLocalDate(start.value);
	const proposedEnd = proposedStart ? new Date(proposedStart.getTime() + Number(duration.value) * 60000) : null;
	const fields = {
      starts_at_local: start.value,
      duration_minutes: duration.value,
      override_reason: dialog.querySelector("[data-appointment-move-override]")?.value.trim() || "",
      without_notification_reason: reason,
	  starts_at: proposedStart?.toISOString() || "",
	  ends_at: proposedEnd?.toISOString() || "",
	};
	await previewAppointmentMutation(dialog.dataset.appointmentId, dialog.dataset.version, action, fields, dialog.querySelector("[data-appointment-csrf]").value);
	if (!window.confirm("Geprüfte Zeitänderung mit Alt/Neu-Vergleich speichern? Der Server prüft Version und Belegungen erneut.")) return;
	await appointmentAction(action, fields);
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

document.querySelector("[data-appointment-swap-search]")?.addEventListener("click", async () => {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const date = dialog?.querySelector("[data-appointment-swap-date]");
  const target = dialog?.querySelector("[data-appointment-swap-target]");
  if (!date?.value || !target) { showAppointmentError("Bitte wählen Sie ein Datum.", date); return; }
  const response = await fetch(`/api/v1/appointments/${encodeURIComponent(dialog.dataset.appointmentId)}/swap-candidates?date=${encodeURIComponent(date.value)}`, { headers: { Accept: "application/json" } });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) { showAppointmentFailure(calendarFailure(payload, response.status)); return; }
  target.replaceChildren(new Option("Bitte wählen", ""));
  (payload.candidates || []).forEach((candidate) => {
    const label = new Intl.DateTimeFormat("de-AT", { dateStyle: "short", timeStyle: "short", timeZone: "Europe/Vienna" }).format(new Date(candidate.start));
    const option = new Option(`${candidate.title} · ${label}`, candidate.id);
    option.dataset.version = candidate.version;
    target.add(option);
  });
  announceCalendar(payload.candidates?.length ? `${payload.candidates.length} tauschbare Vorschläge geladen.` : "Keine tauschbaren Vorschläge an diesem Datum gefunden.");
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
  clearAppointmentError();
  try {
    await previewAppointmentMutation(dialog.dataset.appointmentId, dialog.dataset.version, "fix", { without_notification_reason: reason }, dialog.querySelector("[data-appointment-csrf]").value);
    if (!window.confirm(`Prüfliste gelesen: Termin mit den angezeigten Fahrern und Ressourcen fixieren? Versandvormerkung: ${channels}; ${targets}.`)) return;
    await appointmentAction("fix", { without_notification_reason: reason });
  } catch (failure) { showAppointmentFailure(failure); }
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

async function calendarMutation(info, action, csrf) {
	const proposedStart = new Date(info.event.start);
	const proposedEnd = new Date(info.event.end);
  const previousStart = new Date(info.oldEvent?.start || info.event.start);
  const previousEnd = new Date(info.oldEvent?.end || info.event.end);
  const compareFormat = new Intl.DateTimeFormat("de-AT", { timeZone: "Europe/Vienna", dateStyle: "short", timeStyle: "short" });
  const comparison = action === "resize"
    ? `Dauer ändern: ${Math.round((previousEnd - previousStart) / 60000)} → ${Math.round((proposedEnd - proposedStart) / 60000)} Minuten?`
    : `Termin verschieben:\nAlt: ${compareFormat.format(previousStart)}–${compareFormat.format(previousEnd)}\nNeu: ${compareFormat.format(proposedStart)}–${compareFormat.format(proposedEnd)}?`;
  const form = new FormData();
  form.set("csrf_token", csrf);
  form.set("version", info.event.extendedProps.version);
  form.set("starts_at", info.event.start.toISOString());
  form.set("ends_at", info.event.end.toISOString());
  try {
    await previewAppointmentMutation(info.event.id, info.event.extendedProps.version, action, {
      starts_at: info.event.start.toISOString(), ends_at: info.event.end.toISOString(),
    }, csrf);
  } catch (failure) {
    info.revert(); showAppointmentFailure(failure); return;
  }
  if (!window.confirm(comparison)) { info.revert(); announceCalendar("Änderung abgebrochen."); return; }
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

const calendarOptions = document.querySelector(".calendar-options");
const calendarCompactMedia = window.matchMedia("(max-width: 1050px)");
if (calendarOptions) {
  const syncCalendarOptions = () => { calendarOptions.open = !calendarCompactMedia.matches; };
  syncCalendarOptions();
  calendarCompactMedia.addEventListener("change", syncCalendarOptions);
}

const calendarElement = document.querySelector("[data-calendar]");
if (calendarElement && window.FullCalendar) {
  const editable = calendarElement.dataset.editable === "true";
  const compact = calendarCompactMedia.matches;
  const calendarParameters = new URLSearchParams(window.location.search);
  const requestedAppointment = calendarParameters.get("appointment");
  const requestedViewName = calendarParameters.get("view");
  const requestedView = (compact && requestedViewName === "week" ? "listWeek" : { day: "timeGridDay", week: "timeGridWeek", month: "dayGridMonth", agenda: "listWeek" }[requestedViewName])
    || (compact ? "timeGridDay" : "timeGridWeek");
  const requestedWeekends = calendarParameters.get("weekends") !== "false";
  const viewParameter = (view) => ({ timeGridDay: "day", timeGridWeek: "week", dayGridMonth: "month", listWeek: "agenda" }[view] || "week");
  const toolbarForWidth = (narrow) => narrow
    ? { start: "prev,next", center: "title", end: "today,timeGridDay,dayGridMonth,listWeek" }
    : { start: "prev,next today", center: "title", end: "timeGridDay,timeGridWeek,dayGridMonth,listWeek" };
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
    headerToolbar: toolbarForWidth(compact),
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
          calendarElement.dataset.loadedAt = new Date().toISOString();
          const freshness = document.querySelector("[data-calendar-freshness]");
          if (freshness) freshness.textContent = `Zuletzt geladen: ${new Date().toLocaleTimeString("de-AT", { hour: "2-digit", minute: "2-digit" })} Uhr`;
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
      info.el.title = `${info.event.title} · ${info.event.extendedProps.status_label || "Termin"}`;
      if (info.event.start && info.event.end && info.event.start.toLocaleDateString("de-AT", { timeZone: "Europe/Vienna" }) !== info.event.end.toLocaleDateString("de-AT", { timeZone: "Europe/Vienna" })) info.el.dataset.crossMidnight = "true";
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
      window.history.replaceState(window.history.state, "", url);
      const range = document.querySelector("[data-calendar-range]");
      const rangeFormat = new Intl.DateTimeFormat("de-AT", { timeZone: "Europe/Vienna", weekday: "short", day: "2-digit", month: "2-digit", year: "numeric" });
      const inclusiveEnd = new Date(info.end.getTime() - 1);
      const label = `${rangeFormat.format(info.start)} bis ${rangeFormat.format(inclusiveEnd)}`;
      if (range) range.textContent = label;
      document.title = `${label} – Kalender – HackWerk`;
      const offsetName = (date) => new Intl.DateTimeFormat("de-AT", { timeZone: "Europe/Vienna", timeZoneName: "longOffset" }).formatToParts(date).find((part) => part.type === "timeZoneName")?.value;
      const dstChange = offsetName(info.start) !== offsetName(inclusiveEnd);
      if (dateInput) dateInput.title = dstChange ? "Dieser Bereich enthält einen Wechsel zwischen Sommer- und Winterzeit." : "Datum direkt auswählen";
    },
  });
  calendar.render();
  let calendarNarrow = compact;
  window.addEventListener("resize", () => {
    const narrow = window.matchMedia("(max-width: 1050px)").matches;
    if (narrow === calendarNarrow) return;
    calendarNarrow = narrow;
    calendar.setOption("headerToolbar", toolbarForWidth(narrow));
    if (narrow && calendar.view.type === "timeGridWeek") {
      calendar.changeView("listWeek");
      announceCalendar("Die Wochenansicht wird auf diesem Bildschirm als kompakte Agenda angezeigt.");
    }
  });
  window.hackWerkCalendar = calendar;
  const controls = document.querySelector("[data-calendar-controls]");
  const calendarDate = controls?.querySelector("[data-calendar-date]");
  const weekendToggle = controls?.querySelector("[data-calendar-weekends]");
  const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const zoneWarning = document.querySelector("[data-calendar-timezone-warning]");
  if (zoneWarning && browserZone !== "Europe/Vienna") { zoneWarning.hidden = false; zoneWarning.textContent = `Ihr Browser verwendet ${browserZone || "eine andere Zeitzone"}. HackWerk zeigt Termine verbindlich in Europe/Vienna.`; }
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
  controls?.querySelector("[data-calendar-reload]")?.addEventListener("click", () => { calendar.refetchEvents(); announceCalendar("Kalender wird neu geladen; Ansicht und Datum bleiben erhalten."); });
  controls?.querySelector("[data-calendar-share]")?.addEventListener("click", async () => { const link = new URL(window.location.href); link.searchParams.delete("appointment"); if (await copyText(link.href)) announceCalendar("Datenschutzarmer Ansichtslink kopiert."); });
  window.addEventListener("hackwerk:online", () => { if (calendarElement.dataset.loadFailed === "true") calendar.refetchEvents(); });
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

const mobileMenu = document.querySelector("[data-mobile-menu]");
const mobileMenuTrigger = document.querySelector("[data-mobile-menu-open]");
const closeMobileMenu = () => {
  if (mobileMenu instanceof HTMLDialogElement && mobileMenu.open) mobileMenu.close();
};
if (mobileMenu instanceof HTMLDialogElement && mobileMenuTrigger instanceof HTMLElement) {
	const focusableMobileMenuControls = () => Array.from(mobileMenu.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled])'))
		.filter((control) => control instanceof HTMLElement && control.getClientRects().length > 0);
  mobileMenuTrigger.addEventListener("click", () => {
    if (mobileMenu.open) return;
    mobileMenu.showModal();
    mobileMenuTrigger.setAttribute("aria-expanded", "true");
    mobileMenu.querySelector("[data-mobile-menu-close]")?.focus();
  });
  mobileMenu.querySelector("[data-mobile-menu-close]")?.addEventListener("click", closeMobileMenu);
  mobileMenu.querySelectorAll("a").forEach((link) => link.addEventListener("click", closeMobileMenu));
  mobileMenu.addEventListener("click", (event) => {
    if (event.target === mobileMenu) closeMobileMenu();
  });
	mobileMenu.addEventListener("keydown", (event) => {
		if (event.key !== "Tab") return;
		const controls = focusableMobileMenuControls();
		if (controls.length === 0) return;
		const first = controls[0];
		const last = controls[controls.length - 1];
		if (event.shiftKey && document.activeElement === first) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && document.activeElement === last) {
			event.preventDefault();
			first.focus();
		}
	});
  mobileMenu.addEventListener("close", () => {
    mobileMenuTrigger.setAttribute("aria-expanded", "false");
    mobileMenuTrigger.focus();
  });
  document.documentElement.classList.add("mobile-menu-dialog-ready");
}

const popoverMenus = Array.from(document.querySelectorAll("[data-popover-menu]"));
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
    .filter((node) => node.getClientRects().length > 0 && getComputedStyle(node).visibility !== "hidden")
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
window.addEventListener("resize", updateDashboardCountdown);

document.querySelectorAll("[data-copy-source]").forEach((button) => {
  button.addEventListener("click", async () => {
    const input = document.getElementById(button.dataset.copySource);
    if (!input) return;
    const original = button.textContent;
    if (await copyText(input.value, input)) {
      button.textContent = "Kopiert";
      announce("In die Zwischenablage kopiert.");
      window.setTimeout(() => { button.textContent = original; }, 1800);
    }
  });
});

document.querySelectorAll("[data-copy-value]").forEach((button) => {
  button.addEventListener("click", async () => {
    const value = String(button.dataset.copyValue || "").trim();
    if (!value) return;
    const original = button.textContent;
    const iconOnly = button.hasAttribute("data-copy-icon");
    const originalLabel = button.getAttribute("aria-label");
    const originalTitle = button.getAttribute("title");
    if (await copyText(value)) {
      if (iconOnly) {
        button.classList.add("is-copied");
        button.setAttribute("aria-label", originalLabel?.replace(/kopieren$/i, "kopiert") || "Kopiert");
        button.setAttribute("title", "Kopiert");
      } else {
        button.textContent = "Kopiert";
      }
      announce("In die Zwischenablage kopiert.");
      window.setTimeout(() => {
        if (iconOnly) {
          button.classList.remove("is-copied");
          if (originalLabel === null) button.removeAttribute("aria-label");
          else button.setAttribute("aria-label", originalLabel);
          if (originalTitle === null) button.removeAttribute("title");
          else button.setAttribute("title", originalTitle);
        } else {
          button.textContent = original;
        }
      }, 1800);
    }
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
  const renderFilters = () => {
    rows.forEach((row) => { row.hidden = !visible(row); });
    const count = rows.filter((row) => !row.hidden).length;
    const label = workbench.querySelector("[data-planning-visible-count]");
    if (label) label.textContent = `${count} sichtbar`;
    workbench.dispatchEvent(new CustomEvent("planningfilterchange", { bubbles: true, detail: { count } }));
  };
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
	workbench.querySelector("[data-planning-reset]")?.addEventListener("click", () => { rows.forEach((row) => { const box = row.querySelector('input[name="job_id"]'); if (box) box.checked = false; }); if (search) search.value = ""; if (radius) radius.value = ""; if (region) region.value = ""; renderFilters(); update(); });
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
      lock.addEventListener("release", () => { lock = undefined; update(); announce("Der Bildschirm darf wieder schlafen. Aktivieren Sie die Wachhaltung bei Bedarf erneut."); }, { once: true });
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
  const fileInput = voiceCapture.querySelector("[data-voice-file]");
  const durationInput = voiceCapture.querySelector("[data-voice-duration]");
  const idempotencyInput = voiceCapture.querySelector("[data-voice-idempotency-key]");
  const preview = voiceCapture.querySelector("[data-voice-preview]");
  const previewAudio = voiceCapture.querySelector("[data-voice-audio]");
  const sendButton = voiceCapture.querySelector("[data-voice-send]");
  const discardButton = voiceCapture.querySelector("[data-voice-discard]");
  const retryButton = voiceCapture.querySelector("[data-voice-retry]");
  const progress = voiceCapture.querySelector("[data-voice-progress]");
  const levelWrap = voiceCapture.querySelector("[data-voice-level-wrap]");
  const level = voiceCapture.querySelector("[data-voice-level]");
  const maxSeconds = Number(voiceCapture.dataset.maxSeconds);
  let recorder;
  let stream;
  let chunks = [];
  let accumulatedMs = 0;
  let segmentStartedAt = 0;
  let interval;
  let cancelled = false;
  let audioContext;
  let levelFrame;
  let peakLevel = 0;
  let pendingAudio;
  let pendingDurationMs = 0;
  let previewURL = "";
  let pendingUploadKey = idempotencyInput?.value || "";
  let cancelDurationProbe = () => {};
  let settingDuration = false;

  const setDurationValue = (seconds, source) => {
    if (!durationInput || !Number.isFinite(seconds) || seconds <= 0) return;
    settingDuration = true;
    durationInput.value = String(Math.max(1, Math.ceil(seconds)));
    durationInput.dataset.durationSource = source;
    durationInput.dispatchEvent(new Event("input", { bubbles: true }));
    settingDuration = false;
  };
  const prefillAudioDuration = (blob, fallbackSeconds = 0) => {
    if (!durationInput || !blob) return;
    cancelDurationProbe();
    if (fallbackSeconds > 0) {
      setDurationValue(fallbackSeconds, "recording");
    } else {
      settingDuration = true;
      durationInput.value = "";
      delete durationInput.dataset.durationSource;
      settingDuration = false;
    }
    const probe = document.createElement("audio");
    const objectURL = URL.createObjectURL(blob);
    let timeout;
    let settled = false;
    let attemptedSeek = false;
    const cleanup = () => {
      if (settled) return;
      settled = true;
      window.clearTimeout(timeout);
      probe.removeAttribute("src");
      probe.load();
      URL.revokeObjectURL(objectURL);
      if (cancelDurationProbe === cleanup) cancelDurationProbe = () => {};
    };
    const readDuration = () => {
      if (settled) return;
      const seconds = probe.duration;
      if (Number.isFinite(seconds) && seconds > 0) {
        if (durationInput.dataset.durationSource !== "manual") setDurationValue(seconds, "metadata");
        cleanup();
        return;
      }
      if (seconds === Infinity && !attemptedSeek) {
        attemptedSeek = true;
        try { probe.currentTime = 1e101; } catch { cleanup(); }
      }
    };
    cancelDurationProbe = cleanup;
    probe.preload = "metadata";
    probe.addEventListener("loadedmetadata", readDuration);
    probe.addEventListener("durationchange", readDuration);
    probe.addEventListener("timeupdate", readDuration);
    probe.addEventListener("error", cleanup, { once: true });
    timeout = window.setTimeout(cleanup, 5000);
    probe.src = objectURL;
    probe.load();
  };
  durationInput?.addEventListener("input", () => {
    if (!settingDuration) durationInput.dataset.durationSource = "manual";
  });
  fileInput?.addEventListener("change", () => {
    const audio = fileInput.files?.[0];
    if (audio) prefillAudioDuration(audio);
  });

  const newUploadKey = () => {
    if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
    const values = new Uint32Array(4);
    crypto.getRandomValues(values);
    return Array.from(values, (value) => value.toString(16).padStart(8, "0")).join("-");
  };
  const resetUploadKey = () => {
    pendingUploadKey = newUploadKey();
    if (idempotencyInput) idempotencyInput.value = pendingUploadKey;
  };

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
    window.cancelAnimationFrame(levelFrame);
    audioContext?.close().catch(() => {});
    audioContext = undefined;
    if (levelWrap) levelWrap.hidden = true;
  };
  const elapsedMs = () => accumulatedMs + (recorder?.state === "recording" && segmentStartedAt ? Date.now() - segmentStartedAt : 0);
  const freezeElapsed = () => {
	if (segmentStartedAt) accumulatedMs += Date.now() - segmentStartedAt;
	accumulatedMs = Math.min(maxSeconds * 1000, accumulatedMs);
    segmentStartedAt = 0;
  };
  const updateTimer = () => {
    const elapsed = Math.min(maxSeconds, Math.max(0, Math.ceil(elapsedMs() / 1000)));
    const remaining = Math.max(0, maxSeconds - elapsed);
    const minutes = Math.floor(remaining / 60);
    const seconds = remaining % 60;
    timer.textContent = `Verbleibend: ${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
    if (elapsed >= maxSeconds && recorder?.state !== "inactive") { freezeElapsed(); recorder.stop(); }
  };
  const uploadAudio = (blob, durationMs) => new Promise((resolve, reject) => {
    const form = new FormData();
    form.append("idempotency_key", pendingUploadKey);
    form.append("duration_ms", String(Math.max(1, Math.min(maxSeconds * 1000, durationMs))));
    form.append("audio", blob, "aufnahme.webm");
    announce("Aufnahme wird sicher übertragen und für die Verarbeitung eingereiht …");
    if (progress) { progress.hidden = false; progress.value = 0; }
    const request = new XMLHttpRequest();
    request.open("POST", "/api/v1/voice/drafts");
    request.responseType = "json";
    request.withCredentials = true;
    request.setRequestHeader("X-CSRF-Token", voiceCapture.dataset.csrf);
    request.setRequestHeader("Accept", "application/json");
    request.upload.addEventListener("progress", (event) => {
      if (!progress || !event.lengthComputable) return;
      progress.value = Math.round((event.loaded / event.total) * 100);
      announce(`Upload: ${progress.value} Prozent.`);
    });
    request.addEventListener("load", () => {
      const payload = request.response || {};
      if (request.status < 200 || request.status >= 300) {
        reject(new Error(payload?.error?.message || "Die Aufnahme konnte nicht verarbeitet werden."));
        return;
      }
      dirtyForms.delete(uploadForm);
      resolve(payload);
    });
    request.addEventListener("error", () => reject(new Error("Der Übertragungsstatus ist unbekannt. Die Aufnahme bleibt in diesem Tab erhalten; prüfen Sie zuerst Ihre Entwürfe und senden Sie sie nur kontrolliert erneut.")));
    request.addEventListener("abort", () => reject(new Error("Der Upload wurde abgebrochen. Die Aufnahme bleibt in diesem Tab erhalten.")));
    request.send(form);
  });
  const clearPreview = () => {
    pendingAudio = undefined;
    pendingDurationMs = 0;
    if (previewURL) URL.revokeObjectURL(previewURL);
    previewURL = "";
    previewAudio?.removeAttribute("src");
    if (preview) preview.hidden = true;
    if (retryButton) retryButton.hidden = true;
    if (progress) progress.hidden = true;
    resetUploadKey();
  };
  const submitPendingAudio = async () => {
    if (!pendingAudio) return;
    if (sendButton) sendButton.disabled = true;
    if (retryButton) retryButton.hidden = true;
    try {
      const payload = await uploadAudio(pendingAudio, pendingDurationMs);
      window.location.assign(payload.location);
    } catch (failure) {
      announce(failure.message);
      if (retryButton) retryButton.hidden = false;
      if (sendButton) sendButton.disabled = false;
    }
  };
  const supportedType = () => ["audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus"].find((type) => window.MediaRecorder?.isTypeSupported(type));

  if (!navigator.mediaDevices?.getUserMedia || !window.MediaRecorder || !supportedType()) {
    startButton.disabled = true;
    announce("Dieser Browser unterstützt die Mikrofonaufnahme nicht. Sie können eine Datei hochladen oder vollständig manuell erfassen.");
  } else {
    startButton.addEventListener("click", async () => {
      try {
        resetUploadKey();
        stream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false });
        chunks = [];
        cancelled = false;
		accumulatedMs = 0;
        peakLevel = 0;
        if (window.AudioContext) {
          audioContext = new AudioContext();
          const analyser = audioContext.createAnalyser();
          analyser.fftSize = 256;
          audioContext.createMediaStreamSource(stream).connect(analyser);
          const values = new Uint8Array(analyser.fftSize);
          const sampleLevel = () => {
            analyser.getByteTimeDomainData(values);
            const current = values.reduce((maximum, value) => Math.max(maximum, Math.abs(value - 128) / 128), 0);
            peakLevel = Math.max(peakLevel, current);
            if (level) level.value = current;
            levelFrame = window.requestAnimationFrame(sampleLevel);
          };
          if (levelWrap) levelWrap.hidden = false;
          sampleLevel();
        }
        recorder = new MediaRecorder(stream, { mimeType: supportedType() });
        recorder.addEventListener("dataavailable", (event) => { if (event.data.size) chunks.push(event.data); });
        recorder.addEventListener("stop", async () => {
		  freezeElapsed();
          const durationMs = Math.max(1, accumulatedMs);
          const blob = new Blob(chunks, { type: recorder.mimeType });
          stopTracks(); resetControls();
          if (cancelled) return;
          pendingAudio = blob;
          pendingDurationMs = durationMs;
          prefillAudioDuration(blob, durationMs / 1000);
          if (previewURL) URL.revokeObjectURL(previewURL);
          previewURL = URL.createObjectURL(blob);
          previewAudio.src = previewURL;
          preview.hidden = false;
          announce(peakLevel < 0.015
            ? "Die Aufnahme ist sehr leise oder möglicherweise leer. Hören Sie sie vor dem Upload an und nehmen Sie sie bei Bedarf neu auf."
            : "Aufnahme beendet. Bitte anhören und erst danach bewusst hochladen oder verwerfen.");
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
      chunks = []; stopTracks(); resetControls(); timer.textContent = `Verbleibend: ${String(Math.floor(maxSeconds / 60)).padStart(2, "0")}:${String(maxSeconds % 60).padStart(2, "0")}`; clearPreview(); announce("Aufnahme verworfen; nichts wurde hochgeladen.");
    });
  }
  sendButton?.addEventListener("click", submitPendingAudio);
  retryButton?.addEventListener("click", submitPendingAudio);
  discardButton?.addEventListener("click", () => { clearPreview(); announce("Aufnahme verworfen; nichts wurde hochgeladen."); });
  uploadForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    const audio = uploadForm.elements.audio.files[0];
    const seconds = Number(uploadForm.elements.duration_seconds.value);
    if (!audio || !Number.isFinite(seconds) || seconds <= 0 || seconds > maxSeconds) { announce("Bitte Datei und gültige Dauer innerhalb des Limits angeben."); return; }
    resetUploadKey();
    uploadForm.querySelector("button[type='submit']").disabled = true;
    pendingAudio = audio;
    pendingDurationMs = seconds * 1000;
    try {
      const payload = await uploadAudio(audio, pendingDurationMs);
      window.location.assign(payload.location);
    } catch (failure) {
      announce(failure.message);
      if (previewURL) URL.revokeObjectURL(previewURL);
      previewURL = URL.createObjectURL(audio);
      if (previewAudio) previewAudio.src = previewURL;
      preview.hidden = false;
      retryButton.hidden = false;
      uploadForm.querySelector("button[type='submit']").disabled = false;
    }
  });
  window.addEventListener("pagehide", () => {
    cancelled = true;
    if (recorder && recorder.state !== "inactive") recorder.stop();
    chunks = [];
    clearPreview();
    stopTracks();
  });
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
      if (!window.maplibregl || typeof window.maplibregl.setWorkerUrl !== "function") {
        reject(new Error("Kartenbibliothek konnte nicht gestartet werden"));
        return;
      }
      window.maplibregl.setWorkerUrl(assets.worker);
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
  const customerAddressLabel = editor.querySelector("[data-location-customer-address]");
  const customerFields = editor.closest("form")?.querySelector("[data-customer-fields]");
  const customerFieldValue = (name) => String(customerFields?.querySelector(`[name="${name}"]`)?.value || "").trim();
  const currentCustomerAddress = () => {
    if (!customerFields) return String(editor.dataset.customerAddress || "").trim();
    const street = customerFieldValue("street");
    const postalCode = customerFieldValue("postal_code");
    const locality = customerFieldValue("locality");
    const region = customerFieldValue("region");
    const localityLine = [postalCode, locality].filter(Boolean).join(" ");
    const structured = [street, localityLine, region].filter(Boolean);
    if (structured.length > 0) return [...structured, "AT"].join(", ");
    return customerFieldValue("address_freeform") || String(editor.dataset.customerAddress || "").trim();
  };
  const updateCustomerAddressLabel = () => {
    if (!customerAddressLabel) return;
    customerAddressLabel.textContent = currentCustomerAddress() || "Adresse aus den aktuellen Kundendaten verwenden";
  };
  customerFields?.querySelectorAll("[name='street'], [name='postal_code'], [name='locality'], [name='region'], [name='address_freeform']")
    .forEach((input) => input.addEventListener("input", updateCustomerAddressLabel));
  updateCustomerAddressLabel();
  const clearSearchResults = () => {
    searchResults?.replaceChildren();
    if (searchResults) searchResults.hidden = true;
  };
  const showSearchStatus = (text) => {
    if (searchStatus) searchStatus.textContent = text;
  };
  let searchSequence = 0;
  let searchController;
  const stopSearchBusy = () => {
    if (!searchSubmit) return;
    searchSubmit.disabled = false;
    searchSubmit.removeAttribute("aria-busy");
  };
  const searchAddress = async (selectionMode = "map") => {
    const sequence = ++searchSequence;
    searchController?.abort();
    searchController = new AbortController();
    const query = String(searchInput?.value || "").trim();
    if (query.length < 3) {
      stopSearchBusy();
      clearSearchResults();
      showSearchStatus("Bitte mindestens drei Zeichen eingeben.");
      searchInput?.focus();
      return;
    }
    const csrf = editor.closest("form")?.querySelector("[name='csrf_token']")?.value;
    if (!csrf) {
      stopSearchBusy();
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
        signal: searchController.signal,
      });
      const payload = await response.json().catch(() => ({}));
      if (sequence !== searchSequence) return;
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
          if (selectionMode === "customer") {
            setDraft(point, "customer_address", `Kundenadresse „${label}“ gewählt. Bitte prüfen und anschließend „Standort übernehmen“ wählen.`);
            showSearchStatus(`Kundenadresse „${label}“ als Standortentwurf gewählt.`);
            latitudeInput.focus({ preventScroll: true });
            return;
          }
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
      if (sequence !== searchSequence || error?.name === "AbortError") return;
      clearSearchResults();
      showSearchStatus(error instanceof Error ? error.message : "Die Adresssuche ist derzeit nicht verfügbar.");
    } finally {
      if (sequence !== searchSequence) return;
      stopSearchBusy();
    }
  };

  searchSubmit?.addEventListener("click", () => searchAddress());
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
    if (point) {
      setDraft(point, "customer_address", "Kundenadresse geladen. Bitte prüfen und anschließend übernehmen.");
      return;
    }
    const address = currentCustomerAddress();
    if (!address) {
      announce("Bitte zuerst eine Kundenadresse erfassen oder den Haufenstandort auf der Karte wählen.", badge?.textContent || "Fehlt");
      return;
    }
    if (!searchInput || searchInput.disabled) {
      announce("Die Kundenadresse hat noch keine Koordinaten und die Adresssuche ist nicht konfiguriert. Setzen Sie den Marker oder geben Sie Koordinaten ein.", badge?.textContent || "Fehlt");
      return;
    }
    searchInput.value = address;
    showSearchStatus("Kundenadresse wird gesucht …");
    announce("Kundenadresse wird zur Auswahl vorbereitet …", badge?.textContent || "Fehlt");
    searchAddress("customer");
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

document.querySelector("[data-appointment-assign]")?.addEventListener("click", async () => {
  const dialog = document.querySelector("[data-appointment-dialog]");
  const assignment = dialog?.querySelector("[data-appointment-assignment]");
  const drivers = [...(assignment?.querySelectorAll("[data-appointment-driver]:checked") || [])].map((input) => input.value);
  const primary = assignment?.querySelector("[data-appointment-primary-driver]");
  const chipper = assignment?.querySelector("[data-appointment-chipper]");
  const otherResources = [...(assignment?.querySelectorAll("[data-appointment-other-resource]:checked") || [])].map((input) => input.value);
  if (!drivers.length || !primary?.value || !drivers.includes(primary.value)) {
    showAppointmentError("Bitte wählen Sie mindestens einen Fahrer und daraus den Primärfahrer.", primary);
    return;
  }
  if (!chipper?.value) {
    showAppointmentError("Bitte wählen Sie eine Hackmaschine.", chipper);
    return;
  }
  clearAppointmentError();
  const fields = {
    driver_id: drivers,
    primary_driver_id: primary.value,
    chipper_resource_id: chipper.value,
    transport_resource_id: assignment.querySelector("[data-appointment-transport]")?.value || "",
    trailer_resource_id: assignment.querySelector("[data-appointment-trailer]")?.value || "",
    other_resource_id: otherResources,
    override_reason: assignment.querySelector("[data-appointment-assignment-override]")?.value.trim() || "",
  };
  try {
    await previewAppointmentMutation(dialog.dataset.appointmentId, dialog.dataset.version, "assign", fields, dialog.querySelector("[data-appointment-csrf]").value);
    if (!window.confirm("Geprüfte Fahrer- und Ressourcenzuweisung speichern? Der Server prüft Belegungen erneut.")) return;
    await appointmentAction("assign", fields);
  } catch (failure) {
    showAppointmentFailure(failure);
  }
});

const voiceProcessing = document.querySelector("[data-voice-processing]");
if (voiceProcessing) {
  const message = voiceProcessing.querySelector("[data-voice-processing-message]");
  const statusURL = voiceProcessing.dataset.statusUrl;
  let stopped = false;
  const poll = async () => {
    if (stopped || !statusURL) return;
    try {
      const response = await fetch(statusURL, { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } });
      if ([401, 403, 404].includes(response.status)) {
        stopped = true;
        if (message) message.textContent = response.status === 404
          ? "Der Entwurf ist nicht mehr verfügbar. Öffnen Sie die Spracheingabe erneut."
          : "Ihre Sitzung ist abgelaufen. Laden Sie die Seite neu und melden Sie sich erneut an.";
        return;
      }
      if (!response.ok) throw new Error("status unavailable");
      const payload = await response.json();
      if (["needs_review", "failed", "expired", "committed"].includes(payload.status)) {
        window.location.reload();
        return;
      }
      if (message) message.textContent = payload.status === "transcribing"
        ? "Die Aufnahme wird gerade transkribiert. Sie können währenddessen weiterarbeiten."
        : "Die Aufnahme wartet auf den lokalen Sprachdienst. Sie können währenddessen weiterarbeiten.";
    } catch {
      if (message) message.textContent = "Der Status konnte gerade nicht aktualisiert werden. Die Verarbeitung läuft im Hintergrund weiter; versuchen Sie es später erneut.";
    }
    window.setTimeout(poll, 3000);
  };
  window.addEventListener("pagehide", () => { stopped = true; }, { once: true });
  window.setTimeout(poll, 1500);
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
  let currentStart = start;
  let currentEnd = end;
  const stopJobIDs = new Set(stops.map((stop) => stop.jobID).filter(Boolean));
  const visibleCandidates = candidates.filter((candidate) => !stopJobIDs.has(candidate.jobID));
  const filteredCandidates = () => visibleCandidates.filter((candidate) => !candidate.element.hidden);
  candidates.forEach((candidate) => {
    const syncRow = () => candidate.element.classList.toggle("route-candidate--selected", candidate.checkbox.checked);
    candidate.checkbox.addEventListener("change", syncRow);
    syncRow();
  });
  const allCoordinates = () => {
    const sameEndpoint = currentStart && currentEnd && Math.abs(currentStart.latitude - currentEnd.latitude) < 1e-7 && Math.abs(currentStart.longitude - currentEnd.longitude) < 1e-7;
    return [
      ...(currentStart ? [[currentStart.longitude, currentStart.latitude]] : []),
      ...(currentEnd && !sameEndpoint ? [[currentEnd.longitude, currentEnd.latitude]] : []),
      ...geometryCoordinates,
      ...stops.map((stop) => [stop.point.longitude, stop.point.latitude]),
      ...filteredCandidates().map((candidate) => [candidate.point.longitude, candidate.point.latitude]),
    ];
  };

  const first = allCoordinates()[0];
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
    const coordinates = allCoordinates();
    if (coordinates.length === 0) {
      map.fitBounds([[9.5, 46.3], [17.3, 49.2]], { padding: 36, maxZoom: 7, duration: 0 });
      return;
    }
    if (coordinates.length === 1) {
      map.easeTo({ center: coordinates[0], zoom: 11, duration: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 300 });
      return;
    }
    const bounds = new maplibregl.LngLatBounds();
    coordinates.forEach((coordinate) => bounds.extend(coordinate));
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
  const updateStartButton = () => {
    startButton?.toggleAttribute("disabled", !currentStart);
    if (!currentStart) startButton?.setAttribute("title", "Wählen Sie zuerst einen Startort.");
    else startButton?.removeAttribute("title");
  };
  startButton?.addEventListener("click", () => {
    if (currentStart) map.easeTo({ center: [currentStart.longitude, currentStart.latitude], zoom: 13 });
  });
  updateStartButton();
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
    const candidatesInFilter = filteredCandidates();
    const count = [...stops, ...candidatesInFilter].filter((item) => bounds.contains([item.point.longitude, item.point.latitude])).length;
    const label = context.querySelector("[data-route-map-count]");
    if (label) label.textContent = `${count}/${stops.length + candidatesInFilter.length} Punkte sichtbar`;
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
  let startMarker = null;
  let endMarker = null;
  let endpointSelectionChanged = false;
  const selectedEndpoint = (prefix) => {
    const picker = context.querySelector(`[data-route-location-prefix="${prefix}"]`);
    const choice = picker?.querySelector("[data-route-location-choice]:checked");
    if (!picker || !choice) return { point: null, label: prefix === "start" ? "Startort" : "Endort" };
    if (choice.dataset.routeLocationKind === "saved") {
      return {
        point: mapPoint(choice.dataset.routeLocationLatitude, choice.dataset.routeLocationLongitude),
        label: String(choice.dataset.routeLocationLabel || (prefix === "start" ? "Startort" : "Endort")).trim(),
      };
    }
    if (choice.dataset.routeLocationKind === "last-stop") {
      const last = stops.at(-1);
      return { point: last?.point || null, label: last?.label || "Letzter Stopp" };
    }
    return {
      point: mapPoint(picker.querySelector("[data-route-location-latitude]")?.value, picker.querySelector("[data-route-location-longitude]")?.value),
      label: String(picker.querySelector("[data-route-location-label]")?.value || picker.querySelector("[data-route-location-address]")?.value || (prefix === "start" ? "Startort" : "Endort")).trim(),
    };
  };
  const renderEndpointMarkers = (fromPicker = false) => {
    const startEndpoint = fromPicker
      ? selectedEndpoint("start")
      : { point: start, label: String(canvas.dataset.routeStartLabel || selectedStart?.dataset.routeLocationLabel || "Startort").trim() };
    const endEndpoint = fromPicker
      ? selectedEndpoint("end")
      : { point: end, label: String(canvas.dataset.routeEndLabel || selectedEnd?.dataset.routeLocationLabel || "Endort").trim() };
    currentStart = startEndpoint.point;
    currentEnd = endEndpoint.point;
    updateStartButton();
    startMarker?.remove();
    endMarker?.remove();
    startMarker = null;
    endMarker = null;
    const sameEndpoint = currentStart && currentEnd && Math.abs(currentStart.latitude - currentEnd.latitude) < 1e-7 && Math.abs(currentStart.longitude - currentEnd.longitude) < 1e-7;
    if (currentStart) {
      const popup = routePopupContent({ label: startEndpoint.label || "Startort", customer: sameEndpoint ? "Start und Ende der Route" : "Start der Route" });
      startMarker = new maplibregl.Marker({ element: startMarkerElement(), anchor: "bottom" })
        .setLngLat([currentStart.longitude, currentStart.latitude])
        .setPopup(new maplibregl.Popup({ offset: 22 }).setDOMContent(popup.content))
        .addTo(map);
    }
    if (currentEnd && !sameEndpoint) {
      const popup = routePopupContent({ label: endEndpoint.label || "Endort", customer: "Ende der Route" });
      endMarker = new maplibregl.Marker({ element: endMarkerElement(), anchor: "bottom" })
        .setLngLat([currentEnd.longitude, currentEnd.latitude])
        .setPopup(new maplibregl.Popup({ offset: 22 }).setDOMContent(popup.content))
        .addTo(map);
    }
  };
  context.addEventListener("route-location-status", () => {
    endpointSelectionChanged = true;
    if (!ready) return;
    renderEndpointMarkers(true);
    fitAll();
    updateVisibleCount();
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

    renderEndpointMarkers(endpointSelectionChanged);

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
      features: filteredCandidates().map((candidate) => ({
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

    const applyPlanningFilterToMap = () => {
      if (clusteredSource) clusteredSource.setData(clusteredData());
      candidateMarkers.forEach((markerElement, jobID) => {
        const candidate = visibleCandidates.find((item) => item.jobID === jobID);
        markerElement.hidden = Boolean(candidate?.element.hidden);
      });
      updateVisibleCount();
      fitAll();
    };
    (context.closest("[data-planning-workbench]") || context).addEventListener("planningfilterchange", applyPlanningFilterToMap);
    applyPlanningFilterToMap();

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
    dirtyForms.add(order);
    order.dataset.routeOrderDirty = "true";
    const saveButton = order.querySelector("[data-route-order-save-button]");
    const saveStatus = order.querySelector("[data-route-order-save-status]");
    if (saveButton) saveButton.textContent = "Geänderte Fahrreihenfolge speichern";
    if (saveStatus) saveStatus.textContent = "Die Reihenfolge ist nur in dieser Ansicht geändert. Speichern Sie sie, bevor Sie die Seite verlassen.";
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
