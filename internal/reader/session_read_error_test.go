package reader

import (
	"errors"
	"fmt"
	"testing"
)

func TestSessionReadErrorAs(t *testing.T) {
	inner := errors.New("enoent")
	err := NewSessionReadError(ReadSourceMissing, "source_missing", inner)
	wrapped := fmt.Errorf("get session: %w", err)

	got, ok := AsSessionReadError(wrapped)
	if !ok {
		t.Fatal("expected AsSessionReadError")
	}
	if got.Kind != ReadSourceMissing || got.ReasonCode != "source_missing" {
		t.Fatalf("got %+v", got)
	}
	if !errors.Is(wrapped, inner) {
		t.Fatal("unwrap chain broken")
	}
	if _, ok := AsSessionReadError(errors.New("plain")); ok {
		t.Fatal("plain error should not match")
	}
}
