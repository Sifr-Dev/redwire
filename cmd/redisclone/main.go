// redisclone is the single binary for the Cipher Redis project:
//   redisclone server [-addr 127.0.0.1:6379]   serve RESP2 on TCP
//   redisclone cli [-addr addr] [COMMAND arg...]  one-shot or REPL client
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/Sifr-dev/redis_clone/internal/server"
	"github.com/Sifr-dev/redis_clone/internal/store"
)

const defaultAddr = "127.0.0.1:6379"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "server":
		runServer(os.Args[2:])
	case "cli":
		runCli(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  redisclone server [-addr 127.0.0.1:6379]
  redisclone cli [-addr 127.0.0.1:6379] [COMMAND arg...]
`)
}

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	addr := fs.String("addr", defaultAddr, "listen address")
	_ = fs.Parse(args)
	srv := server.New(store.NewStore())
	fmt.Printf("redisclone: listening on %s\n", *addr)
	if err := srv.ListenAndServe(*addr); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

func runCli(args []string) {
	fs := flag.NewFlagSet("cli", flag.ExitOnError)
	addr := fs.String("addr", defaultAddr, "server address")
	_ = fs.Parse(args)

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect %s: %v\n", *addr, err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	rest := fs.Args()
	if len(rest) > 0 {
		if err := sendCommand(conn, rest); err != nil {
			fmt.Fprintf(os.Stderr, "cli: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Interactive REPL: same wire protocol, line-oriented UX.
	fmt.Printf("redisclone cli - connected to %s\n", *addr)
	fmt.Printf("commands: PING, SET <key> <value>, GET <key>, DEL <key>, EXISTS <key>, QUIT\n\n")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("redisclone> ")
		if !scanner.Scan() {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		upper := strings.ToUpper(parts[0])
		if upper == "QUIT" || upper == "EXIT" {
			fmt.Println("Goodbye!")
			return
		}
		if err := sendCommand(conn, parts); err != nil {
			fmt.Fprintf(os.Stderr, "cli: %v\n", err)
			continue
		}
	}
}

// sendCommand writes one RESP2 command and prints the reply in redis-cli style.
func sendCommand(conn net.Conn, parts []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(parts))
	for _, p := range parts {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(p), p)
	}
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return err
	}

	reader := bufio.NewReader(conn)
	line, err := readReplyLine(reader)
	if err != nil {
		return err
	}

	switch {
	case strings.HasPrefix(line, "+"):
		fmt.Println(strings.TrimPrefix(line, "+"))
	case strings.HasPrefix(line, "-"):
		fmt.Printf("(error) %s\n", strings.TrimPrefix(line, "-"))
	case strings.HasPrefix(line, ":"):
		fmt.Printf("(integer) %s\n", strings.TrimPrefix(line, ":"))
	case strings.HasPrefix(line, "$"):
		n := strings.TrimPrefix(line, "$")
		if n == "-1" {
			fmt.Println("(nil)")
			return nil
		}
		bulk, err := readBulk(reader, n)
		if err != nil {
			return err
		}
		fmt.Printf("%q\n", string(bulk))
	default:
		fmt.Printf("(raw) %s\n", line)
	}
	return nil
}

func readReplyLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func readBulk(r *bufio.Reader, nStr string) ([]byte, error) {
	var n int
	if _, err := fmt.Sscanf(nStr, "%d", &n); err != nil {
		return nil, fmt.Errorf("invalid bulk length %q", nStr)
	}
	if n < 0 || n > 1<<20 {
		return nil, fmt.Errorf("bulk length %d exceeds bound", n)
	}
	buf := make([]byte, n+2)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf[:n], nil
}
