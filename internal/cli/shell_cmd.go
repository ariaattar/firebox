package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"firebox/internal/config"

	"github.com/spf13/cobra"
)

func newShellCmd() *cobra.Command {
	var instanceOverride string

	cmd := &cobra.Command{
		Use:   "shell [-- <command...>]",
		Short: "Connect to an interactive shell in the active runtime instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := exec.LookPath("limactl"); err != nil {
				return fmt.Errorf("limactl not found: install lima (e.g. brew install lima)")
			}

			instance, err := resolveShellInstance(instanceOverride)
			if err != nil {
				return err
			}
			if err := ensureInstanceRunning(instance); err != nil {
				return err
			}

			shellArgs := []string{"shell", instance}
			if len(args) > 0 {
				shellArgs = append(shellArgs, "--")
				shellArgs = append(shellArgs, args...)
			}
			return runAttachedCommand("limactl", shellArgs...)
		},
	}

	cmd.Flags().StringVar(&instanceOverride, "instance", "", "Lima instance name (default: active runtime instance)")
	return cmd
}

func resolveShellInstance(instanceOverride string) (string, error) {
	if v := strings.TrimSpace(instanceOverride); v != "" {
		return v, nil
	}

	paths, err := config.ResolvePaths()
	if err != nil {
		return "", err
	}
	runtimeCfg, err := config.LoadRuntimeConfig(paths.Runtime)
	if err != nil {
		return "", err
	}
	return runtimeCfg.EffectiveInstanceNameForDaemon(paths.DaemonID), nil
}

func ensureInstanceRunning(instance string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	statuses, err := listLimaStatuses(ctx)
	if err != nil {
		return err
	}

	status, exists := statuses[instance]
	if !exists {
		return fmt.Errorf("lima instance %q not found; run `firebox setup` or `firebox image use` first", instance)
	}
	if strings.EqualFold(status, "running") {
		return nil
	}

	fmt.Printf("Starting lima instance %s...\n", instance)
	if err := runStreamingCommand("limactl", "start", "-y", instance); err != nil {
		return fmt.Errorf("start lima instance %q: %w", instance, err)
	}
	return nil
}

func runAttachedCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return newCmdErr(ee.ExitCode(), fmt.Sprintf("%s exited with code %d", name, ee.ExitCode()))
		}
		return err
	}
	return nil
}
