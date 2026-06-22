package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

var store = make(map[string]string)
var storeMux sync.Mutex

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	
	fmt.Println("=== Redis Clone - In-Memory KV Store ===")
	fmt.Println("Commands: SET <key> <value>, GET <key>, PING, DEL <key>, EXIT")
	fmt.Println()
	
	for {
		fmt.Print("127.0.0.1:6379> ")
		if !scanner.Scan() {
			break
		}
		
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.ToUpper(input) == "EXIT" || strings.ToUpper(input) == "QUIT" {
			fmt.Println("Goodbye!")
			break
		}
		
		parts := strings.Fields(input)
		cmd := strings.ToUpper(parts[0])
		
		switch cmd {
		case "PING":
			fmt.Println("PONG")
		case "SET":
			if len(parts) >= 3 {
				storeMux.Lock()
				store[parts[1]] = strings.Join(parts[2:], " ")
				storeMux.Unlock()
				fmt.Println("OK")
			} else {
				fmt.Println("(error) ERR wrong number of arguments for 'set' command")
			}
		case "GET":
			if len(parts) == 2 {
				storeMux.Lock()
				val, exists := store[parts[1]]
				storeMux.Unlock()
				if exists {
					fmt.Printf("\"%s\"\n", val)
				} else {
					fmt.Println("(nil)")
				}
			} else {
				fmt.Println("(error) ERR wrong number of arguments for 'get' command")
			}
		case "DEL":
			if len(parts) == 2 {
				storeMux.Lock()
				delete(store, parts[1])
				storeMux.Unlock()
				fmt.Println("OK")
			} else {
				fmt.Println("(error) ERR wrong number of arguments for 'del' command")
			}
		default:
			fmt.Printf("(error) ERR unknown command '%s'\n", cmd)
		}
	}
}
