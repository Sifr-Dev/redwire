# High-Performance In-Memory Key-Value Store (Redis Clone)

A lightweight Redis-compatible in-memory key-value store built in Go.

## Tech Stack
- Go
- POSIX sockets
- Concurrency (mutex-based thread safety)

## Features
- SET/GET/DEL/PING commands (Redis-like protocol)
- Thread-safe operations
- CLI interface

## Run
```bash
go run main.go
```

## Usage
```
127.0.0.1:6379> SET mykey "Hello World"
OK
127.0.0.1:6379> GET mykey
"Hello World"
127.0.0.1:6379> PING
PONG
```
