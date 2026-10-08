package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryRunIdempotencyAndDLQ(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	args := []string{"-policy", "../../policies/default.yaml", "-state", state, "-now", "2026-06-30", "-log-level", "error", "../../examples/receipts"}

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != 1 {
		t.Fatalf("expected exit 1 (two dead letters), got %d\n%s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("want 5 result lines, got %d", len(lines))
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad JSON line: %v", err)
		}
	}
	if !strings.Contains(errb.String(), "2 dead-lettered") {
		t.Fatalf("summary: %s", errb.String())
	}

	// second run: the 5 good documents are skipped, the 2 bad ones fail again
	out.Reset()
	errb.Reset()
	run(args, &out, &errb)
	if !strings.Contains(errb.String(), "0 processed") || !strings.Contains(errb.String(), "5 skipped") {
		t.Fatalf("second run should skip: %s", errb.String())
	}

	out.Reset()
	if code := run([]string{"-state", state, "-dlq-list"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "schema_violation") || !strings.Contains(out.String(), "invalid_document") {
		t.Fatalf("dlq list: %s", out.String())
	}
}

func TestUsageErrors(t *testing.T) {
	var out, errb bytes.Buffer
	if run(nil, &out, &errb) != 2 {
		t.Error("no args must exit 2")
	}
	if run([]string{"-policy", "../../policies/default.yaml", filepath.Join(t.TempDir(), "missing")}, &out, &errb) != 2 {
		t.Error("missing path must exit 2")
	}
	if run([]string{"-policy", "/nonexistent.yaml", "x"}, &out, &errb) != 2 {
		t.Error("bad policy must exit 2")
	}
}

func TestSingleFileAndImagePairing(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("Cafe\nTotal $5.00\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "a.png"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "b.jpg"), []byte("x"), 0o600)
	docs, err := collect(dir)
	if err != nil || len(docs) != 2 {
		t.Fatalf("%v %v", docs, err)
	}
	if docs[0].Text == "" || !strings.HasSuffix(docs[0].ImagePath, "a.png") {
		t.Fatalf("txt+png must pair: %+v", docs[0])
	}
	if docs[1].Text != "" || !strings.HasSuffix(docs[1].ImagePath, "b.jpg") {
		t.Fatalf("image-only: %+v", docs[1])
	}
}
