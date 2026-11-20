// Package server serves the bounded RESP2 subset over TCP.
package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sifr-Dev/redwire/internal/resp"
	"github.com/Sifr-Dev/redwire/internal/store"
)

const (
	DefaultMaxConnections = 128
	readDeadline          = 30 * time.Second
	writeDeadline         = 10 * time.Second
)

var ErrServerClosed = errors.New("server: closed")

// Config controls hard connection limits. Zero values select defaults.
type Config struct {
	MaxConnections int
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	ErrorLog       *log.Logger
}

// Server accepts RESP2 connections and executes the bounded command set.
type Server struct {
	st *store.Store

	mu          sync.Mutex
	listeners   map[net.Listener]struct{}
	connections map[net.Conn]struct{}
	closed      bool
	wg          sync.WaitGroup

	maxConnections int
	readTimeout    time.Duration
	writeTimeout   time.Duration
	errorLog       *log.Logger
}

// New creates a server with default bounds backed by st.
func New(st *store.Store) *Server {
	return NewWithConfig(st, Config{})
}

// NewWithConfig creates a bounded server. Invalid negative settings panic as
// programmer errors; user-facing command-line validation happens in main.
func NewWithConfig(st *store.Store, config Config) *Server {
	if st == nil || config.MaxConnections < 0 || config.ReadTimeout < 0 || config.WriteTimeout < 0 {
		panic("server: nil store or negative timeout/limit")
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = DefaultMaxConnections
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = readDeadline
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = writeDeadline
	}
	if config.ErrorLog == nil {
		config.ErrorLog = log.New(os.Stderr, "redwire: ", log.LstdFlags)
	}
	return &Server{
		st:             st,
		listeners:      make(map[net.Listener]struct{}),
		connections:    make(map[net.Conn]struct{}),
		maxConnections: config.MaxConnections,
		readTimeout:    config.ReadTimeout,
		writeTimeout:   config.WriteTimeout,
		errorLog:       config.ErrorLog,
	}
}

// ListenAndServe listens on addr and serves until Close or an accept error.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return s.Serve(ln)
}

// Serve accepts connections until Close, which closes listeners and clients
// and waits for handlers. Close may safely be called from another goroutine.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return ErrServerClosed
	}
	s.listeners[ln] = struct{}{}
	s.mu.Unlock()
	defer func() {
		_ = ln.Close()
		s.mu.Lock()
		delete(s.listeners, ln)
		s.mu.Unlock()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return ErrServerClosed
			}
			return fmt.Errorf("accept: %w", err)
		}
		admitted := s.admit(conn)
		if !admitted {
			_ = conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
			_ = resp.WriteError(conn, "ERR connection limit exceeded")
			_ = conn.Close()
			continue
		}
		go s.handleConn(conn)
	}
}

// Close stops listeners and active connections and waits for all handlers.
// It is idempotent. A new Serve after Close returns ErrServerClosed.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return nil
	}
	s.closed = true
	listeners := make([]net.Listener, 0, len(s.listeners))
	for ln := range s.listeners {
		listeners = append(listeners, ln)
	}
	connections := make([]net.Conn, 0, len(s.connections))
	for conn := range s.connections {
		connections = append(connections, conn)
	}
	s.mu.Unlock()
	for _, ln := range listeners {
		_ = ln.Close()
	}
	for _, conn := range connections {
		_ = conn.Close()
	}
	s.wg.Wait()
	return nil
}

// admit registers a handler under the same lock as Add. Close therefore cannot
// pass Wait between admission and Add, which would otherwise launch work late.
func (s *Server) admit(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.connections) >= s.maxConnections {
		return false
	}
	s.connections[conn] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.connections, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	r := bufio.NewReaderSize(conn, resp.MaxHeaderLine)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
			return
		}
		cmd, err := resp.ReadCommand(r)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
					// Preserve existing semantics: one generic protocol error, then close.
					_ = s.writeReply(conn, func(w io.Writer) error {
						return resp.WriteError(w, "ERR Protocol error")
					})
				}
			}
			return
		}
		if quit := s.execute(conn, cmd); quit {
			return
		}
	}
}

func (s *Server) writeReply(conn net.Conn, write func(io.Writer) error) error {
	if err := conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
		return err
	}
	// Values cannot exceed 1 MiB (parser bound), so this buffer is bounded too.
	w := bufio.NewWriterSize(conn, resp.MaxBulkLength+1024)
	if err := write(w); err != nil {
		return err
	}
	return w.Flush()
}

