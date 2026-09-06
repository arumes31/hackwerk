//go:build e2e

package e2e_test

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/app"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/voice"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestTask09VoiceReviewMobileJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, _, _, _, _, _, driverPassword := task04Application(t, databaseURL)
	customerService, err := app.CustomerService(pool)
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Europe/Vienna")
	voiceService, err := voice.New(postgres.NewVoiceStore(pool), voice.FakeTranscriber{Text: "Franz Huber, Unterneukirchen 15, Telefonnummer 0664 1234567, ungefähr 80 Kubikmeter Holz, ungefähr drei Stunden Hackzeit, möglichst Anfang September"}, voice.RuleExtractor{}, voice.Config{Enabled: true, Retention: time.Hour, RecordingRetention: 24 * time.Hour, RateLimitPerMinute: 10, ConcurrentPerUser: 2, Timezone: location}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	workerContext, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)
	go func() {
		for {
			processed, processErr := voiceService.ProcessNext(workerContext, "e2e-voice-worker", time.Minute)
			if processErr != nil {
				if workerContext.Err() != nil {
					workerDone <- nil
				} else {
					workerDone <- processErr
				}
				return
			}
			if processed {
				continue
			}
			select {
			case <-workerContext.Done():
				workerDone <- nil
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	t.Cleanup(func() {
		cancelWorker()
		if workerErr := <-workerDone; workerErr != nil {
			t.Errorf("voice worker: %v", workerErr)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	cfg := config.Config{AppName: "HackWerk", BaseURL: "http://" + server.Listener.Addr().String(), Timezone: "Europe/Vienna", Database: config.Database{ReadinessTimeout: 2 * time.Second}, Auth: config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour}, Voice: config.Voice{Enabled: true, Transcriber: "fake", MaxDuration: 90 * time.Second, MaxBytes: 15 << 20, ProviderTimeout: 5 * time.Second, TempDir: t.TempDir(), ExternalProviderNote: "Testprovider"}}
	router, err := web.NewRouter(web.Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Customers: customerService, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: e2eDashboard(t, pool), Voice: voiceService})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = router
	server.Start()
	t.Cleanup(server.Close)
	audioPath := filepath.Join(t.TempDir(), "voice-fixture.wav")
	if err = os.WriteFile(audioPath, minimalWAV(), 0o600); err != nil {
		t.Fatal(err)
	}
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.Flag("autoplay-policy", "no-user-gesture-required"), chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(360, 800))
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	t.Cleanup(cancelBrowser)
	browser, cancelTimeout := context.WithTimeout(browser, 180*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browser) })
	if err = chromedp.Run(browser, chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery), chromedp.SetValue("#username", "driver-task04", chromedp.ByQuery), chromedp.SetValue("#password", driverPassword, chromedp.ByQuery), chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery), chromedp.Navigate(server.URL+"/voice"), chromedp.WaitVisible("[data-voice-upload]", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var nativeFallbackConfigured bool
	if err = runBrowserStep(browser, "native upload without JavaScript",
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(true).Do(ctx) }),
		chromedp.Reload(),
		chromedp.WaitVisible("[data-voice-upload]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {const form=document.querySelector('[data-voice-upload]');return form.method==='post'&&new URL(form.action).pathname==='/voice/upload'&&form.enctype==='multipart/form-data'&&Boolean(form.elements.csrf_token.value)})()`, &nativeFallbackConfigured),
		chromedp.SetUploadFiles("[data-voice-upload] input[type=file]", []string{audioPath}, chromedp.ByQuery),
		chromedp.SetValue("[data-voice-upload] input[name=duration_seconds]", "3", chromedp.ByQuery),
		chromedp.Click("[data-voice-upload] button[type=submit]", chromedp.ByQuery),
		chromedp.WaitReady("[data-voice-processing], form[action$='/commit']", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				var status string
				if queryErr := pool.QueryRow(ctx, "SELECT status FROM voice_drafts ORDER BY created_at DESC LIMIT 1").Scan(&status); queryErr != nil {
					return queryErr
				}
				if status == string(voice.StatusNeedsReview) {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
			}
		}),
		chromedp.Reload(),
		chromedp.WaitVisible("form[action$='/commit']", chromedp.ByQuery),
		chromedp.Click("form[action$='/discard'] button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-voice-upload]", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(false).Do(ctx) }),
		chromedp.Reload(),
		chromedp.WaitVisible("[data-voice-upload]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !nativeFallbackConfigured {
		t.Fatal("native multipart fallback is not fully configured")
	}
	var inferredDuration string
	if err = runBrowserStep(browser, "prefill selected audio duration from metadata",
		chromedp.SetUploadFiles("[data-voice-file]", []string{audioPath}, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-voice-duration]')?.value==='3'`, nil),
		chromedp.Value("[data-voice-duration]", &inferredDuration, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-voice-upload]').reset()`, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if inferredDuration != "3" {
		t.Fatalf("selected WAV duration = %q, want 3 seconds", inferredDuration)
	}
	uploadRequests := make(chan observedVoiceUploadRequest, 8)
	chromedp.ListenTarget(browser, func(event any) {
		requestEvent, ok := event.(*network.EventRequestWillBeSent)
		if !ok || requestEvent.Request.Method != "POST" {
			return
		}
		requestURL, err := url.Parse(requestEvent.Request.URL)
		if err != nil || (requestURL.Path != "/api/v1/voice/drafts" && requestURL.Path != "/voice/upload") {
			return
		}
		observation := observedVoiceUploadRequest{Path: requestURL.Path}
		for name, value := range requestEvent.Request.Headers {
			headerValue, ok := value.(string)
			if !ok {
				continue
			}
			switch {
			case strings.EqualFold(name, "X-CSRF-Token"):
				observation.HasCSRF = strings.TrimSpace(headerValue) != ""
			case strings.EqualFold(name, "Content-Type"):
				observation.Multipart = strings.HasPrefix(strings.ToLower(strings.TrimSpace(headerValue)), "multipart/form-data;")
			}
		}
		select {
		case uploadRequests <- observation:
		default:
		}
	})
	if err = chromedp.Run(browser, network.Enable()); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err = runBrowserStep(browser, "reject empty native recording",
		chromedp.Evaluate(emptyMediaRecorderInstallScript, nil),
		chromedp.Click("[data-voice-start]", chromedp.ByQuery),
		chromedp.Poll(`!document.querySelector('[data-voice-stop]')?.disabled`, nil),
		chromedp.Click("[data-voice-stop]", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-voice-status]')?.textContent.includes('keine verwertbare Aufnahme') && document.querySelector('[data-voice-preview]')?.hidden`, nil),
		chromedp.Evaluate(emptyMediaRecorderRestoreScript, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	select {
	case request := <-uploadRequests:
		t.Fatalf("empty native recording unexpectedly caused POST %s", request.Path)
	default:
	}
	var recorded mediaRecorderFixture
	if err = runBrowserStep(browser, "generate Edge MediaRecorder fixture", chromedp.Evaluate(mediaRecorderFixtureScript, &recorded, awaitJavaScriptPromise)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !recorded.Supported || recorded.Size < 64 || recorded.FileCount != 1 {
		t.Fatalf("Edge MediaRecorder support/size/files=%v/%d/%d mime=%q capabilities recorder/webm-opus/webm/ogg-opus/audio-context/data-transfer/file=%v/%v/%v/%v/%v/%v/%v",
			recorded.Supported, recorded.Size, recorded.FileCount, recorded.MIMEType,
			recorded.MediaRecorder, recorded.AudioWebMOpus, recorded.AudioWebM, recorded.AudioOggOpus,
			recorded.AudioContext, recorded.DataTransfer, recorded.File)
	}
	if err = runBrowserStep(browser, "upload Edge MediaRecorder fixture through enhanced API", chromedp.Click("[data-voice-upload] button[type=submit]", chromedp.ByQuery), chromedp.WaitVisible("form[action$='/commit']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	observedRequests := make([]observedVoiceUploadRequest, 0, 2)
	select {
	case request := <-uploadRequests:
		observedRequests = append(observedRequests, request)
	case <-time.After(5 * time.Second):
		t.Fatal("no voice upload POST was observed")
	}
drainUploadRequests:
	for {
		select {
		case request := <-uploadRequests:
			observedRequests = append(observedRequests, request)
		default:
			break drainUploadRequests
		}
	}
	var enhancedPosts, nativePosts int
	var enhancedRequestValid bool
	for _, request := range observedRequests {
		switch request.Path {
		case "/api/v1/voice/drafts":
			enhancedPosts++
			enhancedRequestValid = enhancedRequestValid || (request.Multipart && request.HasCSRF)
		case "/voice/upload":
			nativePosts++
		}
	}
	if enhancedPosts != 1 || nativePosts != 0 || !enhancedRequestValid {
		t.Fatalf("enhanced/native voice POSTs=%d/%d valid multipart+CSRF=%v", enhancedPosts, nativePosts, enhancedRequestValid)
	}
	var reviewText string
	var fieldValues []string
	var overflow, smallTarget bool
	if err = chromedp.Run(browser, chromedp.Text("main", &reviewText, chromedp.ByQuery), chromedp.Evaluate(`['first_name','last_name','address_freeform','phone','volume_m3','hack_duration','preference_text'].map(n=>document.querySelector('[name="'+n+'"]').value)`, &fieldValues), chromedp.Evaluate(`document.documentElement.scrollWidth>window.innerWidth`, &overflow), chromedp.Evaluate(`Array.from(document.querySelectorAll('main button,main a.button')).some(e=>e.getBoundingClientRect().height<44||e.getBoundingClientRect().width<44)`, &smallTarget)); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Franz Huber", "Unterneukirchen 15", "80", "drei Stunden", "Anfang September", "prüfen", "Es wird kein Termin"} {
		if !strings.Contains(reviewText, expected) {
			t.Fatalf("review missing %q: %s", expected, reviewText)
		}
	}
	wantValues := []string{"Franz", "Huber", "Unterneukirchen 15", "0664 1234567", "80", "180", "Anfang September"}
	if strings.Join(fieldValues, "|") != strings.Join(wantValues, "|") {
		t.Fatalf("review field values=%v want=%v", fieldValues, wantValues)
	}
	if overflow || smallTarget {
		t.Fatalf("mobile overflow/small target=%v/%v", overflow, smallTarget)
	}
	if err = runBrowserStep(browser, "cancel draft discard",
		chromedp.Evaluate(`window.confirm=()=>false`, nil),
		chromedp.Click("form[action$='/discard'] button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible("form[action$='/commit']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var reviewDrafts int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM voice_drafts WHERE status='needs_review'").Scan(&reviewDrafts); err != nil {
		t.Fatal(err)
	}
	if reviewDrafts != 1 {
		t.Fatalf("cancelled discard changed draft state: needs_review=%d", reviewDrafts)
	}
	var customersBefore, jobsBefore, waitlistBefore, appointmentsBefore int
	for query, target := range map[string]*int{"SELECT count(*) FROM customers": &customersBefore, "SELECT count(*) FROM jobs": &jobsBefore, "SELECT count(*) FROM waitlist_entries": &waitlistBefore, "SELECT count(*) FROM appointments": &appointmentsBefore} {
		if err = pool.QueryRow(t.Context(), query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if err = runBrowserStep(browser, "review commit", chromedp.Click("input[name=reviewed]", chromedp.ByQuery), chromedp.Click("form[action$='/commit'] button[type=submit]", chromedp.ByQuery), chromedp.WaitVisible("main .detail-grid", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var customersAfter, jobsAfter, waitlistAfter, appointmentsAfter, outbox int
	for query, target := range map[string]*int{"SELECT count(*) FROM customers": &customersAfter, "SELECT count(*) FROM jobs": &jobsAfter, "SELECT count(*) FROM waitlist_entries": &waitlistAfter, "SELECT count(*) FROM appointments": &appointmentsAfter, "SELECT count(*) FROM outbox_events": &outbox} {
		if err = pool.QueryRow(t.Context(), query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if customersAfter != customersBefore+1 || jobsAfter != jobsBefore+1 || waitlistAfter != waitlistBefore+1 || appointmentsAfter != appointmentsBefore || outbox != 0 {
		t.Fatalf("before customer/job/waitlist/appointment=%d/%d/%d/%d after=%d/%d/%d/%d outbox=%d", customersBefore, jobsBefore, waitlistBefore, appointmentsBefore, customersAfter, jobsAfter, waitlistAfter, appointmentsAfter, outbox)
	}
	var source, lifecycle string
	if err = pool.QueryRow(t.Context(), "SELECT source,workflow_status FROM jobs ORDER BY created_at DESC LIMIT 1").Scan(&source, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if source != "voice" || lifecycle != "waitlist" {
		t.Fatalf("source/lifecycle=%s/%s", source, lifecycle)
	}
}

type mediaRecorderFixture struct {
	Supported     bool   `json:"supported"`
	MediaRecorder bool   `json:"mediaRecorder"`
	AudioWebMOpus bool   `json:"audioWebMOpus"`
	AudioWebM     bool   `json:"audioWebM"`
	AudioOggOpus  bool   `json:"audioOggOpus"`
	AudioContext  bool   `json:"audioContext"`
	DataTransfer  bool   `json:"dataTransfer"`
	File          bool   `json:"file"`
	MIMEType      string `json:"mimeType"`
	Size          int64  `json:"size"`
	FileCount     int    `json:"fileCount"`
}

func awaitJavaScriptPromise(parameters *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
	return parameters.WithAwaitPromise(true)
}

type observedVoiceUploadRequest struct {
	Path      string
	Multipart bool
	HasCSRF   bool
}

// The automated runtime-media matrix covers Chromium/Edge. Safari/WebKit
// MediaRecorder output requires a separate Apple-browser release check.
const mediaRecorderFixtureScript = `(async () => {
	const capabilities = {
		mediaRecorder: Boolean(window.MediaRecorder),
		audioWebMOpus: Boolean(window.MediaRecorder?.isTypeSupported("audio/webm;codecs=opus")),
		audioWebM: Boolean(window.MediaRecorder?.isTypeSupported("audio/webm")),
		audioOggOpus: Boolean(window.MediaRecorder?.isTypeSupported("audio/ogg;codecs=opus")),
		audioContext: Boolean(window.AudioContext || window.webkitAudioContext),
		dataTransfer: Boolean(window.DataTransfer),
		file: Boolean(window.File),
	};
	const mimeType = ["audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus"]
		.find((candidate) => window.MediaRecorder?.isTypeSupported(candidate));
	const AudioContextClass = window.AudioContext || window.webkitAudioContext;
	if (!mimeType || !AudioContextClass || !window.DataTransfer || !window.File) {
		return {...capabilities, supported: false, mimeType: mimeType || "", size: 0, fileCount: 0};
	}
	const context = new AudioContextClass();
	await context.resume();
	const oscillator = context.createOscillator();
	const gain = context.createGain();
	const destination = context.createMediaStreamDestination();
	gain.gain.value = 0.02;
	oscillator.frequency.value = 440;
	oscillator.connect(gain);
	gain.connect(destination);
	const chunks = [];
	const recorder = new MediaRecorder(destination.stream, {mimeType});
	let recordedBytes = 0;
	let resolveEnoughAudio;
	let rejectEnoughAudio;
	const enoughAudioData = new Promise((resolve, reject) => {
		resolveEnoughAudio = resolve;
		rejectEnoughAudio = reject;
	});
	recorder.addEventListener("dataavailable", (event) => {
		if (event.data.size > 0) {
			chunks.push(event.data);
			recordedBytes += event.data.size;
			if (recordedBytes >= 64) resolveEnoughAudio();
		}
	});
	const started = new Promise((resolve, reject) => {
		recorder.addEventListener("start", resolve, {once: true});
		recorder.addEventListener("error", () => reject(new Error("MediaRecorder failed before start")), {once: true});
	});
	const stopped = new Promise((resolve, reject) => {
		recorder.addEventListener("stop", resolve, {once: true});
		recorder.addEventListener("error", () => {
			const error = new Error("MediaRecorder failed");
			rejectEnoughAudio(error);
			reject(error);
		}, {once: true});
	});
	recorder.start(250);
	await started;
	oscillator.start();
	await new Promise((resolve) => window.setTimeout(resolve, 2200));
	if (recordedBytes < 64) {
		recorder.requestData();
		await Promise.race([
			enoughAudioData,
			new Promise((_, reject) => window.setTimeout(() => reject(new Error("MediaRecorder produced less than 64 bytes within 5 seconds")), 5000)),
		]);
	}
	oscillator.stop();
	recorder.stop();
	await stopped;
	await context.close();
	const blob = new Blob(chunks, {type: recorder.mimeType || mimeType});
	const extension = mimeType.includes("ogg") ? "ogg" : "webm";
	const file = new File([blob], "edge-media-recorder." + extension, {type: blob.type});
	const transfer = new DataTransfer();
	transfer.items.add(file);
	const form = document.querySelector("[data-voice-upload]");
	form.elements.audio.files = transfer.files;
	form.elements.audio.dispatchEvent(new Event("change", {bubbles: true}));
	form.elements.duration_seconds.value = "2";
	form.elements.duration_seconds.dispatchEvent(new Event("input", {bubbles: true}));
	return {...capabilities, supported: true, mimeType: file.type, size: file.size, fileCount: form.elements.audio.files.length};
})()`

const emptyMediaRecorderInstallScript = `(() => {
	window.__hackwerkVoiceTestOriginals = {
		MediaRecorder: window.MediaRecorder,
		AudioContext: window.AudioContext,
		getUserMedia: navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices),
	};
	class EmptyMediaRecorder extends EventTarget {
		static isTypeSupported(type) { return type.startsWith("audio/webm"); }
		constructor(stream, options) {
			super();
			this.stream = stream;
			this.mimeType = options.mimeType;
			this.state = "inactive";
		}
		start() { this.state = "recording"; }
		stop() {
			this.state = "inactive";
			queueMicrotask(() => this.dispatchEvent(new Event("stop")));
		}
		pause() { this.state = "paused"; }
		resume() { this.state = "recording"; }
	}
	window.MediaRecorder = EmptyMediaRecorder;
	Object.defineProperty(window, "AudioContext", {configurable: true, value: undefined});
	Object.defineProperty(navigator.mediaDevices, "getUserMedia", {
		configurable: true,
		value: async () => ({getTracks: () => [{stop() {}}]}),
	});
})()`

const emptyMediaRecorderRestoreScript = `(() => {
	const originals = window.__hackwerkVoiceTestOriginals;
	window.MediaRecorder = originals.MediaRecorder;
	Object.defineProperty(window, "AudioContext", {configurable: true, value: originals.AudioContext});
	Object.defineProperty(navigator.mediaDevices, "getUserMedia", {configurable: true, value: originals.getUserMedia});
	delete window.__hackwerkVoiceTestOriginals;
})()`

func minimalWAV() []byte {
	const (
		sampleRate     = uint32(8000)
		bytesPerSample = uint32(2)
		duration       = 3 * time.Second
	)
	audioBytes := uint32(duration/time.Second) * sampleRate * bytesPerSample
	data := make([]byte, 44+audioBytes)
	copy(data[:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], sampleRate)
	binary.LittleEndian.PutUint32(data[28:32], sampleRate*bytesPerSample)
	binary.LittleEndian.PutUint16(data[32:34], uint16(bytesPerSample))
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], audioBytes)
	return data
}
