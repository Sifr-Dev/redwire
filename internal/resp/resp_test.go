package resp

import (
	"bufio"
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
		"x3\r\n$3\r\nSET\r\n",         // not an array header
		"*x\r\n",                      // invalid arity
		"*1025\r\n",                   // arity exceeds bound
		"*1\r\n$10000000\r\n",         // bulk exceeds bound
		"*1\r\n$3\r\nAB",              // short bulk body
		"*1\r\n$3\r\nABC\n",           // not CRLF-terminated
		"*1\r\nhello\r\n",             // bulk header not $-prefixed
		"*1\r\n$4\r\nABCD\r",          // missing LF after CR
		"*2\r\n$3\r\nGET\r\n$-1\r\n",  // negative bulk length
	}
	for _, wire := range cases {
		if _, err := ReadCommand(bufio.NewReader(strings.NewReader(wire))); err == nil {
			t.Fatalf("expected error for %q", wire)
		}
	}
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
