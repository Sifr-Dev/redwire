<div align="center">

<img src="assets/banner.svg" alt="redwire — bounded RESP2 key-value server" width="100%">

# redwire

**A small, bounded RESP2 key-value server with synchronous atomic snapshots.**

<p>
  <img alt="Go 1.26.3" src="https://img.shields.io/badge/GO-1.26.3-00ADD8?style=for-the-badge&logo=go&logoColor=white&labelColor=05070a">
  <img alt="Build: go build" src="https://img.shields.io/badge/BUILD-GO_BUILD-c8cdd7?style=for-the-badge&labelColor=05070a">
  <img alt="Tests: 36 passing + fuzz" src="https://img.shields.io/badge/TESTS-36_PASSING_%2B_FUZZ-39c5cf?style=for-the-badge&labelColor=05070a">
  <img alt="License MIT" src="https://img.shields.io/badge/LICENSE-MIT-2ea043?style=for-the-badge&labelColor=05070a">
  <img alt="CI" src="https://github.com/Sifr-Dev/redwire/actions/workflows/ci.yml/badge.svg">
</p>

</div>

> redwire (formerly `redwire`) commits every mutation by writing and `fsync`ing
> a complete snapshot, renaming it, and `fsync`ing the directory — then answers.

## What it is

redwire is a deliberately narrow RESP2 server: ten commands, one shared store,
explicit limits, and a durability design that is easy to state and test. It is
**not Redis**, is not production-hardened, and claims compatibility only with the
commands and framing documented below.

The module, binary and package keep their original name, `redwire`; the product
name is redwire and the repository is still `Sifr-Dev/redwire`.

## Current capabilities

- **RESP2 command set** — `PING`, `SET`, `GET`, `DEL`, `EXISTS`, `PEXPIRE`, `TTL`,
  `PTTL`, `QUIT` with the documented replies; arrays of non-null bulk strings
  only, inline commands rejected (`internal/resp/resp.go`, `internal/store/store.go`).
- **TTL semantics** — absolute expiry, `TTL` rounded up, `PTTL` rounded down, `-1`
  without TTL, `-2` when absent, non-positive `PEXPIRE` deletes (`store.go`;
  `store_test.go`).
- **Bounded resources** — `-max-keys` (default 10,000), `-max-memory-bytes`
  (default 64 MiB of live key+value bytes), `-max-connections` (default 128);
  per command at most 1,024 elements, 1 MiB per bulk and 2 MiB total framing;
  512-byte header lines (`internal/resp/resp.go`, `internal/server/server.go`;
  `resp_test.go`, `server_test.go`).
- **Deadlines** — 30 s read idle/partial-command deadline and 10 s response write
  deadline per connection (`server.go`; `server_test.go`).
- **Synchronous durability** — for every successful mutation: check limits, build
  a candidate, write a unique temp file, `fsync` and close it, atomically rename
  over the snapshot, `fsync` the parent directory, then publish and acknowledge
  (`internal/store/persistence.go`; `store_test.go`).
- **Versioned, checksummed snapshot** — JSON version 1 with base64 keys/values,
  absolute expiries and CRC-32; duplicate keys, oversize data, truncation and
  corruption fail startup rather than loading partial state (`persistence.go`;
  `store_test.go`).
- **Injected failure coverage** — file `fsync`, rename and directory-`fsync`
  failures leave memory and the previous snapshot unchanged, with the documented
  ambiguous directory-`fsync` case called out (`store_test.go`).
- **Graceful shutdown** — SIGINT/SIGTERM stop the listener, close clients, wait
  for handlers and exit zero (`cmd/redwire/main.go`; `server_test.go`).
- **36 Go tests** plus a fuzz target; CI runs formatting, vet, race tests, build
  and a bounded 5 s fuzz run.
- **Packaging** — `packaging/arch/PKGBUILD` and `docs/RELEASE.md`.

## Quick start

```sh
git clone https://github.com/Sifr-Dev/redwire.git
cd redwire
go build -o redwire ./cmd/redwire
./redwire server -addr 127.0.0.1:6379 -snapshot ./redwire.json
```

```sh
./redwire cli SET greeting hello
./redwire cli PEXPIRE greeting 60000
./redwire cli TTL greeting
./redwire cli GET greeting
```

An empty `-snapshot` disables persistence. The address defaults to loopback.
There is no AUTH, TLS, ACL or safe multi-process file locking: do not expose the
server to untrusted networks or run two writers against one snapshot.

## Verification

