// Package server serves the bounded RESP2 subset to TCP clients.
// One goroutine per connection, a 30s read deadline, and the same Store
// instance for every connection.
package server

import (
	"bufio"
	"fmt"
	"net"
	"time"

	"github.com/Sifr-Dev/redwire/internal/resp"
	"github.com/Sifr-Dev/redwire/internal/store"
)

const readDeadline = 30 * time.Second

// Server accepts RESP2 connections and executes the bounded command set.
type Server struct {
	st *store.Store
}

// New creates a server backed by st.
func New(st *store.Store) *Server {
	return &Server{st: st}
}

// ListenAndServe listens on addr and serves until ln is closed or an
// unrecoverable accept error occurs.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return s.Serve(ln)
}

// Serve accepts connections on ln until it is closed.
func (s *Server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
	r := bufio.NewReader(conn)

	for {
		cmd, err := resp.ReadCommand(r)
		if err != nil {
			// Protocol errors are answered once, then the connection closes.
			_ = resp.WriteError(conn, "ERR Protocol error")
			return
		}
		if quit := s.execute(conn, cmd); quit {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
	}
}

// execute writes the response for one command and reports whether the
// connection should close (after QUIT).
func (s *Server) execute(conn net.Conn, cmd resp.Command) bool {
	if len(cmd.Parts) == 0 {
		_ = resp.WriteError(conn, "ERR empty command")
		return false
	}
	name := cmd.Name()
	args := cmd.Parts[1:]

	switch name {
	case "PING":
		switch len(args) {
		case 0:
			_ = resp.WriteSimpleString(conn, "PONG")
		case 1:
			_ = resp.WriteBulk(conn, args[0])
		default:
			_ = resp.WriteError(conn, "ERR wrong number of arguments for 'ping' command")
		}
	case "SET":
		if len(args) != 2 {
			_ = resp.WriteError(conn, "ERR wrong number of arguments for 'set' command")
			return false
		}
		s.st.Set(string(args[0]), string(args[1]))
		_ = resp.WriteSimpleString(conn, "OK")
	case "GET":
		if len(args) != 1 {
			_ = resp.WriteError(conn, "ERR wrong number of arguments for 'get' command")
			return false
		}
		val, ok := s.st.Get(string(args[0]))
		if !ok {
			_ = resp.WriteBulk(conn, nil)
			return false
		}
		_ = resp.WriteBulk(conn, []byte(val))
	case "DEL":
		if len(args) != 1 {
			_ = resp.WriteError(conn, "ERR wrong number of arguments for 'del' command")
			return false
		}
		if s.st.Delete(string(args[0])) {
			_ = resp.WriteInt(conn, 1)
		} else {
			_ = resp.WriteInt(conn, 0)
		}
	case "EXISTS":
		if len(args) != 1 {
			_ = resp.WriteError(conn, "ERR wrong number of arguments for 'exists' command")
			return false
		}
		if s.st.Exists(string(args[0])) {
			_ = resp.WriteInt(conn, 1)
		} else {
			_ = resp.WriteInt(conn, 0)
		}
	case "QUIT":
		_ = resp.WriteSimpleString(conn, "OK")
		return true
	default:
		_ = resp.WriteError(conn, fmt.Sprintf("ERR unknown command '%s'", string(cmd.Parts[0])))
	}
	return false
}

