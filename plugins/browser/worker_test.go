package browser

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkerStripsAmbientCredentials(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is not installed")
	}
	t.Setenv("KEST_TELEGRAM_TOKEN", "test-token")
	t.Setenv("KEST_OPENROUTER_API_KEY", "test-key")
	t.Setenv("KEST_SECRET_BROWSER_VNC_PASSWORD", "test-password")
	t.Setenv("NODE_OPTIONS", "--require forbidden-module")
	dir := t.TempDir()
	module := filepath.Join(dir, "fixture.cjs")
	source := `require('node:fs').writeFileSync('environment.txt', ['KEST_TELEGRAM_TOKEN','KEST_OPENROUTER_API_KEY','KEST_SECRET_BROWSER_VNC_PASSWORD','NODE_OPTIONS'].map(k => process.env[k] || '').join('|')); module.exports = {chromium:{launchPersistentContext:async()=>{throw new Error('private-browser-error-test-token')}}};`
	if err := os.WriteFile(module, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := New().Manifest().ResolveConfig(map[string]any{"module_path": module})
	if err != nil {
		t.Fatal(err)
	}
	w, err := startWorker(cfg, dir, &proxy{url: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.stop)
	_, err = w.call(context.Background(), "list_tabs", arguments{})
	if err == nil {
		t.Fatal("fixture should reject launch")
	}
	if err.Error() != "Browser action could not complete. Check the tab, selector, network policy and runtime setup." {
		t.Fatalf("unsanitized error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "environment.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "|||" {
		t.Fatal("worker inherited credentials or a Node preloader")
	}
}

func TestCanceledExchangeReapsWorker(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is not installed")
	}
	dir := t.TempDir()
	module := filepath.Join(dir, "fixture.cjs")
	if err := os.WriteFile(module, []byte(`module.exports={chromium:{launchPersistentContext:async()=>new Promise(()=>{})}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := New().Manifest().ResolveConfig(map[string]any{"module_path": module})
	if err != nil {
		t.Fatal(err)
	}
	w, err := startWorker(cfg, dir, &proxy{url: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.stop)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = w.call(ctx, "list_tabs", arguments{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	select {
	case <-w.done:
	default:
		t.Fatal("canceled worker still running")
	}
}