// execute writes one response and reports whether the connection should close.
func (s *Server) execute(conn net.Conn, cmd resp.Command) bool {
	if len(cmd.Parts) == 0 {
		return s.replyError(conn, "ERR empty command")
	}
	name := cmd.Name()
	args := cmd.Parts[1:]
	switch name {
	case "PING":
		switch len(args) {
		case 0:
			return s.reply(conn, func(w io.Writer) error { return resp.WriteSimpleString(w, "PONG") })
		case 1:
			return s.reply(conn, func(w io.Writer) error { return resp.WriteBulk(w, args[0]) })
		default:
			return s.replyError(conn, arityError("ping"))
		}
	case "SET":
		if len(args) != 2 {
			return s.replyError(conn, arityError("set"))
		}
		if err := s.st.Set(string(args[0]), string(args[1])); err != nil {
			return s.replyError(conn, storeError(err))
		}
		return s.reply(conn, func(w io.Writer) error { return resp.WriteSimpleString(w, "OK") })
	case "GET":
		if len(args) != 1 {
			return s.replyError(conn, arityError("get"))
		}
		value, ok := s.st.Get(string(args[0]))
		if !ok {
			return s.reply(conn, func(w io.Writer) error { return resp.WriteBulk(w, nil) })
		}
		return s.reply(conn, func(w io.Writer) error { return resp.WriteBulk(w, []byte(value)) })
	case "DEL":
		if len(args) != 1 {
			return s.replyError(conn, arityError("del"))
		}
		removed, err := s.st.Delete(string(args[0]))
		if err != nil {
			return s.replyError(conn, storeError(err))
		}
		return s.replyInt(conn, boolInt(removed))
	case "EXISTS":
		if len(args) != 1 {
			return s.replyError(conn, arityError("exists"))
		}
		return s.replyInt(conn, boolInt(s.st.Exists(string(args[0]))))
	case "PEXPIRE":
		if len(args) != 2 {
			return s.replyError(conn, arityError("pexpire"))
		}
		milliseconds, err := strconv.ParseInt(string(args[1]), 10, 64)
		if err != nil {
			return s.replyError(conn, "ERR value is not an integer or out of range")
		}
		// Guard the time.Duration multiplication below.
		if milliseconds > int64((1<<63-1)/time.Millisecond) || milliseconds < -int64((1<<63-1)/time.Millisecond) {
			return s.replyError(conn, "ERR invalid expire time in 'pexpire' command")
		}
		updated, err := s.st.Expire(string(args[0]), time.Duration(milliseconds)*time.Millisecond)
		if err != nil {
			return s.replyError(conn, storeError(err))
		}
		return s.replyInt(conn, boolInt(updated))
	case "TTL", "PTTL":
		if len(args) != 1 {
			return s.replyError(conn, arityError(strings.ToLower(name)))
		}
		remaining, ok := s.st.TTL(string(args[0]))
		if !ok {
			return s.replyInt(conn, -2)
		}
		if remaining < 0 {
			return s.replyInt(conn, -1)
		}
		if name == "TTL" {
			return s.replyInt(conn, int64((remaining+time.Second-1)/time.Second))
		}
		return s.replyInt(conn, remaining.Milliseconds())
	case "QUIT":
		if len(args) != 0 {
			return s.replyError(conn, arityError("quit"))
		}
		if err := s.writeReply(conn, func(w io.Writer) error { return resp.WriteSimpleString(w, "OK") }); err != nil {
			return true
		}
		return true
	default:
		return s.replyError(conn, fmt.Sprintf("ERR unknown command '%s'", string(cmd.Parts[0])))
	}
}

func (s *Server) reply(conn net.Conn, write func(io.Writer) error) bool {
	if err := s.writeReply(conn, write); err != nil {
		if !errors.Is(err, net.ErrClosed) {
			s.errorLog.Printf("write response: %v", err)
		}
		return true
	}
	return false
}

func (s *Server) replyError(conn net.Conn, message string) bool {
	return s.reply(conn, func(w io.Writer) error { return resp.WriteError(w, message) })
}

func (s *Server) replyInt(conn net.Conn, value int64) bool {
	return s.reply(conn, func(w io.Writer) error { return resp.WriteInt(w, value) })
}

func arityError(command string) string {
	return fmt.Sprintf("ERR wrong number of arguments for '%s' command", command)
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func storeError(err error) string {
	switch {
	case errors.Is(err, store.ErrKeyLimit):
		return "ERR key limit exceeded"
	case errors.Is(err, store.ErrMemoryLimit):
		return "ERR memory limit exceeded"
	default:
		return "ERR persistence failure: mutation rejected"
	}
}
