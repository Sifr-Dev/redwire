<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/banner-dark.svg">
    <img alt="redwire — built from zero" src=".github/banner-light.svg" width="100%">
  </picture>
</p>

# redwire

A small, bounded RESP2 key-value server in Go. One `internal/store.Store` is
shared by all TCP connections. This is **not Redis**, is not production-hardened,
and claims compatibility only with the commands and framing documented below.

## Commands

Commands are RESP2 arrays of non-null bulk strings; inline commands are rejected.

| Command | Reply |
|---|---|
| `PING [message]` | `+PONG`, or the message as a bulk string |
| `SET key value` | `+OK`; replaces the value and clears its TTL |
| `GET key` | Bulk string, or `$-1` if absent/expired |
| `DEL key` | `:1` if removed, otherwise `:0` |
| `EXISTS key` | `:1` if present and unexpired, otherwise `:0` |
| `PEXPIRE key milliseconds` | `:1` if a live key was changed, otherwise `:0`; non-positive TTL deletes |
| `TTL key` | Seconds remaining, rounded up; `-1` without TTL, `-2` if absent |
| `PTTL key` | Milliseconds remaining, rounded down; `-1` without TTL, `-2` if absent |
| `QUIT` | `+OK`, then close; extra arguments are rejected |

Protocol errors return `-ERR Protocol error` and close the connection. Wrong
arity, unknown commands, limit failures, and persistence failures return errors
without closing an otherwise usable connection. The original command replies and
single-key-only `DEL`/`EXISTS` semantics are preserved.

## Bounds

Defaults (all three server limits must be positive):

- `-max-keys=10000` live keys.
- `-max-memory-bytes=67108864` (64 MiB) of **live key and value bytes**. This is
  not a process-RSS limit: maps, connections, parser buffers, snapshot encoding,
  and a candidate snapshot during mutation add bounded overhead.
- `-max-connections=128` admitted TCP connections. Excess connections receive an
  error and close.
- Per command: at most 1024 elements, 1 MiB per bulk string, and 2 MiB total
  declared framing/body bytes. Header lines are at most 512 bytes including CRLF;
  lengths are non-negative decimal integers.
- Per admitted connection: 30-second read idle/partial-command deadline and
  10-second response write deadline. Each handler has a bounded response buffer.

The aggregate command budget fixes the original defect where 1024 individually
valid 1 MiB bulks could allocate about 1 GiB. Fixed-size header reads avoid
unterminated-line growth. Connection admission bounds goroutines, write deadlines
bound slow readers, and mutation checks bound stored data.

## Run

```bash
go build -o redwire ./cmd/redwire
./redwire server -addr 127.0.0.1:6379 -snapshot ./redwire.json \
  -max-keys 10000 -max-memory-bytes 67108864 -max-connections 128

./redwire cli SET greeting hello
./redwire cli PEXPIRE greeting 60000
./redwire cli TTL greeting
./redwire cli GET greeting
```

An empty `-snapshot` disables persistence. The address defaults to loopback;
there is no AUTH, TLS, ACL, or safe multi-process file locking. Do not expose this
server to untrusted networks or run two writers against one snapshot.

`SIGINT` and `SIGTERM` stop the listener, close active clients, wait for handlers,
and exit zero. No additional snapshot is needed at shutdown because successful
persistent mutations are committed synchronously.

## Durability and recovery

The smallest durability design is a **complete, synchronous atomic snapshot** per
successful `SET`, effective `DEL`, and effective `PEXPIRE`. The snapshot contains
version 1 JSON with base64 keys/values, absolute expiry times, and a CRC-32
checksum for accidental-corruption detection (not authentication).

For every mutation:

1. Check live key/byte limits and build a candidate state.
2. Write a uniquely named temporary file in the snapshot directory, then `fsync`
   and close that file.
3. Atomically `rename` it over the configured snapshot on the same filesystem.
4. `fsync` the parent directory so the rename itself is durable.
5. Publish the candidate in memory and acknowledge the client.

Reads never wait for a background checkpoint. Persistent writes are serialized,
and every write copies and `fsync`s the full dataset: this favors simplicity and
recovery correctness over throughput. There is no AOF replay or compaction.

Startup reads only the last fully renamed snapshot, enforces a size bound,
requires valid JSON/version/checksum, and rejects duplicate keys or data over the
configured limits. A corrupt or truncated snapshot fails startup rather than
silently loading partial data. Expired entries are omitted during recovery, so a
restart does not extend a TTL. Temporary files are never recovery candidates;
orphaned temporary files are removed when no snapshot exists.

A write/file-`fsync`/rename error leaves both memory and the previous snapshot
unchanged and rejects the mutation. A directory-open/`fsync` error after rename is
**ambiguous**: the command returns an error and does not publish the candidate,
but the renamed snapshot may survive a crash. The file is still whole and
checksummed, never intentionally torn. Actual media/filesystem `fsync` semantics
are required for the durability guarantee; these tests inject failures, not power
loss. `SIGKILL`/power-loss recovery is not claimed beyond this protocol.

Lazy expiration removes unreachable entries from memory on access. It does not
rewrite the snapshot until the next effective mutation; expired values may remain
in the on-disk snapshot but are never returned or restored.

## Develop and verify

Requires the Go version in `go.mod` (currently Go 1.26.3 or newer).

```bash
gofmt -w cmd internal
go vet ./...
go test -race -count=1 ./...
go build ./...
go test ./internal/resp -run '^$' -fuzz '^FuzzReadCommand$' -fuzztime=5s
```

Tests include seeded property-style binary/fragmented protocol round trips,
fuzzing, live loopback TCP command and connection-limit tests, graceful shutdown,
binary snapshot recovery, absolute TTL recovery, corruption/truncation rejection,
and injected file/directory `fsync` failures. CI runs formatting, vet, race tests,
build, and a short fuzz job; see `.github/workflows/ci.yml`.

## Release

See [docs/RELEASE.md](docs/RELEASE.md) for reproducible build, verification, and
release-notes guidance. No release is published automatically by this branch.

## Layout

- `internal/resp` — bounded RESP2 reader/writer and protocol tests.
- `internal/store` — bounded store, TTL, snapshots, and fault/recovery tests.
- `internal/server` — TCP admission, deadlines, execution, and shutdown tests.
- `cmd/redisclone` — server and small line-oriented CLI.
