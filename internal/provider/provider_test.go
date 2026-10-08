package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/extract"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

const sample = `BLUE HERON CAFE
Date: Mar 14, 2026
1 Latte 4.50
2 Pasta 24.00
Subtotal 28.50
Sales Tax 8.5% 2.42
Total 30.92
Guests: Ana Ruiz, Ben Okoye and Cy Tran
VISA ****4242`

func run(t *testing.T, version string) expense.Extraction {
	t.Helper()
	raw, err := Mock{}.Extract(context.Background(), Request{Text: sample, PromptVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	ex, err := extract.Parse(raw)
	if err != nil {
		t.Fatalf("mock output must satisfy the schema: %v\n%s", err, raw)
	}
	return ex
}

func TestMockV2(t *testing.T) {
	ex := run(t, "v2")
	if ex.Merchant != "BLUE HERON CAFE" || ex.Date != "2026-03-14" || ex.Amount != "30.92" || ex.Category != "meals" {
		t.Fatalf("%+v", ex)
	}
	if ex.Tax == nil || *ex.Tax != "2.42" || len(ex.Attendees) != 3 || ex.PaymentMethod != "card" {
		t.Fatalf("%+v", ex)
	}
}

func TestMockV1IsWeaker(t *testing.T) {
	ex := run(t, "v1")
	if ex.Amount != "28.50" || ex.Tax != nil || len(ex.Attendees) != 0 || ex.Date != "" {
		t.Fatalf("v1 should pick the subtotal and miss tax, attendees and named-month dates: %+v", ex)
	}
}

func TestMockHooks(t *testing.T) {
	_, err := Mock{}.Extract(context.Background(), Request{Text: HookTimeout})
	if failure.Classify(err) != failure.Retryable {
		t.Errorf("timeout hook: %v", err)
	}
	_, err = Mock{}.Extract(context.Background(), Request{Text: HookStatus})
	if failure.Classify(err) != failure.Retryable {
		t.Errorf("429 hook: %v", err)
	}
	raw, _ := Mock{}.Extract(context.Background(), Request{Text: HookSchema})
	if _, err := extract.Parse(raw); failure.Classify(err) != failure.Schema {
		t.Errorf("schema hook: %v", err)
	}
}

func TestFactory(t *testing.T) {
	for _, n := range []string{"mock", "anthropic", "openai"} {
		if _, err := New(Config{Name: n}); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	if _, err := New(Config{Name: "gemini"}); err == nil {
		t.Error("expected unknown provider error")
	}
}

const goodExtraction = `{"merchant":"Cafe Uno","date":"2026-04-02","amount":"18.40","currency":"USD","category":"meals","tax":null,"attendees":[],"payment_method":"card","document_type":"itemized_receipt","contains_alcohol":false,"confidence":{"merchant":0.9,"date":0.9,"amount":0.9,"currency":0.9,"category":0.8,"tax":0.9,"attendees":0.9,"payment_method":0.9}}`

func req() Request {
	sys, _ := extract.System("v2")
	return Request{System: sys, User: extract.User("x"), Text: "x", Schema: extract.Schema(), PromptVersion: "v2"}
}

func TestAnthropicAgainstFakeServer(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"tool_use",
"content":[{"type":"tool_use","id":"tu_1","name":"record_expense","input":` + goodExtraction + `}],
"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	raw, err := NewAnthropic("test-model", "k", srv.URL).Extract(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := extract.Parse(raw); err != nil {
		t.Fatal(err)
	}
	tc, _ := gotBody["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "record_expense" {
		t.Errorf("tool use not forced: %v", gotBody["tool_choice"])
	}
	tools := gotBody["tools"].([]any)
	schema := tools[0].(map[string]any)["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false || len(schema["required"].([]any)) != 11 {
		t.Errorf("schema not forwarded: %v", schema)
	}
}

func TestAnthropicStatusClassification(t *testing.T) {
	for status, want := range map[int]failure.Class{429: failure.Retryable, 529: failure.Retryable, 500: failure.Retryable, 401: failure.Permanent} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"x"}}`))
		}))
		_, err := NewAnthropic("m", "k", srv.URL).Extract(context.Background(), req())
		srv.Close()
		if got := failure.Classify(err); got != want {
			t.Errorf("status %d: got %q want %q (%v)", status, got, want, err)
		}
	}
}

func TestOpenAICompatibleAgainstFakeServer(t *testing.T) {
	var path string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		esc, _ := json.Marshal(goodExtraction)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":` + string(esc) + `}}]}`))
	}))
	defer srv.Close()
	img := filepath.Join(t.TempDir(), "r.png")
	_ = os.WriteFile(img, []byte("\x89PNG fake"), 0o600)
	r := req()
	r.ImagePath = img
	raw, err := NewOpenAI("llama3.2-vision", srv.URL+"/v1", "").Extract(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := extract.Parse(raw); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("path %s", path)
	}
	rf := gotBody["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Errorf("response_format %v", rf)
	}
	if !strings.Contains(string(mustJSON(gotBody["messages"])), "data:image/png;base64,") {
		t.Error("image not attached as data URL")
	}
}

func TestOpenAIStatusClassification(t *testing.T) {
	for status, want := range map[int]failure.Class{429: failure.Retryable, 503: failure.Retryable, 400: failure.Permanent} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"x","type":"e"}}`))
		}))
		_, err := NewOpenAI("m", srv.URL, "").Extract(context.Background(), req())
		srv.Close()
		if got := failure.Classify(err); got != want {
			t.Errorf("status %d: got %q want %q (%v)", status, got, want, err)
		}
	}
}

func TestUnsupportedImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.bmp")
	_ = os.WriteFile(p, []byte("x"), 0o600)
	r := req()
	r.ImagePath = p
	_, err := NewOpenAI("m", "http://127.0.0.1:1", "").Extract(context.Background(), r)
	var fe *failure.Error
	if !errors.As(err, &fe) || fe.Class != failure.InvalidDoc {
		t.Errorf("got %v", err)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
