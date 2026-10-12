package browser

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
)

type proxy struct {
	host                    plugin.Host
	url, username, password string
	server                  *http.Server
	wg                      sync.WaitGroup
	mu                      sync.Mutex
	tunnels                 map[net.Conn]bool
	closing                 bool
}

func startProxy(h plugin.Host) (*proxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // listener lifetime is owned by the plugin
	if err != nil {
		return nil, err
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		_ = listener.Close()
		return nil, err
	}
	p := &proxy{host: h, url: "http://" + listener.Addr().String(), username: "kest", password: hex.EncodeToString(token[:]), tunnels: make(map[net.Conn]bool)}
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { _ = recover() }()
		_ = p.server.Serve(listener)
	}()
	return p, nil
}

func (p *proxy) close() {
	p.mu.Lock()
	p.closing = true
	for conn := range p.tunnels {
		_ = conn.Close()
	}
	p.mu.Unlock()
	_ = p.server.Close()
	p.wg.Wait()
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(p.username+":"+p.password))
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(expected)) != 1 {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Kest browser"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	if r.URL.Scheme != "http" || r.URL.User != nil || r.URL.Host == "" || !allowedPort(r.URL.Port()) {
		http.Error(w, "destination blocked", http.StatusForbidden)
		return
	}
	req := r.Clone(r.Context())
	req.RequestURI = ""
	stripHopHeaders(req.Header)
	// The browser follows redirects itself; each request passes the host guard.
	client := *p.host.HTTP()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		http.Error(w, "destination blocked or unavailable", http.StatusBadGateway)
		return
	}
	defer res.Body.Close() //nolint:errcheck // response reader
	stripHopHeaders(res.Header)
	for key, values := range res.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

func allowedPort(port string) bool { return port == "" || port == "80" || port == "443" }

func stripHopHeaders(headers http.Header) {
	for _, value := range headers.Values("Connection") {
		for _, key := range strings.Split(value, ",") {
			headers.Del(strings.TrimSpace(key))
		}
	}
	for _, key := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "Transfer-Encoding", "TE", "Trailer", "Upgrade"} {
		headers.Del(key)
	}
}

func (p *proxy) connect(w http.ResponseWriter, r *http.Request) {
	_, port, err := net.SplitHostPort(r.Host)
	if err != nil || (port != "443" && port != "80") {
		http.Error(w, "destination blocked", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	upstream, err := p.host.DialContext(ctx, "tcp", r.Host)
	if err != nil {
		http.Error(w, "destination blocked or unavailable", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "tunnel unavailable", http.StatusBadGateway)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		_ = upstream.Close()
		_ = client.Close()
		return
	}
	p.tunnels[client] = true
	p.tunnels[upstream] = true
	p.wg.Add(2)
	p.mu.Unlock()
	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = client.Close()
	}
	if err := buffered.Flush(); err != nil {
		_ = client.Close()
	}
	copyStream := func(dst, src net.Conn, reader io.Reader) {
		defer p.wg.Done()
		defer func() {
			_ = recover()
			_ = dst.Close()
			_ = src.Close()
			p.mu.Lock()
			delete(p.tunnels, dst)
			delete(p.tunnels, src)
			p.mu.Unlock()
		}()
		_, _ = io.Copy(dst, reader)
	}
	go copyStream(upstream, client, buffered)
	go copyStream(client, upstream, upstream)
}
