package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// termState / termRestore are seams over term.GetState / term.Restore,
// mirroring the pattern the other prompt seams already use (isInteractive,
// readToken, readLine): a real terminal fd is expensive to fake in a test,
// so tests replace the seam instead of building one.
var (
	termState   = term.GetState
	termRestore = term.Restore
)

// osExit is a seam over os.Exit so terminateSelf's exit-status arithmetic
// can be tested without ending the test binary.
var osExit = os.Exit

// terminateSelf ends this process immediately with the conventional
// 128+signal exit status, the same status the OS's default action for sig
// would have produced.
//
// A previous version reset sig to its default disposition, re-sent it to
// this process, and then blocked in select{} waiting to be killed by it.
// That depends on sig's default disposition actually being "terminate" —
// true most of the time, but not always: a background job started by a
// non-interactive shell, or a wrapper doing `trap "" INT`, commonly
// inherits SIG_IGN for SIGINT. signal.Reset restores exactly that ignored
// disposition, the re-raise is then a no-op, and select{} blocks forever —
// with the terminal's echo already turned back on and the token-reading
// goroutine still parked on the blocked read, wide open to echo whatever
// the user types next. Verified against a real binary: still alive after
// two minutes, with a second SIGTERM swallowed too, because by then nothing
// was reading sigCh any more.
//
// Calling os.Exit here instead depends on none of that: it is this process
// making its own exit syscall, so it terminates unconditionally no matter
// what sig's disposition was before this process ever ran, and it does so
// synchronously — there is no window where the terminal is echoing again
// but the process is still around to receive further input.
var terminateSelf = func(sig os.Signal) {
	code := 1
	if n, ok := sig.(syscall.Signal); ok {
		code = 128 + int(n)
	}
	osExit(code)
}

// readTokenInterruptible reads the token the way readToken does, but treats
// SIGINT and SIGTERM as a reason to restore the terminal first.
//
// term.ReadPassword restores terminal state (turns local echo back on) with
// a defer, but a signal that kills the process terminates it before that
// defer ever gets a chance to run. Ctrl-C at an unexpected password-style
// prompt is the single most likely thing a user does here, and until this
// wrapped the read, it left their shell silently echo-less — `stty sane`
// required — for the rest of the session.
func readTokenInterruptible() (string, error) {
	fd := int(os.Stdin.Fd())
	state, stateErr := termState(fd)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	type result struct {
		token string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		tok, err := readToken()
		done <- result{tok, err}
	}()

	select {
	case sig := <-sigCh:
		// Stop delivery before anything else: the process is about to exit
		// unconditionally (see terminateSelf), and a second signal that
		// arrives in the meantime must fall back to its normal disposition
		// rather than being silently dropped into a channel this function
		// is no longer reading — that silent drop is what let a second
		// SIGTERM be swallowed by the previous, blocking implementation.
		signal.Stop(sigCh)
		// Best-effort: if we couldn't read the original state we have
		// nothing to restore it to, but the process terminates below
		// either way.
		if stateErr == nil {
			termRestore(fd, state)
		}
		terminateSelf(sig)
		// Unreachable in production (terminateSelf exits the process
		// above); reachable only when a test replaces terminateSelf with a
		// stub.
		return "", fmt.Errorf("interrupted by %v", sig)
	case r := <-done:
		return r.token, r.err
	}
}
