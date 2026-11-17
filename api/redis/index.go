// Package handler: JSON command endpoint used by the portfolio site lab.
// Shares the one internal.Store — output format identical to the REPL.
package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mohamedsalahi/redis_clone/internal"
)

type redisRequest struct {
	Command string `json:"command"`
}

type redisResponse struct {
	Output string `json:"output"`
}

func Handler(store *internal.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(redisResponse{Output: "(error) ERR method not allowed"})
			return
		}
		var req redisRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = json.NewEncoder(w).Encode(redisResponse{Output: "(error) ERR syntax error"})
			return
		}
		parts := strings.Fields(req.Command)
		if len(parts) == 0 {
			_ = json.NewEncoder(w).Encode(redisResponse{Output: ""})
			return
		}
		cmd := strings.ToUpper(parts[0])
		args := parts[1:]
		var output string
		switch cmd {
		case "PING":
			output = "PONG"
		case "SET":
			if len(args) < 2 {
				output = "(error) ERR wrong number of arguments for 'set' command"
				break
			}
			store.Set(args[0], strings.Join(args[1:], " "))
			output = "OK"
		case "GET":
			if len(args) != 1 {
				output = "(error) ERR wrong number of arguments for 'get' command"
				break
			}
			if v, ok := store.Get(args[0]); ok {
				output = `"` + v + `"`
			} else {
				output = "(nil)"
			}
		case "DEL":
			if len(args) != 1 {
				output = "(error) ERR wrong number of arguments for 'del' command"
				break
			}
			if store.Delete(args[0]) {
				output = "OK"
			} else {
				output = "(nil)"
			}
		default:
			output = "(error) ERR unknown command '" + parts[0] + "'"
		}
		_ = json.NewEncoder(w).Encode(redisResponse{Output: output})
	}
}
