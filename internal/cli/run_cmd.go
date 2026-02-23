package cli

import (
	"fmt"
	"os"
	"time"

	"firebox/internal/api"
	"firebox/internal/model"

	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var (
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
		Use:   "run [OPTIONS] <COMMAND>...",
		Short: "Run a sandbox workload",
		Long:  "Run a command with igloo-style flag syntax where applicable.",
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

			runCommand, err := parseRunCommand(args)
			if err != nil {
				return err
			}

			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()

			spec := model.RunSpec{
				Command:        runCommand,
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
			req := api.RunRequest{
				Interactive: isInteractive(),
				Spec:        spec,
			}

			resp, err := client.Run(ctx, req)
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

	cmd.Flags().StringArrayVar(&mounts, "mount", nil, "Mount in form /host:/guest[:rw|ro][:cow=on|off]")
	cmd.Flags().StringArrayVarP(&volumes, "volume", "v", nil, "Bind mount (host:guest[:ro])")
	cmd.Flags().StringArrayVar(&sandboxMounts, "sandbox", nil, "Sandbox mount (dst or src:dst, always CoW on)")
	cmd.Flags().StringArrayVarP(&env, "env", "e", nil, "Environment variable (KEY=VALUE or KEY)")
	cmd.Flags().StringVarP(&workdir, "workdir", "w", "", "Working directory")

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
	cmd.Flags().BoolVar(&allowHostEnv, "allow-host-env", false, "Allow workload command to access the host home mount directly (less isolated)")
	cmd.Flags().BoolVar(&allowHostWrite, "allow-host-write", false, "Allow direct host writes for rw mounts with cow=off")
	cmd.Flags().BoolVar(&strictBudget, "strict-budget", true, "Fail command when latency exceeds budget")
	cmd.Flags().Int64Var(&timeoutMs, "timeout-ms", int64((5 * time.Second).Milliseconds()), "Command timeout in milliseconds")
	cmd.Flags().SetInterspersed(false)

	return cmd
}

func parseCow(v string) (model.CowMode, error) {
	switch v {
	case "on":
		return model.CowOn, nil
	case "off":
		return model.CowOff, nil
	default:
		return model.CowOn, fmt.Errorf("invalid --cow value %q, expected on|off", v)
	}
}

func parseOptionalCow(v string) (model.CowMode, error) {
	if v == "" {
		return model.CowAuto, nil
	}
	return parseCow(v)
}

func parseNetwork(v string) (model.NetworkMode, error) {
	switch v {
	case string(model.NetworkNAT):
		return model.NetworkNAT, nil
	case string(model.NetworkNone):
		return model.NetworkNone, nil
	default:
		return model.NetworkNAT, fmt.Errorf("invalid --network value %q, expected nat|none", v)
	}
}
