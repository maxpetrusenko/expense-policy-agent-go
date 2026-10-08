package failure

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Class
	}{
		{"nil", nil, ""},
		{"429", &StatusError{Status: 429, Err: errors.New("rate")}, Retryable},
		{"503", &StatusError{Status: 503, Err: errors.New("down")}, Retryable},
		{"400", &StatusError{Status: 400, Err: errors.New("bad")}, Permanent},
		{"401", &StatusError{Status: 401, Err: errors.New("auth")}, Permanent},
		{"deadline", context.DeadlineExceeded, Retryable},
		{"canceled", context.Canceled, Permanent},
		{"net timeout", timeoutErr{}, Retryable},
		{"schema", New(Schema, "parse", errors.New("x")), Schema},
		{"wrapped", fmt.Errorf("outer: %w", New(InvalidDoc, "validate", errors.New("x"))), InvalidDoc},
		{"unknown", errors.New("boom"), Permanent},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
