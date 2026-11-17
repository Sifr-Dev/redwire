// Package resp implements the RESP2 wire protocol for a deliberately bounded
// command subset. See README.md for exact semantics and limits.
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
	// maxHeaderLine bounds any RESP header line (e.g. "$1234567\r\n").
	maxHeaderLine = 512
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

// ReadCommand reads exactly one RESP2 command from r. It rejects arrays that
// exceed MaxArrayElements, bulk strings that exceed MaxBulkLength, malformed
// headers, and non-CRLF line endings.
func ReadCommand(r *bufio.Reader) (Command, error) {
	header, err := readHeaderLine(r)
	if err != nil {
		return Command{}, err
	}
	if len(header) == 0 || header[0] != '*' {
		return Command{}, errors.New("resp: expected array header")
	}
	arity, err := strconv.Atoi(string(header[1:]))
	if err != nil {
		return Command{}, fmt.Errorf("resp: invalid arity %q", header[1:])
	}
	if arity < 0 || arity > MaxArrayElements {
		return Command{}, fmt.Errorf("resp: arity %d exceeds bound %d", arity, MaxArrayElements)
	}

	parts := make([][]byte, 0, arity)
	for i := 0; i < arity; i++ {
		lh, err := readHeaderLine(r)
		if err != nil {
			return Command{}, err
		}
		if len(lh) == 0 || lh[0] != '$' {
			return Command{}, errors.New("resp: expected bulk string header")
		}
		bulkLen, err := strconv.Atoi(string(lh[1:]))
		if err != nil {
			return Command{}, fmt.Errorf("resp: invalid bulk length %q", lh[1:])
		}
		if bulkLen < 0 || bulkLen > MaxBulkLength {
			return Command{}, fmt.Errorf("resp: bulk length %d exceeds bound %d", bulkLen, MaxBulkLength)
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

// readHeaderLine reads one CRLF-terminated header line bounded by maxHeaderLine.
func readHeaderLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		return nil, errors.New("resp: header line too long")
	}
	if err != nil {
		return nil, err
	}
	if len(line) > maxHeaderLine {
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
	_, err := io.WriteString(w, fmt.Sprintf(":%d\r\n", n))
	return err
}
