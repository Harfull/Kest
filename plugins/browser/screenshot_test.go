package browser

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestScreenshotReadConfinesArtifacts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "screenshots")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(dir, "image.png")
	if err := os.WriteFile(valid, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "private.png")
	if err := os.WriteFile(outside, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(dir, "invalid.png")
	if err := os.WriteFile(invalid, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{dir: root}
	for _, tc := range []struct {
		name, path string
		ok         bool
	}{
		{"owned screenshot", valid, true}, {"outside", outside, false}, {"invalid PNG", invalid, false}, {"missing", filepath.Join(dir, "missing.png"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.readScreenshot(tc.path)
			if (err == nil) != tc.ok {
				t.Fatalf("error = %v", err)
			}
			if tc.ok {
				if _, statErr := os.Stat(tc.path); statErr == nil {
					t.Fatal("screenshot file was kept after reading")
				}
			}
		})
	}
	link := filepath.Join(dir, "linked.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symlink check unavailable: %v", err)
		return
	}
	if _, err := p.readScreenshot(link); err == nil {
		t.Fatal("symlink escaped screenshot directory")
	}
}
