package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newSupervisor() *Supervisor {
	return &Supervisor{Stagger: 10 * time.Millisecond, StopTimeout: 2 * time.Second}
}

func sh(name, script string) Spec {
	return Spec{Name: name, Path: "/bin/sh", Args: []string{"-c", script}, Env: os.Environ()}
}

func TestRunStopsEverythingWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	a := sh("a", "sleep 30")
	a.Log = filepath.Join(dir, "logs", "a.log")
	a.Dir = filepath.Join(dir, "a")
	b := sh("b", "sleep 30")

	ctx, cancel := context.WithCancel(context.Background())
	s := newSupervisor()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, []Spec{a, b}) }()
	waitFor(t, func() bool { return len(s.started()) == 2 })
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run after cancel: %v", err)
	}
	for _, p := range s.procs {
		select {
		case <-p.done:
		default:
			t.Fatalf("%s still running after Run returned", p.spec.Name)
		}
	}
	if _, err := os.Stat(a.Log); err != nil {
		t.Fatalf("log not written: %v", err)
	}
}

func TestRunFailsWhenCriticalProcessExits(t *testing.T) {
	world := sh("world", "exit 3")
	world.Critical = true
	drone := sh("drone", "sleep 30")

	err := newSupervisor().Run(context.Background(), []Spec{drone, world})
	if err == nil || !strings.Contains(err.Error(), "world exited") {
		t.Fatalf("want world exited error, got %v", err)
	}
}

func TestNonCriticalExitKeepsRunning(t *testing.T) {
	short := sh("short", "exit 0")
	long := sh("long", "sleep 30")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := newSupervisor().Run(ctx, []Spec{short, long}); err != nil {
		t.Fatalf("a non-critical exit must not stop the swarm: %v", err)
	}
}

func TestReadyGatesTheNextStart(t *testing.T) {
	var ready atomic.Bool
	first := sh("first", "sleep 30")
	first.Ready = func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		ready.Store(true)
		return nil
	}
	second := sh("second", "sleep 30")
	var startedEarly atomic.Bool
	second.After = func(ctx context.Context) error {
		if !ready.Load() {
			startedEarly.Store(true)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := newSupervisor().Run(ctx, []Spec{first, second}); err != nil {
		t.Fatal(err)
	}
	if startedEarly.Load() {
		t.Fatal("second started before first was ready")
	}
}

func TestReadyErrorStopsStart(t *testing.T) {
	sp := sh("x", "sleep 30")
	sp.Ready = func(ctx context.Context) error { return errors.New("not today") }

	err := newSupervisor().Run(context.Background(), []Spec{sp, sh("never", "sleep 30")})
	if err == nil || !strings.Contains(err.Error(), "x: not today") {
		t.Fatalf("want ready error, got %v", err)
	}
}

func TestReadyFailsWhenProcessDies(t *testing.T) {
	sp := sh("x", "exit 1")
	sp.Ready = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

	err := newSupervisor().Run(context.Background(), []Spec{sp})
	if err == nil || !strings.Contains(err.Error(), "exited before it was ready") {
		t.Fatalf("want exited-before-ready error, got %v", err)
	}
}

func TestAfterErrorIsReportedNotFatal(t *testing.T) {
	var called atomic.Bool
	sp := sh("x", "sleep 30")
	sp.After = func(ctx context.Context) error { called.Store(true); return errors.New("params") }

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := newSupervisor().Run(ctx, []Spec{sp}); err != nil {
		t.Fatalf("an After error must not stop the swarm: %v", err)
	}
	if !called.Load() {
		t.Fatal("After was not called")
	}
}

func TestCancelDuringStagger(t *testing.T) {
	s := &Supervisor{Stagger: time.Hour, StopTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, []Spec{sh("a", "sleep 30"), sh("b", "sleep 30")}) }()
	waitFor(t, func() bool { return len(s.started()) == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(s.procs); n != 1 {
		t.Fatalf("started %d processes, want 1", n)
	}
}

func TestLaunchErrorStopsStart(t *testing.T) {
	sp := Spec{Name: "missing", Path: "/nonexistent/binary"}
	err := newSupervisor().Run(context.Background(), []Spec{sp})
	if err == nil || !strings.Contains(err.Error(), "missing:") {
		t.Fatalf("want launch error, got %v", err)
	}
}

func TestStopKillsProcessIgnoringSIGINT(t *testing.T) {
	s := &Supervisor{StopTimeout: 100 * time.Millisecond}
	sp := sh("stubborn", "trap '' INT; sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, []Spec{sp}) }()
	waitFor(t, func() bool { return len(s.started()) == 1 })
	time.Sleep(100 * time.Millisecond) // let the trap install
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process that ignores SIGINT was not killed")
	}
}

func TestExitText(t *testing.T) {
	if got := exitText(nil); got != "exit status 0" {
		t.Fatalf("exitText(nil) = %q", got)
	}
	if got := exitText(errors.New("boom")); got != "boom" {
		t.Fatalf("exitText(boom) = %q", got)
	}
}

// started is the processes launched so far; safe to poll from tests.
func (s *Supervisor) started() []*proc {
	s.out.Lock()
	defer s.out.Unlock()
	return append([]*proc(nil), s.procs...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in 5s")
}
