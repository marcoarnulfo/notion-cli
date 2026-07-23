package cli

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"
)

// withInterruptSeams swaps the signal-handling seams and restores them on
// cleanup, mirroring withInteractivePrompt's pattern for the prompt seams.
func withInterruptSeams(t *testing.T, state func(int) (*term.State, error),
	restore func(int, *term.State) error, raise func(os.Signal)) {
	t.Helper()
	oldState, oldRestore, oldRaise := termState, termRestore, raiseAndWait
	if state != nil {
		termState = state
	}
	if restore != nil {
		termRestore = restore
	}
	if raise != nil {
		raiseAndWait = raise
	}
	t.Cleanup(func() {
		termState, termRestore, raiseAndWait = oldState, oldRestore, oldRaise
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
	var raisedSig atomic.Value // os.Signal

	raised := make(chan struct{})
	withInterruptSeams(t,
		func(fd int) (*term.State, error) { return fakeState, nil },
		func(fd int, s *term.State) error {
			restoredWithRightState.Store(s == fakeState)
			restored.Store(true)
			return nil
		},
		func(sig os.Signal) {
			raisedSig.Store(sig)
			close(raised)
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
	case <-raised:
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
	if sig, _ := raisedSig.Load().(os.Signal); sig != syscall.SIGINT {
		t.Fatalf("raiseAndWait received %v, want SIGINT", sig)
	}

	select {
	case <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("readTokenInterruptible never returned after the stubbed raiseAndWait")
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
		t.Fatal("raiseAndWait must not be called when no signal arrives")
	})

	tok, err := readTokenInterruptible()
	if err != nil {
		t.Fatalf("readTokenInterruptible: %v", err)
	}
	if tok != "ntn_typed" {
		t.Fatalf("token = %q, want ntn_typed", tok)
	}
}
