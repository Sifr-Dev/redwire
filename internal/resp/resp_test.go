package resp

import (
	"bufio"
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestReadCommandRoundTrip(t *testing.T) {
	wire := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"
	cmd, err := ReadCommand(bufio.NewReader(strings.NewReader(wire)))
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	if cmd.Name() != "SET" {
		t.Fatalf("Name() = %q, want SET", cmd.Name())
	}
	if len(cmd.Parts) != 3 {
		t.Fatalf("len(Parts) = %d, want 3", len(cmd.Parts))
	}
	if string(cmd.Parts[2]) != "value" {
		t.Fatalf("Parts[2] = %q, want value", cmd.Parts[2])
	}
}

func TestReadCommandRejectsGarbage(t *testing.T) {
	cases := []string{
		"x3\r\n$3\r\nSET\r\n",        // not an array header
		"*x\r\n",                     // invalid arity
		"*1025\r\n",                  // arity exceeds bound
		"*1\r\n$10000000\r\n",        // bulk exceeds bound
		"*1\r\n$3\r\nAB",             // short bulk body
		"*1\r\n$3\r\nABC\n",          // not CRLF-terminated
		"*1\r\nhello\r\n",            // bulk header not $-prefixed
		"*1\r\n$4\r\nABCD\r",         // missing LF after CR
		"*2\r\n$3\r\nGET\r\n$-1\r\n", // negative bulk length
	}
	for _, wire := range cases {
		if _, err := ReadCommand(bufio.NewReader(strings.NewReader(wire))); err == nil {
			t.Fatalf("expected error for %q", wire)
		}
	}
}

func TestCommandByteBoundAndStrictLengths(t *testing.T) {
	var wire bytes.Buffer
	fmt.Fprintln(&wire, "*1024")
	// Replace the LF-only test construction with the actual protocol header.
	wire.Reset()
	wire.WriteString("*1024\r\n")
	for i := 0; i < 1024; i++ {
		wire.WriteString("$4096\r\n")
		wire.Write(bytes.Repeat([]byte{'x'}, 4096))
		wire.WriteString("\r\n")
	}
	if _, err := ReadCommand(bufio.NewReader(&wire)); err == nil || !strings.Contains(err.Error(), "byte bound") {
		t.Fatalf("aggregate command error = %v, want byte bound", err)
	}
	for _, header := range []string{"$+1\r\n", "$-1\r\n", "$999999999999999999999\r\n"} {
		if _, err := ReadCommand(bufio.NewReader(strings.NewReader("*1\r\n" + header))); err == nil {
			t.Fatalf("accepted malformed length %q", header)
		}
	}
}

func TestReadCommandPropertyRandomFragmentation(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		parts := make([][]byte, rng.Intn(12))
		var wire bytes.Buffer
		fmt.Fprintf(&wire, "*%d\r\n", len(parts))
		for i := range parts {
			parts[i] = make([]byte, rng.Intn(128))
			_, _ = rng.Read(parts[i])
			fmt.Fprintf(&wire, "$%d\r\n", len(parts[i]))
			wire.Write(parts[i])
			wire.WriteString("\r\n")
		}
		fragmented := &oneByteReader{r: &wire}
		cmd, err := ReadCommand(bufio.NewReaderSize(fragmented, 64))
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if len(cmd.Parts) != len(parts) {
			t.Fatalf("trial %d: part count = %d, want %d", trial, len(cmd.Parts), len(parts))
		}
		for i := range parts {
			if !bytes.Equal(cmd.Parts[i], parts[i]) {
				t.Fatalf("trial %d part %d changed", trial, i)
			}
		}
	}
}

type oneByteReader struct{ r *bytes.Buffer }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.r.Read(p)
}

func FuzzReadCommand(f *testing.F) {
	for _, seed := range []string{
		"*1\r\n$4\r\nPING\r\n",
		"*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n",
		"*0\r\n",
		"*1\r\n$3\r\nAB",
		"garbage\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, wire string) {
		// The safety property is no panic/unbounded loop; malformed input may
		// return any error. Valid random cases are checked by the property test.
		cmd, err := ReadCommand(bufio.NewReaderSize(strings.NewReader(wire), 64))
		if err == nil && len(cmd.Parts) > MaxArrayElements {
			t.Fatalf("accepted %d parts", len(cmd.Parts))
		}
		for _, part := range cmd.Parts {
			if len(part) > MaxBulkLength {
				t.Fatalf("accepted bulk length %d", len(part))
			}
		}
	})
}

func TestWriteRoundTrips(t *testing.T) {
	var b strings.Builder
	if err := WriteSimpleString(&b, "OK"); err != nil {
		t.Fatal(err)
	}
	if b.String() != "+OK\r\n" {
		t.Fatalf("simple string = %q", b.String())
	}

	b.Reset()
	if err := WriteBulk(&b, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if b.String() != "$5\r\nhello\r\n" {
		t.Fatalf("bulk = %q", b.String())
	}

	b.Reset()
	if err := WriteBulk(&b, nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != "$-1\r\n" {
		t.Fatalf("nil bulk = %q", b.String())
	}

	b.Reset()
	if err := WriteInt(&b, 42); err != nil {
		t.Fatal(err)
	}
	if b.String() != ":42\r\n" {
		t.Fatalf("int = %q", b.String())
	}

	b.Reset()
	if err := WriteError(&b, "ERR nope"); err != nil {
		t.Fatal(err)
	}
	if b.String() != "-ERR nope\r\n" {
		t.Fatalf("error = %q", b.String())
	}
}
