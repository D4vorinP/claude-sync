package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tawanorg/claude-sync/internal/sync"
)

func TestExitCodeSeparatesHaltedSync(t *testing.T) {
	halted := &sync.FormatTooNewError{Bucket: 2, Binary: 1}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"halted", halted, 3},
		{"halted, wrapped", fmt.Errorf("pull: %w", halted), 3},
		{"other failure", errors.New("network down"), 1},
	}
	for _, c := range cases {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("%s: exitCode = %d, want %d", c.name, got, c.want)
		}
	}
}
