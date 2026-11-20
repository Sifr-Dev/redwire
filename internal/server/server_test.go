package server

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Sifr-Dev/redwire/internal/store"
)

func dial(t *testing.T, ln net.Listener) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func sendRaw(t *testing.T, conn net.Conn, wire string) string {
	t.Helper()
	if _, err := fmt.Fprint(conn, wire); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

func sendBulk(t *testing.T, conn net.Conn, wire, bulk string) (string, string) {
	t.Helper()
	if _, err := fmt.Fprint(conn, wire); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	header, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	header = strings.TrimSuffix(strings.TrimSuffix(header, "\n"), "\r")
	if strings.HasPrefix(header, "$") && header != "$-1" {
		body := make([]byte, 0)
		for len(body) < len(bulk)+2 {
			chunk := make([]byte, 1)
			n, err := r.Read(chunk)
			if err != nil {
				t.Fatalf("read bulk: %v", err)
			}
			body = append(body, chunk[:n]...)
		}
		return header, strings.TrimSuffix(strings.TrimSuffix(string(body), "\n"), "\r")
	}
	return header, ""
}

func startTestServer(t *testing.T, st *store.Store, config Config) (net.Listener, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewWithConfig(st, config)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		<-done
	})
	return ln, done
}

func commandWire(parts ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(parts))
	for _, part := range parts {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(part), part)
	}
	return b.String()
}

func TestServerTTLAndPersistence(t *testing.T) {
	path := t.TempDir() + "/state.json"
	st, err := store.Open(path, store.Limits{MaxKeys: 10, MaxMemoryBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	ln, _ := startTestServer(t, st, Config{})
	conn := dial(t, ln)
	defer conn.Close()
	cases := []struct {
		command string
		want    string
	}{
		{commandWire("SET", "k", "v"), "+OK"},
		{commandWire("PEXPIRE", "k", "50000"), ":1"},
		{commandWire("TTL", "k"), ":50"},
		{commandWire("PEXPIRE", "missing", "1"), ":0"},
		{commandWire("TTL", "missing"), ":-2"},
		{commandWire("PEXPIRE", "k", "nope"), "-ERR value is not an integer or out of range"},
		{commandWire("TTL", "k", "extra"), "-ERR wrong number of arguments for 'ttl' command"},
	}
	for _, tc := range cases {
		if got := sendRaw(t, conn, tc.command); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.command, got, tc.want)
		}
	}
	if got := sendRaw(t, conn, commandWire("SET", "persist", "yes")); got != "+OK" {
		t.Fatal(got)
	}
	if got := sendRaw(t, conn, commandWire("PEXPIRE", "persist", "0")); got != ":1" {
		t.Fatal(got)
	}
	if got := sendRaw(t, conn, commandWire("PTTL", "persist")); got != ":-2" {
		t.Fatal(got)
	}
}

func TestConnectionLimitAndProtocolBound(t *testing.T) {
	ln, _ := startTestServer(t, store.NewStore(), Config{MaxConnections: 1})
	first := dial(t, ln)
	defer first.Close()
	if got := sendRaw(t, first, commandWire("PING")); got != "+PONG" {
		t.Fatal(got)
	}
	second := dial(t, ln)
	defer second.Close()
	if got := sendRaw(t, second, commandWire("PING")); !strings.Contains(got, "connection limit exceeded") {
		t.Fatalf("second connection = %q", got)
	}
	if got := sendRaw(t, first, "*1025\r\n"); got != "-ERR Protocol error" {
		t.Fatalf("oversized command = %q", got)
	}
}

func TestGracefulShutdownClosesIdleClient(t *testing.T) {
	st := store.NewStore()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewWithConfig(st, Config{ReadTimeout: time.Hour})
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	conn := dial(t, ln)
	defer conn.Close()
	if got := sendRaw(t, conn, commandWire("PING")); got != "+PONG" {
		t.Fatal(got)
	}
	closed := make(chan error, 1)
	go func() { closed <- srv.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind idle connection")
	}
	if err := <-done; !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve returned %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("idle client remained open after shutdown")
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond); err == nil {
		t.Fatal("listener accepted after shutdown")
	}
}

func TestServerRoundTrip(t *testing.T) {
	srv := New(store.NewStore())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()

	conn := dial(t, ln)
	defer conn.Close()

	if got := sendRaw(t, conn, "*1\r\n$4\r\nPING\r\n"); got != "+PONG" {
		t.Fatalf("PING -> %q, want +PONG", got)
	}

	if got := sendRaw(t, conn, "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"); got != "+OK" {
		t.Fatalf("SET -> %q, want +OK", got)
	}

	header, body := sendBulk(t, conn, "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n", "value")
	if header != "$5" || body != "value" {
		t.Fatalf("GET -> (%q, %q), want ($5, value)", header, body)
	}

	header, _ = sendBulk(t, conn, "*2\r\n$3\r\nGET\r\n$7\r\nmissing\r\n", "")
	if header != "$-1" {
		t.Fatalf("GET missing -> %q, want $-1", header)
	}

	if got := sendRaw(t, conn, "*2\r\n$3\r\nDEL\r\n$3\r\nkey\r\n"); got != ":1" {
		t.Fatalf("DEL -> %q, want :1", got)
	}
	if got := sendRaw(t, conn, "*2\r\n$3\r\nDEL\r\n$3\r\nkey\r\n"); got != ":0" {
		t.Fatalf("DEL again -> %q, want :0", got)
	}
	if got := sendRaw(t, conn, "*2\r\n$6\r\nEXISTS\r\n$3\r\nkey\r\n"); got != ":0" {
		t.Fatalf("EXISTS -> %q, want :0", got)
	}
	if got := sendRaw(t, conn, "*3\r\n$3\r\nSET\r\n$1\r\nx\r\n$1\r\ny\r\n"); got != "+OK" {
		t.Fatalf("SET x -> %q", got)
	}
	if got := sendRaw(t, conn, "*2\r\n$6\r\nEXISTS\r\n$1\r\nx\r\n"); got != ":1" {
		t.Fatalf("EXISTS x -> %q, want :1", got)
	}
	if got := sendRaw(t, conn, "*2\r\n$5\r\nBOGUS\r\n$3\r\nfoo\r\n"); !strings.HasPrefix(got, "-ERR") {
		t.Fatalf("unknown command -> %q, want -ERR...", got)
	}
	if got := sendRaw(t, conn, "*1\r\n$3\r\nGET\r\n"); !strings.HasPrefix(got, "-ERR") {
		t.Fatalf("arity error -> %q, want -ERR...", got)
	}
}
