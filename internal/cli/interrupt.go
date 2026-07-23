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

// raiseAndWait resets sig to its default disposition and re-sends it to this
// process, then blocks. In production this call never returns: the OS
// terminates the process via sig's default action — the ordinary Ctrl-C
// exit, complete with the conventional 128+signal exit status — which is
// exactly what should happen once the terminal has been put back in a sane
// state. Tests replace this seam with something that returns instead of
// blocking, since re-raising a real signal at the test process would end
// the whole `go test` run.
var raiseAndWait = func(sig os.Signal) {
	signal.Reset(sig)
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		p.Signal(sig)
	}
	select {}
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
		// Best-effort: if we couldn't read the original state we have
		// nothing to restore it to, but the read below still gets
		// interrupted correctly either way.
		if stateErr == nil {
			termRestore(fd, state)
		}
		raiseAndWait(sig)
		// Unreachable in production (raiseAndWait blocks forever above);
		// reachable only when a test replaces raiseAndWait with a stub.
		return "", fmt.Errorf("interrupted by %v", sig)
	case r := <-done:
		return r.token, r.err
	}
}
