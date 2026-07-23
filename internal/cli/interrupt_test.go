package cli

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"
)

// withInterruptSeams swaps the signal-handling seams and restores them on
// cleanup, mirroring withInteractivePrompt's pattern for the prompt seams.
func withInterruptSeams(t *testing.T, state func(int) (*term.State, error),
	restore func(int, *term.State) error, terminate func(os.Signal)) {
	t.Helper()
	oldState, oldRestore, oldTerminate := termState, termRestore, terminateSelf
	if state != nil {
		termState = state
	}
	if restore != nil {
		termRestore = restore
	}
	if terminate != nil {
		terminateSelf = terminate
	}
	t.Cleanup(func() {
		termState, termRestore, terminateSelf = oldState, oldRestore, oldTerminate
	})
}

// This is the regression test for the SIGINT-leaves-the-terminal-broken
// defect: term.ReadPassword restores terminal state with a defer, and a
// defer never runs when an unhandled SIGINT kills the process first. A real
// Ctrl-C at the prompt was reproduced (via a PTY) to leave ECHO disabled
// after the process died, forcing `stty sane` to get a usable shell back.
//
// This test cannot reproduce that exact symptom without a real terminal
// (term.GetState/Restore need an actual tty fd), so it verifies the
// mechanism directly instead: readTokenInterruptible must call termRestore
// before the signal is allowed to actually end the process. A real SIGINT
// is sent to this very process — safe here only because
// readTokenInterruptible has already called signal.Notify by the time it
// arrives, and raiseAndWait is stubbed to return instead of re-raising it
// for real.
func TestReadTokenInterruptibleRestoresTerminalBeforeDying(t *testing.T) {
	oldReadToken := readToken
	t.Cleanup(func() { readToken = oldReadToken })

	started := make(chan struct{})
	readToken = func() (string, error) {
		close(started)
		select {} // blocks like a real term.ReadPassword call waiting on stdin
	}

	fakeState := &term.State{}
	var restored atomic.Bool
	var restoredWithRightState atomic.Bool
	var terminatedWith atomic.Value // os.Signal

	terminated := make(chan struct{})
	withInterruptSeams(t,
		func(fd int) (*term.State, error) { return fakeState, nil },
		func(fd int, s *term.State) error {
			restoredWithRightState.Store(s == fakeState)
			restored.Store(true)
			return nil
		},
		func(sig os.Signal) {
			terminatedWith.Store(sig)
			close(terminated)
		},
	)

	resultCh := make(chan error, 1)
	go func() {
		_, err := readTokenInterruptible()
		resultCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the read to start blocking")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("sending SIGINT to self: %v", err)
	}

	select {
	case <-terminated:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the SIGINT to reach readTokenInterruptible")
	}

	if !restored.Load() {
		t.Fatal("the terminal state was never restored: a real SIGINT would have " +
			"killed the process with the terminal still in no-echo mode")
	}
	if !restoredWithRightState.Load() {
		t.Fatal("termRestore was called with a different state than termState returned")
	}
	if sig, _ := terminatedWith.Load().(os.Signal); sig != syscall.SIGINT {
		t.Fatalf("terminateSelf received %v, want SIGINT", sig)
	}

	select {
	case <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("readTokenInterruptible never returned after the stubbed terminateSelf")
	}
}

// The ordinary path — no signal — must still work exactly like a direct
// readToken() call: this is the "no regression on the common case" half of
// the fix.
func TestReadTokenInterruptibleReturnsTheTokenWhenUninterrupted(t *testing.T) {
	oldReadToken := readToken
	t.Cleanup(func() { readToken = oldReadToken })
	readToken = func() (string, error) { return "ntn_typed", nil }

	withInterruptSeams(t, nil, nil, func(os.Signal) {
		t.Fatal("terminateSelf must not be called when no signal arrives")
	})

	tok, err := readTokenInterruptible()
	if err != nil {
		t.Fatalf("readTokenInterruptible: %v", err)
	}
	if tok != "ntn_typed" {
		t.Fatalf("token = %q, want ntn_typed", tok)
	}
}

