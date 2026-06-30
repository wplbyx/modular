package errs

import (
	"errors"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	err := New(
		WithCode(2000),
		WithMsgf("business error"),
		WithField("user_id", "12345"),
	)

	if err.Code() != 2000 {
		t.Fatalf("expected code 2000, got %d", err.Code())
	}
	if err.Error() != "[Code:2000] business error" {
		t.Fatalf("unexpected error string: %s", err.Error())
	}
	if got := err.Fields()["user_id"]; got != "12345" {
		t.Fatalf("expected field user_id=12345, got %v", got)
	}
}

func TestNewDefault(t *testing.T) {
	err := New()

	if err.Code() != 1000 {
		t.Fatalf("expected default code 1000, got %d", err.Code())
	}
	if err.Error() != "[Code:1000] unknown error" {
		t.Fatalf("unexpected default error string: %s", err.Error())
	}
}

func TestWrapStandardError(t *testing.T) {
	origin := errors.New("original error")
	err := Wrap(origin, WithCode(3000), WithMsgf("wrapped error"))

	if err.Code() != 3000 {
		t.Fatalf("expected code 3000, got %d", err.Code())
	}
	if !errors.Is(err, origin) {
		t.Fatal("expected wrapped error to match original error")
	}
	if errors.Unwrap(err) != origin {
		t.Fatal("expected unwrap to return original error")
	}
}

func TestWrapCustomErrorMergesFields(t *testing.T) {
	inner := New(
		WithCode(1001),
		WithMsgf("inner"),
		WithField("inner_key", "inner_value"),
	)

	outer := Wrap(
		inner,
		WithCode(2002),
		WithMsgf("outer"),
		WithField("outer_key", "outer_value"),
	)

	if outer.Code() != 2002 {
		t.Fatalf("expected outer code 2002, got %d", outer.Code())
	}
	if got := outer.Fields()["inner_key"]; got != "inner_value" {
		t.Fatalf("expected merged inner field, got %v", got)
	}
	if got := outer.Fields()["outer_key"]; got != "outer_value" {
		t.Fatalf("expected outer field, got %v", got)
	}
	if errors.Unwrap(outer) != nil {
		t.Fatal("expected custom error without cause to unwrap to nil")
	}
}

func TestWrapNil(t *testing.T) {
	if err := Wrap(nil); err != nil {
		t.Fatalf("expected nil wrap to return nil, got %v", err)
	}
}

func TestFullErrStack(t *testing.T) {
	err := Wrap(
		errors.New("db timeout"),
		WithCode(5000),
		WithMsgf("query failed"),
		WithField("table", "devices"),
	)

	stack := err.FullErrStack()
	for _, want := range []string{"Error Stack", "Error Chain", "Code: 5000", "table", "Creation Stack"} {
		if !strings.Contains(stack, want) {
			t.Fatalf("expected stack to contain %q, got:\n%s", want, stack)
		}
	}
}

func TestErrorsIsByCode(t *testing.T) {
	target := New(WithCode(404), WithMsgf("not found"))
	err := New(WithCode(404), WithMsgf("missing device"))

	if !errors.Is(err, target) {
		t.Fatal("expected errors.Is to match custom errors by code")
	}
}
