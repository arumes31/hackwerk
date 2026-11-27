//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

const chromeStartupLimit = 4096

// Capture only startup; the browser can keep writing until test cleanup.
type chromeStartupOutput struct {
	mu      sync.Mutex
	data    []byte
	stopped bool
}

func (o *chromeStartupOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.stopped {
		o.data = append(o.data, p[:min(len(p), chromeStartupLimit-len(o.data))]...)
	}
	return len(p), nil
}

func (o *chromeStartupOutput) stop() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = true
	return strings.TrimSpace(string(o.data))
}

func startChrome(ctx context.Context, path string, output *chromeStartupOutput) error {
	start := time.Now()
	err := chromedp.Run(ctx)
	elapsed := time.Since(start).Round(time.Millisecond)
	startupLog := output.stop()
	if err == nil {
		return nil
	}
	version := "unavailable"
	// The hosted CI uses Linux. Do not run GUI executables with --version on Windows.
	if runtime.GOOS == "linux" {
		versionCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var versionOut chromeStartupOutput
		command := exec.CommandContext(versionCtx, path, "--version")
		command.Stdout, command.Stderr = &versionOut, &versionOut
		command.WaitDelay = time.Second
		if command.Run() == nil {
			version = versionOut.stop()
		}
	}
	// The allocator may include its own unbounded copy of subprocess output.
	cause := err.Error()
	if strings.HasPrefix(cause, "chrome failed to start:\n") {
		cause = "chrome failed to start"
	}
	cause = cause[:min(len(cause), 512)]
	for _, line := range strings.Split(startupLog, "\n") {
		if strings.HasPrefix(line, "DevTools listening on ") {
			startupLog = strings.ReplaceAll(startupLog, line, "DevTools listening on [local URL omitted]")
		}
	}
	return fmt.Errorf("start Chromium after %s (executable=%q, version=%q, process output capped at %d bytes=%q): %s", elapsed, path, version, chromeStartupLimit, startupLog, cause)
}

func TestChromeStartupOutput(t *testing.T) {
	var output chromeStartupOutput
	payload := strings.Repeat("x", chromeStartupLimit+17)
	if n, err := output.Write([]byte(payload)); err != nil || n != len(payload) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got := output.stop(); got != payload[:chromeStartupLimit] {
		t.Fatalf("captured %d bytes, want %d startup bytes", len(got), chromeStartupLimit)
	}
	var stopped chromeStartupOutput
	stopped.stop()
	if n, err := stopped.Write([]byte("later")); n != 5 || err != nil {
		t.Fatalf("Write after stop = %d, %v", n, err)
	}
	if got := stopped.stop(); got != "" {
		t.Fatalf("captured after stop: %q", got)
	}
}

func TestChromeStartupFailureIncludesExecutable(t *testing.T) {
	var output chromeStartupOutput
	path := t.TempDir() + "/not-a-browser"
	allocator, cancel := chromedp.NewExecAllocator(t.Context(), chromedp.ExecPath(path), chromedp.CombinedOutput(&output))
	defer cancel()
	ctx, stop := chromedp.NewContext(allocator)
	defer stop()
	err := startChrome(ctx, path, &output)
	if err == nil || !strings.Contains(err.Error(), "start Chromium after") || !strings.Contains(err.Error(), "not-a-browser") {
		t.Fatalf("missing startup diagnostic: %v", err)
	}
}
