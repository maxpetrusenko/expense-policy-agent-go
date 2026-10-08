package flags

import (
	"fmt"
	"testing"
)

func TestResolveRollout(t *testing.T) {
	f := Flags{Stable: "v1", Candidate: "v2", Percent: 0}
	if f.Resolve("x") != "v1" {
		t.Fatal("0% must stay stable")
	}
	f.Percent = 100
	if f.Resolve("x") != "v2" {
		t.Fatal("100% must be candidate")
	}
	f.Percent = 30
	n := 0
	prev := map[string]bool{}
	for i := 0; i < 5000; i++ {
		k := fmt.Sprint("doc", i)
		if f.Resolve(k) == "v2" {
			n++
			prev[k] = true
		}
	}
	if n < 1300 || n > 1700 {
		t.Fatalf("30%% rollout routed %d/5000", n)
	}
	// raising the percentage never removes a document from the candidate
	f.Percent = 60
	for k := range prev {
		if f.Resolve(k) != "v2" {
			t.Fatalf("%s dropped out of candidate when percent increased", k)
		}
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv(EnvStable, "v1")
	t.Setenv(EnvCandidate, "v2")
	t.Setenv(EnvPercent, "25")
	f, err := FromEnv()
	if err != nil || f.Stable != "v1" || f.Candidate != "v2" || f.Percent != 25 {
		t.Fatalf("%+v %v", f, err)
	}
	t.Setenv(EnvPercent, "250")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected range error")
	}
	t.Setenv(EnvPercent, "10")
	t.Setenv(EnvCandidate, "v9")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected unknown version error")
	}
}
