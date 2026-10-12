package plugin

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

func TestDialBlocksUnsafeDestinations(t *testing.T) {
	for _, tt := range []struct {
		name, network, address string
		hosts                  []string
	}{
		{"loopback", "tcp", "127.0.0.1:443", []string{"*"}},
		{"IPv6 loopback", "tcp", "[::1]:443", []string{"*"}},
		{"mapped loopback", "tcp", "[::ffff:127.0.0.1]:443", []string{"*"}},
		{"metadata", "tcp", "169.254.169.254:80", []string{"*"}},
		{"private", "tcp", "10.0.0.1:443", []string{"*"}},
		{"host outside manifest", "tcp", "example.com:443", []string{"example.org"}},
		{"UDP", "udp", "example.com:443", []string{"*"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := DialContext(context.Background(), tt.hosts, tt.network, tt.address)
			if conn != nil {
				_ = conn.Close()
				t.Fatal("opened a forbidden connection")
			}
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPluginDirectoryIsScoped(t *testing.T) {
	d := Deps{DataDir: t.TempDir(), Log: slog.New(slog.DiscardHandler)}
	r := NewRegistry(d)
	h := r.newHost(Manifest{ID: "browser", Capabilities: Capabilities{Files: true}}, nil)
	dir, err := h.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir == d.DataDir {
		t.Fatal("plugin received the application root")
	}
	if _, err := r.newHost(Manifest{ID: "tasks"}, nil).DataDir(); err == nil {
		t.Fatal("undeclared files access allowed")
	}
}
