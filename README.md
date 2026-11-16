# redis_clone

A bounded, RESP2-protocol key-value store in Go. **One implementation**, one truth: an in-memory `internal/store.Store` shared by the TCP server and exercised by the test suite. It is explicitly **not** a full Redis clone and makes no compatibility claim beyond the subset below.

## What exists (exactly)

**Wire protocol:** RESP2 over TCP only. Commands are arrays of bulk strings. Responses are simple strings, errors, integers, and bulk strings (nil bulk = `$-1`).

**Commands and semantics (exhaustive):**

| Command | Arity | Reply |
|---|---|---|
| `PING` | 0 or 1 arg | `+PONG`, or the single argument as a bulk string |
| `SET key value` | 2 args | `+OK` |
| `GET key` | 1 arg | bulk string, or `$-1` if absent |
| `DEL key` | 1 arg | `:1` if removed, `:0` otherwise |
| `EXISTS key` | 1 arg | `:1` if present, `:0` otherwise |
| `QUIT` | 0 args | `+OK`, then the connection closes |

**Bounds (enforced by the parser):**

- Command arity ≤ 1024 elements
- Any single bulk string ≤ 1 MiB
- RESP header lines ≤ 512 bytes, must be CRLF-terminated
- Per-connection read deadline of 30 seconds (a connection that stalls is dropped)

**Guarantees:** thread-safe map access (`sync.RWMutex`), one store shared by every connection, `-race`-clean test suite.

**Non-goals (honestly stated):** no persistence, no expiry, no transactions, no pub/sub, no replication, no clustering, no AUTH/TLS, no ACLs, no scripting, no streams. Not wire-compatible with real Redis beyond the RESP2 framing above. Memory grows until keys are deleted (or the process exits).

## Run

```bash
go build -o redisclone ./cmd/redisclone
./redisclone server -addr 127.0.0.1:6379
```

In another terminal:

```bash
./redisclone cli -addr 127.0.0.1:6379 SET greeting "hello"
./redisclone cli -addr 127.0.0.1:6379 GET greeting
./redisclone cli -addr 127.0.0.1:6379 PING
./redisclone cli                 # interactive REPL
```

RESP2, by hand (inline protocol is not implemented — frames only):

```bash
printf '*1\r\n$4\r\nPING\r\n' | nc 127.0.0.1 6379   # correct frame, prints +PONG
```

## Develop

```bash
go vet ./...
go test -race -count=1 ./...
go build ./...
```

CI runs the same three steps on `ubuntu-24.04` (`.github/workflows/ci.yml`).

## Layout

- `internal/store` — the single concurrency-safe store (and its tests)
- `internal/resp` — bounded RESP2 reader/writer (and its tests)
- `internal/server` — TCP accept loop, per-connection goroutine, command execution (and its integration tests)
- `cmd/redisclone` — `server` and `cli` subcommands
