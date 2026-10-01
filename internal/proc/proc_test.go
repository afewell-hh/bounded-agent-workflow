package proc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func sh(body string, cap int, timeout time.Duration) ([]byte, error) {
	return Run(context.Background(), Spec{Path: "/bin/sh", Args: []string{"-c", body}, Timeout: timeout, StdoutCap: cap, StderrCap: 8})
}

func TestStdoutCapBoundary(t *testing.T) {
	out, err := sh("printf 12345678", 8, 5*time.Second)
	if err != nil || string(out) != "12345678" {
		t.Fatalf("at cap: %q %v", out, err)
	}
	if _, err := sh("printf 123456789", 8, 5*time.Second); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("over cap: %v", err)
	}
	// Stderr beyond its cap is discarded, not a failure and not returned.
	out, err = sh("printf 'secret-stderr-value-long' >&2; printf ok", 8, 5*time.Second)
	if err != nil || string(out) != "ok" {
		t.Fatalf("stderr cap: %q %v", out, err)
	}
}

func TestExitAndTimeout(t *testing.T) {
	var ee *ExitError
	if _, err := sh("exit 3", 8, 5*time.Second); !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("exit: %v", err)
	}
	start := time.Now()
	// A TERM-ignoring child is killed after the grace period and joined.
	_, err := sh("trap '' TERM; sleep 30 & wait", 8, 200*time.Millisecond)
	if !errors.Is(err, ErrTimeout) || time.Since(start) > 5*time.Second || strings.Contains(err.Error(), "secret") {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Spec{Path: "/bin/sh", Args: []string{"-c", "sleep 30"}, Timeout: time.Minute, StdoutCap: 8}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("ctx: %v", err)
	}
}
