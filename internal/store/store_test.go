package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestKeyChangesWithDocAndPolicy(t *testing.T) {
	a := Key([]byte("receipt"), "2026.1")
	if a != Key([]byte("receipt"), "2026.1") {
		t.Fatal("not stable")
	}
	if a == Key([]byte("receipt2"), "2026.1") || a == Key([]byte("receipt"), "2026.2") {
		t.Fatal("key must change with document and policy version")
	}
	// boundary: "ab"+"c" must not equal "a"+"bc"
	if Key([]byte("ab"), "c") == Key([]byte("a"), "bc") {
		t.Fatal("ambiguous concatenation")
	}
}

func TestIdempotencyPersists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "idem.json")
	s, err := OpenIdempotency(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("unexpected hit")
	}
	if err := s.Put("k", map[string]int{"v": 1}); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenIdempotency(p)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := s2.Get("k")
	var got map[string]int
	if err := json.Unmarshal(v, &got); !ok || err != nil || got["v"] != 1 {
		t.Fatalf("reload: %s %v %v", v, ok, err)
	}
}

func TestIdempotencyConcurrentPut(t *testing.T) {
	s, _ := OpenIdempotency(filepath.Join(t.TempDir(), "i.json"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Put(string(rune('a'+i)), i) }()
	}
	wg.Wait()
	if s.Len() != 20 {
		t.Fatalf("len=%d", s.Len())
	}
}

func TestCorruptStoreIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "i.json")
	_ = os.WriteFile(p, []byte("{not json"), 0o600)
	if _, err := OpenIdempotency(p); err == nil {
		t.Fatal("expected error")
	}
}

func TestDLQ(t *testing.T) {
	q := DLQ{Dir: filepath.Join(t.TempDir(), "dlq")}
	if _, err := q.Write(DeadLetter{Key: "abc", Class: "schema_violation", Error: "bad enum", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := q.List()
	if err != nil || len(got) != 1 || got[0].Class != "schema_violation" {
		t.Fatalf("%v %v", got, err)
	}
}
