# Release verification

Releases are manual. Do not tag or merge the feature branch from CI.

1. Start from a clean checkout at the reviewed commit with the Go toolchain from
   `go.mod` (or a newer compatible toolchain).
2. Run:

   ```bash
   test -z "$(gofmt -l cmd internal)"
   go vet ./...
   go test -race -count=1 ./...
   go test ./internal/resp -run '^$' -fuzz '^FuzzReadCommand$' -fuzztime=30s
   CGO_ENABLED=0 go build -trimpath -o redwire ./cmd/redisclone
   ```

3. Smoke-test the actual binary over TCP with `-snapshot` in a temporary
   directory: `SET`, `PEXPIRE`, `TTL`/`PTTL`, terminate with `SIGTERM`, restart,
   and verify `GET` plus a non-increasing `PTTL`. Confirm the process exits and
   no temporary test listener remains.
4. Test resource flags with small positive values. Invalid/zero/negative values
   must exit before listening.
5. Publish source and the checksum of any binary artifact, for example
   `sha256sum redwire > redwire.sha256`. Identify OS/architecture and Go version;
   do not imply that a Linux artifact is portable to other systems.

Release notes must include the exact command subset, default limits, synchronous
full-snapshot write cost, durability/fsync ordering, post-rename directory-sync
ambiguity, single-writer requirement, and absence of AUTH/TLS/production security
or general Redis compatibility. Snapshot version 1 must not be changed silently;
a format change requires an explicit version and migration/recovery policy.

No container image, package repository, signing service, or automatic deployment
is configured in this project.
