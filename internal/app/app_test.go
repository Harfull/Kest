package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/config"
)

// syncBuffer is a bytes.Buffer safe for use by a logger and a reader at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.Telegram.OwnerIDs = []int64{1}
	return cfg
}

func TestRunLogsReadyAndStopsOnCancel(t *testing.T) {
	var out syncBuffer
	log := slog.New(slog.NewTextHandler(&out, nil))

	a, err := New(testConfig(t), log)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), ReadyMessage) {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("ready message not logged; output:\n%s", out.String())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(ShutdownTimeout):
		t.Fatal("Run() did not return after cancel")
	}
}

func TestNewCreatesDataDir(t *testing.T) {
	cfg := testConfig(t)

	if _, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("New() error = %v", err)
	}

	info, err := os.Stat(cfg.DataDir)
	if err != nil {
		t.Fatalf("data dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", cfg.DataDir)
	}
}
