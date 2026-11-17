// redisclone — an honest in-memory key/value store.
// REPL on stdin by default; `-http :PORT` serves the JSON command handler
// (used by the portfolio site's redis lab). NOT a network-compatible Redis
// server and never claimed to be one.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	redis "github.com/mohamedsalahi/redis_clone/api/redis"
	"github.com/mohamedsalahi/redis_clone/internal"
)

func runREPL(store *internal.Store, in *os.File, out *os.File) {
	sc := bufio.NewScanner(in)
	fmt.Fprintln(out, "redisclone — in-memory KV (PING, SET, GET, DEL, KEYS, EXIT)")
	for {
		fmt.Fprint(out, "127.0.0.1:6379> ")
		if !sc.Scan() {
			return
		}
		parts := strings.Fields(strings.TrimSpace(sc.Text()))
		if len(parts) == 0 {
			continue
		}
		out.WriteString(exec(store, parts) + "\n")
	}
}

func exec(store *internal.Store, parts []string) string {
	cmd := strings.ToUpper(parts[0])
	args := parts[1:]
	switch cmd {
	case "PING":
		return "PONG"
	case "SET":
		if len(args) < 2 {
			return "(error) ERR wrong number of arguments for 'set' command"
		}
		store.Set(args[0], strings.Join(args[1:], " "))
		return "OK"
	case "GET":
		if len(args) != 1 {
			return "(error) ERR wrong number of arguments for 'get' command"
		}
		if v, ok := store.Get(args[0]); ok {
			return fmt.Sprintf("%q", v)
		}
		return "(nil)"
	case "DEL":
		if len(args) != 1 {
			return "(error) ERR wrong number of arguments for 'del' command"
		}
		if store.Delete(args[0]) {
			return "OK"
		}
		return "(nil)"
	case "KEYS":
		keys := store.Keys()
		if len(keys) == 0 {
			return "(empty list)"
		}
		return strings.Join(keys, " ")
	case "EXIT", "QUIT":
		fmt.Println("Goodbye!")
		os.Exit(0)
	default:
		return fmt.Sprintf("(error) ERR unknown command '%s'", cmd)
	}
	// unreachable: EXIT/QUIT os.Exit, all other cases return
	return ""
}

func main() {
	httpMode := flag.String("http", "", "serve the JSON command handler on this address (e.g. :6379)")
	flag.Parse()

	store := internal.NewStore()

	if *httpMode != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/redis", redis.Handler(store))
		fmt.Printf("redisclone listening on %s (POST /api/redis {\"command\":\"PING\"})\n", *httpMode)
		if err := http.ListenAndServe(*httpMode, mux); err != nil {
			fmt.Fprintln(os.Stderr, "fatal:", err)
			os.Exit(1)
		}
		return
	}
	runREPL(store, os.Stdin, os.Stdout)
}
