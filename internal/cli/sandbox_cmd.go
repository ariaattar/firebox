package cli

import (
	"fmt"
	"os"
	"time"

	"firebox/internal/api"
	"firebox/internal/model"

	"github.com/spf13/cobra"
)

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "sandbox", Short: "Manage named sandboxes"}
	cmd.AddCommand(newSandboxCreateCmd())
	cmd.AddCommand(newSandboxStartCmd())
	cmd.AddCommand(newSandboxStopCmd())
	cmd.AddCommand(newSandboxRmCmd())
	cmd.AddCommand(newSandboxListCmd())
	cmd.AddCommand(newSandboxInspectCmd())
	cmd.AddCommand(newSandboxExecCmd())
	cmd.AddCommand(newSandboxDiffCmd())
	cmd.AddCommand(newSandboxApplyCmd())
	return cmd
}

func newSandboxCreateCmd() *cobra.Command {
	var (
		id             string
		mounts         []string
		volumes        []string
		sandboxMounts  []string
		env            []string
		cow            string
		cowRoot        string
		network        string
		networkAllow   []string
		networkDeny    []string
		fileAllowPaths []string
		fileDenyPaths  []string
		fileAllowExts  []string
		fileDenyExts   []string
		profile        string
		workdir        string
		allowHostEnv   bool
		allowHostWrite bool
		strictBudget   bool
		timeoutMs      int64
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a sandbox definition",
		RunE: func(cmd *cobra.Command, args []string) error {
			globalCow, err := parseCow(cow)
			if err != nil {
				return err
			}
			rootCow, err := parseOptionalCow(cowRoot)
			if err != nil {
				return err
			}
			netMode, err := parseNetwork(network)
			if err != nil {
				return err
			}
			parsedMounts, err := mergeMountInputs(mounts, volumes, sandboxMounts, globalCow)
			if err != nil {
				return err
			}
			envVars, err := normalizeEnvVars(env)
			if err != nil {
				return err
			}

			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()

			spec := model.RunSpec{
				Env:            envVars,
				Mounts:         parsedMounts,
				Cow:            globalCow,
				CowRoot:        rootCow,
				Network:        netMode,
				NetworkAllow:   networkAllow,
				NetworkDeny:    networkDeny,
				FileAllowPaths: fileAllowPaths,
				FileDenyPaths:  fileDenyPaths,
				FileAllowExts:  fileAllowExts,
				FileDenyExts:   fileDenyExts,
				Profile:        profile,
				Workdir:        workdir,
				AllowHostEnv:   allowHostEnv,
				AllowHostWrite: allowHostWrite,
				StrictBudget:   strictBudget,
				TimeoutMs:      timeoutMs,
			}
			resp, err := client.CreateSandbox(ctx, api.CreateSandboxRequest{ID: id, Spec: spec})
			if err != nil {
				return err
			}
			fmt.Println(resp.Sandbox.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&id, "id", "", "Sandbox id")
	cmd.Flags().StringArrayVar(&mounts, "mount", nil, "Mount in form /host:/guest[:rw|ro][:cow=on|off]")
	cmd.Flags().StringArrayVarP(&volumes, "volume", "v", nil, "Bind mount (host:guest[:ro])")
	cmd.Flags().StringArrayVar(&sandboxMounts, "sandbox", nil, "Sandbox mount (dst or src:dst, always CoW on)")
	cmd.Flags().StringArrayVarP(&env, "env", "e", nil, "Environment variable (KEY=VALUE or KEY)")
	cmd.Flags().StringVar(&cow, "cow", "on", "Global CoW mode (on|off)")
	cmd.Flags().StringVar(&cowRoot, "cow-root", "", "Rootfs CoW mode override (on|off)")
	cmd.Flags().StringVarP(&network, "network", "n", string(model.NetworkNAT), "Network mode (nat|none)")
	cmd.Flags().StringArrayVar(&networkAllow, "network-allow", nil, "Allow outbound destination (IP/CIDR/hostname/domain, repeatable)")
	cmd.Flags().StringArrayVar(&networkDeny, "network-deny", nil, "Deny outbound destination (IP/CIDR/hostname/domain, repeatable)")
	cmd.Flags().StringArrayVar(&fileAllowPaths, "file-allow-path", nil, "Allow host mount path prefix/glob (absolute)")
	cmd.Flags().StringArrayVar(&fileDenyPaths, "file-deny-path", nil, "Deny host mount path prefix/glob (absolute)")
	cmd.Flags().StringArrayVar(&fileAllowExts, "file-allow-ext", nil, "Allow mounted file extensions (e.g. .go, .md)")
	cmd.Flags().StringArrayVar(&fileDenyExts, "file-deny-ext", nil, "Deny mounted file extensions (e.g. .pem)")
	cmd.Flags().StringVar(&profile, "profile", "default", "Sandbox profile")
	cmd.Flags().StringVarP(&workdir, "workdir", "w", "", "Working directory")
	cmd.Flags().BoolVar(&allowHostEnv, "allow-host-env", false, "Allow workload command to access the host home mount directly (less isolated)")
	cmd.Flags().BoolVar(&allowHostWrite, "allow-host-write", false, "Allow direct host writes for rw mounts with cow=off")
	cmd.Flags().BoolVar(&strictBudget, "strict-budget", true, "Fail command when latency exceeds budget")
	cmd.Flags().Int64Var(&timeoutMs, "timeout-ms", int64((5 * time.Second).Milliseconds()), "Command timeout in milliseconds")

	return cmd
}

func newSandboxStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <id>",
		Short: "Start sandbox warm context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()
			resp, err := client.StartSandbox(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("%s %s\n", resp.Sandbox.ID, resp.Sandbox.Status)
			return nil
		},
	}
}

func newSandboxStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id>",
		Short: "Stop sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()
			resp, err := client.StopSandbox(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("%s %s\n", resp.Sandbox.ID, resp.Sandbox.Status)
			return nil
		},
	}
}

func newSandboxRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Remove sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()
			if err := client.RemoveSandbox(ctx, args[0]); err != nil {
				return err
			}
			fmt.Println(styleSuccess("removed"))
			return nil
		},
	}
}

func newSandboxListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List sandboxes",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()
			resp, err := client.ListSandboxes(ctx)
			if err != nil {
				return err
			}
			if len(resp.Sandboxes) == 0 {
				fmt.Println(styleMuted("no sandboxes"))
				return nil
			}
			fmt.Println(styleHeader("ID\tSTATUS\tPROFILE"))
			for _, sb := range resp.Sandboxes {
				fmt.Printf("%s\t%s\t%s\n", sb.ID, colorSandboxStatus(string(sb.Status)), sb.Profile)
			}
			return nil
		},
	}
}

func colorSandboxStatus(status string) string {
	switch status {
	case "running":
		return styleSuccess(status)
	case "created":
		return styleWarn(status)
	case "stopped":
		return styleMuted(status)
	default:
		return status
	}
}

func newSandboxInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <id>",
		Short: "Inspect sandbox details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()
			resp, err := client.InspectSandbox(ctx, args[0])
			if err != nil {
				return err
			}
			enc := jsonEncoder(os.Stdout)
			return enc(resp.Sandbox)
		},
	}
}

func newSandboxExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <id> -- <command...>",
		Short: "Execute command in a named sandbox",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("usage: firebox sandbox exec <id> -- <command...>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()

			resp, err := client.ExecSandbox(ctx, api.SandboxExecRequest{
				ID:          args[0],
				Command:     args[1:],
				Interactive: isInteractive(),
			})
			if err != nil {
				return err
			}
			if resp.Result.Stdout != "" {
				_, _ = os.Stdout.WriteString(resp.Result.Stdout)
			}
			if resp.Result.Stderr != "" {
				_, _ = os.Stderr.WriteString(resp.Result.Stderr)
			}
			if resp.Error != "" {
				return newCmdErr(1, resp.Error)
			}
			if resp.Result.ExitCode != 0 {
				return newCmdErr(resp.Result.ExitCode, fmt.Sprintf("command failed with exit code %d", resp.Result.ExitCode))
			}
			return nil
		},
	}
}

func newSandboxDiffCmd() *cobra.Command {
	var (
		path  string
		limit int
	)

	cmd := &cobra.Command{
		Use:   "diff <id>",
		Short: "Show CoW changes for a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()

			resp, err := client.DiffSandbox(ctx, api.SandboxDiffRequest{
				ID:    args[0],
				Path:  path,
				Limit: limit,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return newCmdErr(1, resp.Error)
			}

			if len(resp.Result.Mounts) == 0 {
				fmt.Println("no changes")
				return nil
			}

			for _, mount := range resp.Result.Mounts {
				for _, ch := range mount.Changes {
					var op string
					switch ch.Op {
					case model.DiffAdd:
						op = "A"
					case model.DiffModify:
						op = "M"
					case model.DiffDelete:
						op = "D"
					default:
						op = "?"
					}
					fmt.Printf("%s %s\n", op, ch.Path)
				}
				if mount.Truncated {
					fmt.Printf("... truncated output for %s\n", mount.GuestPath)
				}
			}
			fmt.Printf(
				"summary: added=%d modified=%d deleted=%d mounts=%d duration=%dms\n",
				resp.Result.Added,
				resp.Result.Modified,
				resp.Result.Deleted,
				len(resp.Result.Mounts),
				resp.Result.DurationMs,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "Restrict to a single sandbox mount path")
	cmd.Flags().IntVar(&limit, "limit", 200, "Max listed changes per mount")
	return cmd
}

func newSandboxApplyCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "apply <id>",
		Short: "Apply CoW changes back to host for a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()

			resp, err := client.ApplySandbox(ctx, api.SandboxApplyRequest{
				ID:   args[0],
				Path: path,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return newCmdErr(1, resp.Error)
			}
			fmt.Printf(
				"applied=%d deleted=%d mounts=%d duration=%dms\n",
				resp.Result.Applied,
				resp.Result.Deleted,
				len(resp.Result.Mounts),
				resp.Result.DurationMs,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "Restrict to a single sandbox mount path")
	return cmd
}