// Regression test for the "signal with an ignored disposition" defect: the
// old raiseAndWait called signal.Reset(sig) and re-raised sig, trusting the
// OS to kill the process via sig's default action. That default action is
// not always "terminate" — a background job started from a non-interactive
// shell, or a wrapper doing `trap "" INT`, commonly inherits SIG_IGN for
// SIGINT. signal.Reset restores exactly that disposition, the re-raise is a
// no-op, and the old code then blocked in select{} forever: the terminal's
// echo had already been turned back on, and the token-reading goroutine was
// still parked on the blocked read, wide open to leak whatever the user
// typed next. Verified against a real binary: still alive after two minutes,
// with a second SIGTERM swallowed too because sigCh was no longer read.
//
// signal.Ignore(SIGINT) reproduces the same disposition Go's runtime would
// see for an inherited SIG_IGN: it is what a call to signal.Reset/Stop with
// no other watcher left would unwind back to, exactly the scenario that broke
// the old code. terminateSelf is stubbed only so the test process itself
// survives the call — the real fix is that terminateSelf no longer depends
// on the signal's disposition to do its job (it calls os.Exit directly), so
// this test also passes as proof that the fix does not merely narrow the old
// window: readTokenInterruptible returns promptly instead of hanging, and it
// does so having never relied on the OS re-raise/default-action path at all.
func TestReadTokenInterruptibleDoesNotHangWhenSIGINTWasIgnoredBeforeNotify(t *testing.T) {
	oldReadToken := readToken
	t.Cleanup(func() { readToken = oldReadToken })

	started := make(chan struct{})
	readToken = func() (string, error) {
		close(started)
		select {} // blocks like a real term.ReadPassword call waiting on stdin
	}

	// Simulate the inherited-SIG_IGN scenario: SIGINT ignored before this
	// process's own signal-handling machinery (Notify, inside
	// readTokenInterruptible) ever touches it.
	signal.Ignore(syscall.SIGINT)
	t.Cleanup(func() { signal.Reset(syscall.SIGINT) })

	var restored atomic.Bool
	var terminatedWith atomic.Value // os.Signal
	terminated := make(chan struct{})
	withInterruptSeams(t,
		func(fd int) (*term.State, error) { return &term.State{}, nil },
		func(fd int, s *term.State) error { restored.Store(true); return nil },
		func(sig os.Signal) {
			terminatedWith.Store(sig)
			close(terminated)
		},
	)

	resultCh := make(chan error, 1)
	go func() {
		_, err := readTokenInterruptible()
		resultCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the read to start blocking")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("sending SIGINT to self: %v", err)
	}

	// The whole point of the fix: this must not take anywhere near the ~2
	// minutes the unpatched binary was observed to hang for.
	select {
	case <-terminated:
	case <-time.After(2 * time.Second):
		t.Fatal("readTokenInterruptible hung: a signal with an ignored " +
			"disposition must not leave the process blocked in select{}")
	}
	if !restored.Load() {
		t.Fatal("the terminal state was never restored")
	}
	if sig, _ := terminatedWith.Load().(os.Signal); sig != syscall.SIGINT {
		t.Fatalf("terminateSelf received %v, want SIGINT", sig)
	}

	select {
	case <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("readTokenInterruptible never returned after the stubbed terminateSelf")
	}
}

// terminateSelf must compute the conventional 128+signal exit status itself,
// rather than depending on the OS's default action for sig (which, as the
// test above demonstrates, is not always "terminate"). osExit is the seam
// that lets this be checked without ending the test binary.
func TestTerminateSelfUsesConventionalSignalExitStatus(t *testing.T) {
	oldExit := osExit
	t.Cleanup(func() { osExit = oldExit })

	var gotCode int
	var callCount int
	osExit = func(code int) { gotCode = code; callCount++ }

	terminateSelf(syscall.SIGINT)
	if gotCode != 128+int(syscall.SIGINT) {
		t.Fatalf("exit code for SIGINT = %d, want %d", gotCode, 128+int(syscall.SIGINT))
	}

	terminateSelf(syscall.SIGTERM)
	if gotCode != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit code for SIGTERM = %d, want %d", gotCode, 128+int(syscall.SIGTERM))
	}

	if callCount != 2 {
		t.Fatalf("osExit called %d times, want 2", callCount)
	}
}
