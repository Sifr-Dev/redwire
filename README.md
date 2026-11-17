<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/banner-dark.svg">
    <img alt="redisclone — built from zero" src=".github/banner-light.svg" width="100%">
  </picture>
</p>

# redisclone

an honest in-memory key/value store in Go. a local REPL on stdin, plus an
HTTP/JSON command endpoint (`POST /api/redis`) used by the portfolio site lab.
**it is not a network-compatible Redis server** — no  RESP2 wire protocol, no
persistence, no expiry, no replication — and it is never claimed to be one.

## commands

`PING` · `SET k v...` · `GET k` · `DEL k` · `KEYS` · `EXIT`

```console
$ go run .
redisclone — in-memory KV (PING, SET, GET, DEL, KEYS, EXIT)
127.0.0.1:6379> SET greeting hello world
OK
127.0.0.1:6379> GET greeting
"hello world"
```

http mode (used by the portfolio labs):

```sh
go run . -http :6379
curl -X POST localhost:6379/api/redis -d '{"command":"PING"}'   # {"output":"PONG"}
```

## design

- **one store** (`internal/Store`, `sync.RWMutex`) shared by the REPL and the
  HTTP handler — no split-brain copies, no unlocked globals
- command parsing identical in both front-ends
- output format: `PONG`, `OK`, `"quoted value"`, `(nil)`, `(error) ERR ...`
- old `internal/store.go` (unused) and `api/redis` (its own unlocked
  `mockStore`) are replaced by the shared implementation

## verified (2026-11-17)

`go vet ./...` exit 0 · `go test -race ./...` PASS · REPL scripted session
(PING/SET/GET/KEYS/DEL) OK · `POST /api/redis` PING/SET/GET OK · CORS
preflight handled

## scope (what this is not)

not RESP2, not persistent, not network-distributed — deliberately. it is a
small, correct, thread-safe in-memory KV demonstrating the command surface
used by the SIFR portfolio labs.

## license

`MIT` — see `LICENSE`. built from zero.
