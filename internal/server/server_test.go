package server

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mohamedsalahi/redis_clone/internal/store"
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
