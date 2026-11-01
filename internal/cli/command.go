package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// executionError distinguishes processing failures (exit 1) from invalid input (exit 2).
type executionError struct{ error }

func (e executionError) Unwrap() error { return e.error }

func executionFailure(err error) error {
	if err == nil {
		return nil
	}
	return executionError{err}
}

// Cobra's help/completion writers may discard write errors. Retain the first
// failure so the public runner still reports output failures as exit code 1.
type checkedWriter struct {
	io.Writer
	err error
}

func (w *checkedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if w.err == nil {
		w.err = err
	}
	return n, err
}

func execute(ctx context.Context, args []string, input io.Reader, out, diagnostics io.Writer, open opener, interfaces interfaceLister) int {
	output := &checkedWriter{Writer: out}
	root := newRootCommand(input, output, diagnostics, open, interfaces)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	var failure executionError
	if output.err != nil && !errors.As(err, &failure) {
		err = executionFailure(output.err)
	}
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(diagnostics, err)
	if errors.As(err, &failure) {
		return 1
	}
	return 2
}

func newRootCommand(input io.Reader, out, diagnostics io.Writer, open opener, interfaces interfaceLister) *cobra.Command {
	root := &cobra.Command{
		Use: "echonet", Short: "Control ECHONET Lite air conditioners",
		SilenceErrors: true, SilenceUsage: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("unknown command %q", args[0])
			}
			return cmd.Help()
		},
	}
	root.SetIn(input)
	root.SetOut(out)
	root.SetErr(diagnostics)
	root.AddCommand(newAirconCommand(open, interfaces), newConfigCommand(open, interfaces))
	root.AddCommand(&cobra.Command{
		Use: "completion bash|zsh", Short: "Generate a shell completion script", Args: cobra.ExactArgs(1),
		ValidArgsFunction: positionalValues("bash", "zsh"),
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			switch args[0] {
			case "bash":
				err = root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				err = root.GenZshCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell %q: expected bash or zsh", args[0])
			}
			return executionFailure(err)
		},
	})
	return root
}

func mustRegisterCompletion(cmd *cobra.Command, name string, fn cobra.CompletionFunc) {
	// Flag names are defined immediately above; failure indicates a programming error.
	if err := cmd.RegisterFlagCompletionFunc(name, fn); err != nil {
		panic(err)
	}
}

func requireSubcommand(cmd *cobra.Command, _ []string) error {
	return fmt.Errorf("%s requires a subcommand", cmd.CommandPath())
}
