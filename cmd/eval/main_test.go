package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestExitCodes(t *testing.T) {
	root := "../../"
	base := []string{"-golden", root + "eval/golden.json", "-policy", root + "policies/default.yaml"}
	var out, errb bytes.Buffer
	if code := run(append(base, "-prompts", "v1,v2"), &out, &errb); code != 0 || !strings.Contains(out.String(), "GATE PASSED for prompt v2") {
		t.Fatalf("v2 candidate: code=%d\n%s%s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run(append(base, "-prompts", "v2,v1"), &out, &errb); code != 1 || !strings.Contains(out.String(), "GATE FAILED for prompt v1") {
		t.Fatalf("v1 candidate must fail: code=%d\n%s", code, out.String())
	}
	if code := run(append(base, "-prompts", "v9"), &out, &errb); code != 2 {
		t.Fatalf("unknown version: code=%d", code)
	}
	if code := run(append(base, "-provider", "gemini"), &out, &errb); code != 2 {
		t.Fatalf("unknown provider: code=%d", code)
	}
}