```sh
gofmt -l cmd internal
go vet ./...
go test -race -count=1 ./...
go build ./...
go test ./internal/resp -run '^$' -fuzz '^FuzzReadCommand$' -fuzztime=5s
```

Observed on this machine (2026-11-28; Go 1.27.4, AMD Ryzen 7 7435HS, Linux
7.3.4):

```text
ok  github.com/Sifr-Dev/redwire/internal/resp    1.154s
ok  github.com/Sifr-Dev/redwire/internal/server  1.023s
ok  github.com/Sifr-Dev/redwire/internal/store   1.222s
36 tests (unit, loopback TCP, recovery, injected fsync failures), -race
fuzz: 1,531,074 execs in 5 s (~308k/s), PASS
```

Recovery check: after 500 synchronous `SET`s and a clean stop, restarting against
the same snapshot returned `k0 = v0` and `k499 = v499`.

## Architecture

| Path | Responsibility |
|---|---|
| `internal/resp` | Bounded RESP2 reader/writer and protocol tests |
| `internal/store` | Store, TTL, snapshot encoding/decoding, fault injection |
| `internal/server` | TCP admission, deadlines, dispatch, shutdown |
| `cmd/redwire` | `server` and `cli` entry points |

## Durability and recovery

The design is a complete, synchronous atomic snapshot per successful `SET`,
effective `DEL` and effective `PEXPIRE`:

1. Check live key/byte limits and build a candidate state.
2. Write a uniquely named temp file in the snapshot directory, `fsync`, close.
3. Atomically `rename` over the configured snapshot on the same filesystem.
4. `fsync` the parent directory so the rename itself is durable.
5. Publish the candidate in memory and acknowledge the client.

Reads never wait for a background checkpoint. Writes are serialised and each one
copies and `fsync`s the full dataset. There is no AOF replay and no compaction.

A write/`fsync`/rename error leaves memory and the previous snapshot unchanged and
rejects the mutation. A directory-open/`fsync` error after rename is
**ambiguous**: the command returns an error and does not publish the candidate,
but the renamed snapshot may survive a crash — it is still whole and checksummed,
never intentionally torn. The tests inject failures; they do not simulate power
loss, and `SIGKILL`/power-loss recovery is not claimed beyond this protocol.
Lazy expiration never rewrites the snapshot until the next effective mutation;
expired values may remain on disk but are never returned or restored.

## Benchmarks

Loopback, one RESP client, 500 sequential operations against a snapshot on this
machine's filesystem.

- **Hardware:** AMD Ryzen 7 7435HS (8C/16T), 15 GiB RAM, Linux 7.3.4-zen2 x86_64.
- **Workload:** `SET k{i} v{i}` then `GET k{i}` for i in 0..499; server built with
  `go build`, snapshot in `/tmp`, default limits.
- **Methodology:** wall-clock around 500 round trips from a Python socket client;
  ops/s = 500 / elapsed.

```text
SET  n=500  total 0.228 s  ≈ 2,196 ops/s
GET  n=500  total 0.081 s  ≈ 6,140 ops/s
snapshot size after the run: 18,248 bytes
```

The `SET` figure is dominated by the `fsync` + full-snapshot protocol — that is
the design, not an accident. The `GET` figure is partly client-bound (one
round trip at a time, Python); it is not a server throughput ceiling.

## Limitations

- Complete snapshot per mutation: write cost grows with dataset size, and
  throughput falls as the store fills.
- No AOF, compaction, background checkpoint, or incremental persistence.
- Commands are a documented subset; no transactions, pub/sub, scripting, cluster,
  replication or RESP3.
- No AUTH, TLS, ACL, or multi-process file locking.
- Limits are on live key+value bytes, not process RSS; maps, connections, parser
  buffers and a candidate snapshot add bounded overhead.
- Lazy expiry only; expired keys can remain in the snapshot until the next write.
- Media/filesystem `fsync` semantics are assumed; failure injection is not a
  power-loss test.
- Packaging artifacts under `packaging/arch/` include a 0.1.1 source snapshot and
  are not regenerated by CI.

## Roadmap (NOT IMPLEMENTED)

- Append-only log with compaction as an alternative durability mode.
- Background checkpointing with a bounded snapshot window.
- RESP3 and pipelined request batching.
- AUTH/TLS/ACL and safe single-writer file locking.
- Regenerating the Arch package from the current tree.
- A load benchmark with concurrency and dataset-size sweeps.
- Binary rename to `monolith` while keeping `redwire` working.

## License

MIT. See [LICENSE](LICENSE).
