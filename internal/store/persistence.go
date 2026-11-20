package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const snapshotVersion = 1

// Package variables permit precise failure injection without requiring root,
// a faulting kernel, or special filesystem mounts.
var (
	syncSnapshotFile = func(f *os.File) error { return f.Sync() }
	syncSnapshotDir  = func(f *os.File) error { return f.Sync() }
)

type diskSnapshot struct {
	Version  int          `json:"version"`
	Checksum string       `json:"checksum"` // CRC-32 (IEEE) of canonical payload.
	Payload  []diskRecord `json:"payload"`
}

type diskRecord struct {
	Key       string `json:"key"`   // Base64, binary-safe.
	Value     string `json:"value"` // Base64, binary-safe.
	ExpiresAt int64  `json:"expires_at_ms,omitempty"`
}

func writeSnapshot(path string, entries []snapshotEntry) error {
	payload := make([]diskRecord, 0, len(entries))
	for _, item := range entries {
		payload = append(payload, diskRecord{
			Key:       base64.StdEncoding.EncodeToString(item.Key),
			Value:     base64.StdEncoding.EncodeToString(item.Value),
			ExpiresAt: item.ExpiresAt,
		})
	}
	sort.Slice(payload, func(i, j int) bool { return payload[i].Key < payload[j].Key })
	envelope := diskSnapshot{Version: snapshotVersion, Payload: payload}
	envelope.Checksum = checksum(payload)
	contents, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	contents = append(contents, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".redwire-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set snapshot permissions: %w", err)
	}
	if _, err := tmp.Write(contents); err != nil {
		return fmt.Errorf("write temporary snapshot: %w", err)
	}
	// Required for rename durability: file bytes and metadata reach stable
	// storage before the name replaces the previous generation.
	if err := syncSnapshotFile(tmp); err != nil {
		return fmt.Errorf("fsync temporary snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary snapshot: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace snapshot: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open snapshot directory for fsync: %w", err)
	}
	defer d.Close()
	// Required so the directory entry survives a crash, not just file data.
	if err := syncSnapshotDir(d); err != nil {
		return fmt.Errorf("fsync snapshot directory: %w", err)
	}
	committed = true
	return nil
}

func loadSnapshot(path string, limits Limits, now time.Time) ([]snapshotEntry, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		// A prior process may have died before rename. Temporary files are never
		// recovery candidates: the last fully renamed snapshot is authoritative.
		removeTemporarySnapshots(path)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open snapshot: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat snapshot: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("snapshot is not a regular file")
	}
	// Bound disk input independently of configured live bytes: base64 and JSON
	// add overhead, but a corrupt huge file must not cause a huge allocation.
	maxFileBytes := limits.MaxMemoryBytes*4 + int64(limits.MaxKeys)*256 + (1 << 20)
	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("snapshot size %d exceeds bound %d", info.Size(), maxFileBytes)
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	if int64(len(contents)) > maxFileBytes {
		return nil, errors.New("snapshot exceeds size bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var envelope diskSnapshot
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode snapshot (refusing partial recovery): %w", err)
	}
	if decoder.More() {
		return nil, errors.New("snapshot contains trailing JSON values")
	}
	if envelope.Version != snapshotVersion {
		return nil, fmt.Errorf("unsupported snapshot version %d", envelope.Version)
	}
	if checksum(envelope.Payload) != envelope.Checksum {
		return nil, errors.New("snapshot checksum mismatch (refusing partial recovery)")
	}
	if len(envelope.Payload) > limits.MaxKeys {
		return nil, ErrKeyLimit
	}
	entries := make([]snapshotEntry, 0, len(envelope.Payload))
	var totalBytes int64
	seen := make(map[string]struct{}, len(envelope.Payload))
	for _, record := range envelope.Payload {
		key, err := base64.StdEncoding.Strict().DecodeString(record.Key)
		if err != nil {
			return nil, fmt.Errorf("invalid snapshot key encoding: %w", err)
		}
		value, err := base64.StdEncoding.Strict().DecodeString(record.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid snapshot value encoding: %w", err)
		}
		if record.ExpiresAt != 0 && record.ExpiresAt <= now.UnixMilli() {
			continue
		}
		if _, duplicate := seen[string(key)]; duplicate {
			return nil, errors.New("snapshot contains duplicate keys")
		}
		seen[string(key)] = struct{}{}
		totalBytes += int64(len(key) + len(value))
		if totalBytes > limits.MaxMemoryBytes {
			return nil, ErrMemoryLimit
		}
		entries = append(entries, snapshotEntry{Key: key, Value: value, ExpiresAt: record.ExpiresAt})
	}
	return entries, nil
}

func checksum(payload []diskRecord) string {
	canonical, _ := json.Marshal(payload) // The struct contains no failing types.
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE(canonical))
}

func removeTemporarySnapshots(path string) {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".redwire-*.tmp"))
	for _, match := range matches {
		_ = os.Remove(match)
	}
}
