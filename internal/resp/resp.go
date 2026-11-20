// Package resp implements a deliberately bounded RESP2 command subset.
package resp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	// MaxArrayElements bounds the command arity accepted from the wire.
	MaxArrayElements = 1024
	// MaxBulkLength bounds a single bulk string (1 MiB).
	MaxBulkLength = 1 << 20
	// MaxCommandBytes bounds headers and bodies for one complete command. This
	// prevents MaxArrayElements individually valid bulks allocating ~1 GiB.
	MaxCommandBytes = 2 << 20
	// MaxHeaderLine bounds any RESP header line, including its CRLF.
	MaxHeaderLine = 512
)

// Command is a parsed RESP2 command: an array of bulk strings.
type Command struct {
	Parts [][]byte
}

// Name returns the upper-cased first part (the command verb).
func (c Command) Name() string {
	if len(c.Parts) == 0 {
		return ""
	}
	return string(bytes.ToUpper(c.Parts[0]))
}

// ReadCommand reads exactly one RESP2 command from r. Its allocations and wire
// consumption are bounded by MaxCommandBytes in addition to per-field limits.
func ReadCommand(r *bufio.Reader) (Command, error) {
	header, err := readHeaderLine(r)
	if err != nil {
		return Command{}, err
	}
	budget := MaxCommandBytes - len(header) - 2
	if budget < 0 {
		return Command{}, errors.New("resp: command exceeds byte bound")
	}
	if len(header) == 0 || header[0] != '*' {
		return Command{}, errors.New("resp: expected array header")
	}
	arity, err := parseLength(header[1:])
	if err != nil {
		return Command{}, fmt.Errorf("resp: invalid arity: %w", err)
	}
	if arity > MaxArrayElements {
		return Command{}, fmt.Errorf("resp: arity %d exceeds bound %d", arity, MaxArrayElements)
	}

	parts := make([][]byte, 0, arity)
	for i := 0; i < arity; i++ {
		lh, err := readHeaderLine(r)
		if err != nil {
			return Command{}, err
		}
		budget -= len(lh) + 2
		if len(lh) == 0 || lh[0] != '$' {
			return Command{}, errors.New("resp: expected bulk string header")
		}
		bulkLen, err := parseLength(lh[1:])
		if err != nil {
			return Command{}, fmt.Errorf("resp: invalid bulk length: %w", err)
		}
		if bulkLen > MaxBulkLength {
			return Command{}, fmt.Errorf("resp: bulk length %d exceeds bound %d", bulkLen, MaxBulkLength)
		}
		budget -= bulkLen + 2
		if budget < 0 {
			return Command{}, fmt.Errorf("resp: command exceeds byte bound %d", MaxCommandBytes)
		}
		buf := make([]byte, bulkLen+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return Command{}, fmt.Errorf("resp: reading bulk body: %w", err)
		}
		if buf[bulkLen] != '\r' || buf[bulkLen+1] != '\n' {
			return Command{}, errors.New("resp: bulk string not CRLF-terminated")
		}
		parts = append(parts, buf[:bulkLen])
	}
	return Command{Parts: parts}, nil
}

func parseLength(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, errors.New("empty length")
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, errors.New("length must be a non-negative decimal integer")
		}
	}
	n, err := strconv.ParseUint(string(b), 10, 32)
	if err != nil {
		return 0, errors.New("length overflows supported bound")
	}
	return int(n), nil
}

// readHeaderLine uses a fixed buffer; Reader.ReadString/ReadBytes would let an
// unterminated peer line grow without limit even with a later length check.
func readHeaderLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		return nil, errors.New("resp: header line too long")
	}
	if err != nil {
		return nil, err
	}
	if len(line) > MaxHeaderLine {
		return nil, errors.New("resp: header line too long")
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, errors.New("resp: expected CRLF line ending")
	}
	return line[:len(line)-2], nil
}

// WriteSimpleString writes e.g. "+OK\r\n".
func WriteSimpleString(w io.Writer, s string) error {
	_, err := io.WriteString(w, "+"+s+"\r\n")
	return err
}

// WriteError writes e.g. "-ERR unknown command\r\n".
func WriteError(w io.Writer, s string) error {
	_, err := io.WriteString(w, "-"+s+"\r\n")
	return err
}

// WriteBulk writes a bulk string, or the RESP nil ($-1) when b is nil.
func WriteBulk(w io.Writer, b []byte) error {
	if b == nil {
		_, err := io.WriteString(w, "$-1\r\n")
		return err
	}
	if _, err := fmt.Fprintf(w, "$%d\r\n", len(b)); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\r\n")
	return err
}

// WriteInt writes an integer reply, e.g. ":1\r\n".
func WriteInt(w io.Writer, n int64) error {
	_, err := fmt.Fprintf(w, ":%d\r\n", n)
	return err
}
