package apperr

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestFromClassifiesErrors(t *testing.T) {
	cases := []struct {
		err  error
		code Code
	}{
		{NotFound("x"), CodeNotFound},
		{fmt.Errorf("wrapped: %w", Validation("bad")), CodeValidation},
		{context.DeadlineExceeded, CodeTimeout},
		{fmt.Errorf("op: %w", context.Canceled), CodeCancelled},
		{errors.New("raw"), CodeInternal},
	}
	for _, c := range cases {
		if got := From(c.err).Code; got != c.code {
			t.Errorf("%v → %s, want %s", c.err, got, c.code)
		}
	}
	if From(nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestInternalHidesCause(t *testing.T) {
	e := From(errors.New("password=hunter2"))
	if e.Message != "internal error" || !errors.Is(e, e.Unwrap()) {
		t.Fatalf("unexpected: %+v", e)
	}
}

func TestCodesAreUnique(t *testing.T) {
	seen := map[Code]bool{}
	for _, c := range Codes() {
		if seen[c] {
			t.Fatalf("duplicate code %s", c)
		}
		seen[c] = true
	}
}
