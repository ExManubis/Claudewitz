package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
)

func TestToastsDedupeAndExpire(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, "")
	m.notify(toastInfo, "Plan mode", "")
	m.notify(toastSuccess, "Theme: charm", "")
	m.notify(toastInfo, "Plan mode", "")
	if len(m.toasts) != 2 || m.toasts[1].title != "Plan mode" {
		t.Fatalf("duplicate should replace and move to the top: %+v", m.toasts)
	}
	for i := 0; i < 10; i++ {
		m.notify(toastInfo, strings.Repeat("x", i+1), "")
	}
	if len(m.toasts) != toastMax {
		t.Fatalf("stack should be capped at %d, got %d", toastMax, len(m.toasts))
	}
	if m.pruneToasts(time.Now().Add(time.Minute)) {
		t.Fatal("all toasts should have expired")
	}
}

func TestUsageWarningsFireOncePerThreshold(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, "")
	m.limits["five_hour"] = claude.RateLimitWindow{Utilization: 0.5}
	if m.usageWarnings() != nil {
		t.Fatal("no warning below 80%")
	}
	m.limits["five_hour"] = claude.RateLimitWindow{Utilization: 0.83}
	m.usageWarnings()
	if len(m.toasts) != 1 || !strings.Contains(m.toasts[0].title, "80%") {
		t.Fatalf("expected an 80%% warning: %+v", m.toasts)
	}
	m.usageWarnings()
	if len(m.toasts) != 1 {
		t.Fatal("80% warning should fire only once")
	}
	m.limits["five_hour"] = claude.RateLimitWindow{Utilization: 0.95}
	m.usageWarnings()
	if len(m.toasts) != 2 || m.toasts[1].kind != toastError {
		t.Fatalf("expected a 90%% error toast: %+v", m.toasts)
	}
}
