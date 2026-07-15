package controllers

import (
	"testing"
)

func TestGetStatusMessageInvalidExclusions(t *testing.T) {
	t.Parallel()

	got := getStatusMessage(nil, nil, []string{"unknown-policy"}, nil, nil, nil)
	expected := "Invalid exclusions: unknown-policy"

	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}
