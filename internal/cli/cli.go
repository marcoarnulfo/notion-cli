// Package cli wires the cobra command tree.
//
// Execute never calls os.Exit: it returns an exit code so that tests can
// exercise the whole command tree in-process. main is the only place allowed
// to terminate the program.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Exit codes. Pipelines rely on these to tell failure modes apart without
// parsing error strings.
const (
	ExitOK        = 0
	ExitError     = 1
	ExitUsage     = 2
	ExitNotFound  = 3
	ExitDuplicate = 4
	ExitAuth      = 5
)

// codedError carries an exit code alongside the message.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// Errorf builds an error that resolves to a specific exit code.
func Errorf(code int, format string, args ...any) error {
	return &codedError{code: code, err: fmt.Errorf(format, args...)}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "notion-track",
		Short:         "Keep a Notion task database in sync from your terminal and CI",
		SilenceUsage:  true,
		SilenceErrors: true,
		// ArbitraryArgs matters: with cobra's default arg validation an unknown
		// command is rejected inside Find() with a plain error, before RunE ever
		// runs, and we lose the ability to give it exit code 2. Taking the args
		// ourselves keeps that decision here.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return Errorf(ExitUsage, "unknown command %q", args[0])
			}
			// With no arguments the TUI takes over; until it lands, show help.
			return cmd.Help()
		},
	}
	// Without this, cobra's Print* helpers write to stderr, which would put
	// human-readable results on the wrong stream.
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)

	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return Errorf(ExitUsage, "%v", err)
	})

	root.PersistentFlags().String("profile", "", "config profile to use")
	root.PersistentFlags().String("config", "", "path to config file")
	return root
}

// Execute runs the CLI with os.Args and returns the process exit code.
func Execute() int { return executeArgs(os.Args[1:]) }

func executeArgs(args []string) int {
	root := newRootCmd()
	root.SetArgs(args)

	err := root.Execute()
	if err == nil {
		return ExitOK
	}

	fmt.Fprintf(os.Stderr, "error: %v\n", err)

	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ExitUsage
}
