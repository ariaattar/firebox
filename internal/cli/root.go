package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type exitCoder interface {
	error
	ExitCode() int
}

type cmdErr struct {
	msg  string
	code int
}

func (e cmdErr) Error() string             { return e.msg }
func (e cmdErr) ExitCode() int             { return e.code }
func newCmdErr(code int, msg string) error { return cmdErr{msg: msg, code: code} }

func Execute() int {
	cmd := newRootCmd()
	if err := cmd.Execute(); err != nil {
		if ec, ok := err.(exitCoder); ok {
			fmt.Fprintln(os.Stderr, ec.Error())
			return ec.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "firebox",
		Short:         "Firebox sandbox CLI",
		Long:          "Firebox orchestrates low-latency sandbox execution using a Lima + Firecracker backend.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newDaemonCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newSandboxCmd())
	root.AddCommand(newImageCmd())
	root.AddCommand(newSetupCmd())
	root.AddCommand(newMetricsCmd())
	root.AddCommand(newCompletionCmd(root))
	return root
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(os.Stdout)
			case "zsh":
				return root.GenZshCompletion(os.Stdout)
			case "fish":
				return root.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unknown shell %q", args[0])
			}
		},
	}
	return cmd
}
