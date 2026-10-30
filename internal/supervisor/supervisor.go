// Package supervisor starts a list of processes in order, merges their output
// into one console with a prefix per process, and stops all of them together.
package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Spec is one process to run.
type Spec struct {
	Name string
	Path string
	Args []string
	Env  []string
	Dir  string
	// Log receives the raw output; the console gets it with a prefix.
	Log string
	// Critical processes take everything down when they exit, e.g. the
	// simulator server. Others only report that they exited.
	Critical bool
	// Ready, if set, blocks the start of the next process until it returns.
	Ready func(ctx context.Context) error
	// After runs in the background once the process started, e.g. to push
	// parameters. Its error is reported but does not stop the swarm.
	After func(ctx context.Context) error
}

type proc struct {
	spec Spec
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

type Supervisor struct {
	// Stagger is the pause between starting two processes.
	Stagger time.Duration
	// StopTimeout is how long a process gets after SIGINT before SIGKILL.
	StopTimeout time.Duration

	out   sync.Mutex
	procs []*proc
	width int
}

// Run starts specs in order and blocks until ctx is cancelled or a critical
// process exits, then stops everything in reverse order.
func (s *Supervisor) Run(ctx context.Context, specs []Spec) error {
	for _, sp := range specs {
		s.width = max(s.width, len(sp.Name))
	}
	parent := ctx
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	err := s.start(ctx, cancel, specs)
	if err == nil {
		<-ctx.Done()
		if parent.Err() == nil { // stopped from inside: a critical process died
			err = context.Cause(ctx)
		}
	}
	cancel(nil) // planned stops below must not read as crashes
	s.stopAll()
	return err
}

func (s *Supervisor) start(ctx context.Context, cancel context.CancelCauseFunc, specs []Spec) error {
	for i, sp := range specs {
		if i > 0 && !s.pause(ctx) {
			return nil
		}
		if err := s.startOne(ctx, cancel, i, sp); err != nil {
			return fmt.Errorf("%s: %w", sp.Name, err)
		}
	}
	return nil
}

// pause waits Stagger between two starts; false means ctx was cancelled.
func (s *Supervisor) pause(ctx context.Context) bool {
	if s.Stagger <= 0 {
		return true
	}
	select {
	case <-time.After(s.Stagger):
		return true
	case <-ctx.Done():
		return false
	}
}

// startOne launches one process, watches it, runs its After hook and waits
// until it is Ready.
func (s *Supervisor) startOne(ctx context.Context, cancel context.CancelCauseFunc, i int, sp Spec) error {
	p, err := s.launch(i, sp)
	if err != nil {
		return err
	}
	go s.watch(ctx, cancel, i, p)
	if sp.After != nil {
		go func() {
			if err := sp.After(ctx); err != nil && ctx.Err() == nil {
				s.logf(sp.Name, i, "error: %v", err)
			}
		}()
	}
	if sp.Ready == nil {
		return nil
	}
	return waitReady(ctx, p, sp.Ready)
}

// watch reports a process that exits while the swarm runs, and takes the
// swarm down if the process is critical.
func (s *Supervisor) watch(ctx context.Context, cancel context.CancelCauseFunc, i int, p *proc) {
	<-p.done
	if ctx.Err() != nil {
		return
	}
	s.logf(p.spec.Name, i, "exited: %v", exitText(p.err))
	if p.spec.Critical {
		cancel(fmt.Errorf("%s exited: %v", p.spec.Name, exitText(p.err)))
	}
}

// waitReady fails early if the process dies while it is being waited on.
func waitReady(ctx context.Context, p *proc, ready func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	res := make(chan error, 1)
	go func() { res <- ready(ctx) }()
	select {
	case err := <-res:
		return err
	case <-p.done:
		return fmt.Errorf("exited before it was ready: %v", exitText(p.err))
	}
}

func (s *Supervisor) launch(i int, sp Spec) (*proc, error) {
	if sp.Dir != "" {
		if err := os.MkdirAll(sp.Dir, 0o755); err != nil {
			return nil, err
		}
	}
	var logFile io.WriteCloser = nopCloser{io.Discard}
	if sp.Log != "" {
		if err := os.MkdirAll(filepath.Dir(sp.Log), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(sp.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(f, "\n==== %s %s %v\n", time.Now().Format(time.RFC3339), sp.Path, sp.Args)
		logFile = f
	}

	cmd := exec.Command(sp.Path, sp.Args...)
	cmd.Env = sp.Env
	cmd.Dir = sp.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	s.logf(sp.Name, i, "started pid %d: %s %v", cmd.Process.Pid, sp.Path, sp.Args)

	p := &proc{spec: sp, cmd: cmd, done: make(chan struct{})}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			fmt.Fprintln(logFile, line)
			s.line(sp.Name, i, line)
		}
	}()
	go func() {
		p.err = cmd.Wait()
		pw.Close()
		<-copied
		logFile.Close()
		close(p.done)
	}()
	s.out.Lock()
	s.procs = append(s.procs, p)
	s.out.Unlock()
	return p, nil
}

// stopAll stops processes in reverse start order, so drones go before the
// simulator they are connected to.
func (s *Supervisor) stopAll() {
	for i := len(s.procs) - 1; i >= 0; i-- {
		p := s.procs[i]
		select {
		case <-p.done:
			continue
		default:
		}
		pgid := p.cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGINT)
		select {
		case <-p.done:
		case <-time.After(s.StopTimeout):
			s.logf(p.spec.Name, i, "did not stop in %v, killing", s.StopTimeout)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-p.done
		}
	}
}

var colors = []string{"36", "33", "32", "35", "34", "91", "96", "93", "92", "95"}

func (s *Supervisor) line(name string, i int, text string) {
	s.out.Lock()
	defer s.out.Unlock()
	fmt.Fprintf(os.Stdout, "\x1b[%sm%-*s |\x1b[0m %s\n", colors[i%len(colors)], s.width, name, text)
}

func (s *Supervisor) logf(name string, i int, format string, args ...any) {
	s.line(name, i, "\x1b[1m[swarmsim] "+fmt.Sprintf(format, args...)+"\x1b[0m")
}

func exitText(err error) string {
	var ee *exec.ExitError
	if err == nil {
		return "exit status 0"
	}
	if errors.As(err, &ee) {
		return ee.String()
	}
	return err.Error()
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
