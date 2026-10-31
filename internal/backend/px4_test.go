package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func logFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.log")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWaitLogLineSucceeds(t *testing.T) {
	p := logFile(t, "INFO  [px4] "+px4StartedLine+"\n")
	if err := waitLogLine(context.Background(), p, 0, px4StartedLine, px4FailedLine, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitLogLineFailsFastOnFailLine(t *testing.T) {
	p := logFile(t, "INFO  [init] something\nERROR [px4] "+px4FailedLine+": 256\nmore\n")
	start := time.Now()
	err := waitLogLine(context.Background(), p, 0, px4StartedLine, px4FailedLine, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "ERROR [px4] "+px4FailedLine+": 256") {
		t.Fatalf("want the whole failing line, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("did not fail fast")
	}
	if errors.Is(err, errLogTimeout) {
		t.Fatal("a failed startup is not a timeout")
	}
}

func TestWaitLogLineIgnoresEarlierRuns(t *testing.T) {
	old := "INFO  [px4] " + px4StartedLine + "\n"
	p := logFile(t, old+"INFO  [init] booting\n")
	err := waitLogLine(context.Background(), p, int64(len(old)), px4StartedLine, px4FailedLine, 600*time.Millisecond)
	if !errors.Is(err, errLogTimeout) {
		t.Fatalf("a success line from an earlier run must not count: %v", err)
	}
}

func TestHungStartupShowsWhereItStopped(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 10; i++ {
		body.WriteString("INFO  [init] step\n")
	}
	body.WriteString("ERROR [sensors] Accel #0 fail:  TIMEOUT!\n")
	p := logFile(t, body.String())

	err := px4Hung(waitLogLine(context.Background(), p, 0, px4StartedLine, px4FailedLine, 600*time.Millisecond))
	if !errors.Is(err, errLogTimeout) {
		t.Fatalf("want a timeout, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"startup hung", "comes from stopping it", "Accel #0 fail:  TIMEOUT!"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if n := strings.Count(msg, "INFO  [init] step"); n != 5 {
		t.Errorf("want the last 6 lines (5 steps + the error), got %d steps:\n%s", n, msg)
	}
}

func TestHungStartupWithEmptyLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.log")
	err := waitLogLine(context.Background(), p, 0, px4StartedLine, px4FailedLine, 600*time.Millisecond)
	if !errors.Is(err, errLogTimeout) || !strings.Contains(err.Error(), "(log is empty)") {
		t.Fatalf("got %v", err)
	}
}

func TestWaitLogLineCancelled(t *testing.T) {
	p := logFile(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitLogLine(ctx, p, 0, px4StartedLine, px4FailedLine, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if got := px4Hung(err); got.Error() != err.Error() {
		t.Fatal("px4Hung must pass other errors through")
	}
}
