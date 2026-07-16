package server

import "testing"

func TestIsUniqueConstraintErr(t *testing.T) {
	if !isUniqueConstraintErr(errString("UNIQUE constraint failed: llm_providers.model_id")) {
		t.Fatal("expected unique detect")
	}
	if isUniqueConstraintErr(errString("no such table")) {
		t.Fatal("should not match")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
