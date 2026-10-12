package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
)

// actionError is a failure the worker reported for one action, such as a ref
// that no longer exists. The browser itself is fine, so it keeps running.
type actionError struct {
	msg string
	// fatal means the worker cannot continue and must be replaced.
	fatal bool
}

func (e *actionError) Error() string { return e.msg }

type worker struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Scanner
	done chan struct{}
}

func startWorker(cfg plugin.Config, dir string, px *proxy) (*worker, error) {
	cmd := exec.Command(cfg.String("node_path"), "-e", workerSrc) //nolint:gosec,noctx // fixed embedded code; lifecycle outlives a tool context and is owned by Close
	cmd.Dir = dir
	// Browser children receive runtime settings only, never the bot's credentials.
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "LOCALAPPDATA", "TEMP", "TMP", "TMPDIR", "DISPLAY", "XAUTHORITY", "PLAYWRIGHT_BROWSERS_PATH"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+dir, "USERPROFILE="+dir, "XDG_CACHE_HOME="+filepath.Join(dir, "cache"), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
	cmd.Stderr = io.Discard // Playwright errors can include typed values, URLs and page content.
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open browser input: %w", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, fmt.Errorf("open browser output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, fmt.Errorf("start browser worker: %w", err)
	}
	w := &worker{cmd: cmd, in: in, out: bufio.NewScanner(out), done: make(chan struct{})}
	w.out.Buffer(make([]byte, 4096), 1<<20)
	go func() {
		defer close(w.done) // The worker owns this completion channel.
		defer func() { _ = recover() }()
		_ = cmd.Wait()
	}()
	config := map[string]any{
		"module": cfg.String("module_path"), "dir": dir, "proxy": px.url,
		"username": px.username, "password": px.password,
		"headless": cfg.Bool("headless"), "sandbox": cfg.Bool("sandbox"),
		"timeout": cfg.Int("timeout_seconds") * 1000, "maxChars": cfg.Int("max_chars"),
	}
	if err := json.NewEncoder(in).Encode(config); err != nil {
		w.stop()
		return nil, errors.New("configure browser worker")
	}
	return w, nil
}

func (w *worker) stop() {
	_ = w.in.Close() // EOF requests a graceful profile flush and Chromium shutdown.
	select {
	case <-w.done:
	case <-time.After(3 * time.Second):
		_ = w.cmd.Process.Kill()
		<-w.done
	}
}

func (w *worker) call(ctx context.Context, action string, args workerArgs) (string, error) {
	type response struct {
		Content string `json:"content"`
		Error   string `json:"error"`
		Fatal   bool   `json:"fatal"`
	}
	type outcome struct {
		content string
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		result := outcome{}
		defer func() {
			if recover() != nil {
				result.err = errors.New("browser protocol interrupted")
			}
			done <- result // This exchange owns done; the buffered send cannot block on cancellation.
		}()
		if err := json.NewEncoder(w.in).Encode(map[string]any{"action": action, "args": args}); err != nil {
			result.err = errors.New("write browser command")
			return
		}
		if !w.out.Scan() {
			result.err = errors.New("browser worker stopped; check Playwright installation and Chromium sandbox support")
			return
		}
		var res response
		if err := json.Unmarshal(w.out.Bytes(), &res); err != nil {
			result.err = errors.New("invalid browser response")
			return
		}
		if res.Error != "" {
			result.err = &actionError{msg: res.Error, fatal: res.Fatal}
			return
		}
		result.content = res.Content
	}()
	select {
	case result := <-done:
		return result.content, result.err
	case <-ctx.Done():
		w.stop()
		<-done
		return "", fmt.Errorf("browser action: %w", ctx.Err())
	}
}
