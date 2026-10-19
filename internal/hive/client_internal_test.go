package hive

import (
	"io"
	"strings"
	"testing"
)

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestReadBodyRefusesOversizedResponses(t *testing.T) {
	if _, err := readBody(io.LimitReader(zeros{}, maxResponseBytes+1)); err == nil || !strings.Contains(err.Error(), "larger than 64 MiB") {
		t.Errorf("oversized: %v", err)
	}
	if b, err := readBody(strings.NewReader(`{"ok":true}`)); err != nil || string(b) != `{"ok":true}` {
		t.Errorf("small: %q %v", b, err)
	}
}
