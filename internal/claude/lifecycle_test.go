package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeCLI writes a shell script standing in for claude and returns its path.
func fakeCLI(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A stopped process whose events nobody reads must still exit: the reader
// may not block on the channel, and a CLI that ignores stdin gets killed.
func TestStopUnreadProcessExits(t *testing.T) {
	defer func(d time.Duration) { stopGrace = d }(stopGrace)
	stopGrace = 200 * time.Millisecond
	bin := fakeCLI(t, `i=0
while [ $i -lt 2000 ]; do echo '{"type":"stream_event","event":{"type":"ping"}}'; i=$((i+1)); done
exec sleep 60
`)
	p, err := Start(context.Background(), Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let the channel fill up
	p.Stop()
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("stopped process never exited")
	}
}

// A line longer than the scanner allows is reported, and the process is
// still waited for rather than left blocked on its stdout.
func TestOverlongLineReported(t *testing.T) {
	bin := fakeCLI(t, `head -c 70000000 /dev/zero | tr '\0' 'x'; echo`)
	p, err := Start(context.Background(), Options{Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	var sawErr, sawExit bool
	timeout := time.After(10 * time.Second)
	for !sawExit {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				t.Fatal("events closed before Exited")
			}
			sawErr = sawErr || (ev.Err != nil && !ev.Exited)
			sawExit = ev.Exited
		case <-timeout:
			t.Fatal("no Exited event")
		}
	}
	if !sawErr {
		t.Error("overlong line was not reported")
	}
}
