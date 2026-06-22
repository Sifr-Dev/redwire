package handler

import (
	"encoding/json"
	"net/http"
	"strings"
)

var mockStore = make(map[string]string)

type RedisRequest struct {
	Command string `json:"command"`
}

type RedisResponse struct {
	Output string `json:"output"`
}

func Handler(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}

	var req RedisRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(RedisResponse{Output: "(error) ERR syntax error"})
		return
	}

	parts := strings.Fields(req.Command)
	if len(parts) == 0 {
		json.NewEncoder(w).Encode(RedisResponse{Output: ""})
		return
	}

	cmd := strings.ToUpper(parts[0])
	var output string

	switch cmd {
	case "PING":
		output = "PONG"
	case "SET":
		if len(parts) >= 3 {
			key := parts[1]
			val := strings.Join(parts[2:], " ")
			mockStore[key] = val
			output = "OK"
		} else {
			output = "(error) ERR wrong number of arguments for 'set' command"
		}
	case "GET":
		if len(parts) == 2 {
			if val, exists := mockStore[parts[1]]; exists {
				output = "\"" + val + "\""
			} else {
				output = "(nil)"
			}
		} else {
			output = "(error) ERR wrong number of arguments for 'get' command"
		}
	default:
		output = "(error) ERR unknown command '" + parts[0] + "'"
	}

	json.NewEncoder(w).Encode(RedisResponse{Output: output})
}
