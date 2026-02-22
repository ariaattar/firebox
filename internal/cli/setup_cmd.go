package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"firebox/internal/api"
	"firebox/internal/config"
	"firebox/internal/daemon"
	"firebox/internal/model"

	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	var (
		name          string
		yamlFile      string
		rebuild       bool
		autoInstall   bool
		restartDaemon bool
		warmRuntime   bool
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install dependencies and bootstrap a ready-to-run firebox runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateImageName(name); err != nil {
				return err
			}
			if err := ensureLimaInstalled(autoInstall); err != nil {
				return err
			}

			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(paths); err != nil {
				return err
			}

			absYAML, err := resolveSetupYAMLPath(paths, yamlFile)
			if err != nil {
				return err
			}

			rec, built, err := ensureSetupImage(paths, name, absYAML, rebuild)
			if err != nil {
				return err
			}
			if err := setActiveImage(paths, rec); err != nil {
				return err
			}

			if restartDaemon {
				if err := restartAndWarmDaemon(paths, warmRuntime); err != nil {
					return err
				}
			}

			status := "reused"
			if built {
				status = "built"
			}
			fmt.Printf(
				"setup complete: image=%s instance=%s status=%s runtime=%s\n",
				rec.Name,
				rec.InstanceName,
				status,
				rec.YAMLFile,
			)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "devyaml", "Image name to build/use")
	cmd.Flags().StringVarP(&yamlFile, "file", "f", "", "Lima YAML path (default: auto-discover examples/firebox-dev.yaml, with embedded fallback)")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "Force rebuild if image already exists")
	cmd.Flags().BoolVar(&autoInstall, "install-lima", true, "Install lima via brew when limactl is missing")
	cmd.Flags().BoolVar(&restartDaemon, "restart-daemon", true, "Restart daemon after setup so runtime change is applied")
	cmd.Flags().BoolVar(&warmRuntime, "warm", true, "Run a warm-up command after daemon restart")
	return cmd
}

const (
	defaultSetupYAMLName = "firebox-dev.yaml"
	embeddedSetupYAML    = `vmType: vz
nestedVirtualization: true
mountType: virtiofs
cpus: 4
memory: "8GiB"
disk: "100GiB"
images:
  - location: "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-arm64.img"
    arch: "aarch64"
mounts:
  - location: "~"
    writable: true
provision:
  - mode: system
    script: |
      set -eu
      export DEBIAN_FRONTEND=noninteractive
      apt-get update
      apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        jq \
        python-is-python3 \
        python3 \
        python3-pip \
        python3-venv \
        rsync \
        unzip \
        zip
      python3 -m pip install --break-system-packages uv
`
)

func resolveSetupYAMLPath(paths config.Paths, raw string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		return resolveImageYAMLPath(raw)
	}

	for _, candidate := range defaultSetupYAMLCandidates() {
		abs, err := resolveImageYAMLPath(candidate)
		if err == nil {
			return abs, nil
		}
	}

	fallback := filepath.Join(paths.StateDir, defaultSetupYAMLName)
	if err := os.WriteFile(fallback, []byte(embeddedSetupYAML), 0o644); err != nil {
		return "", fmt.Errorf("write embedded setup yaml %q: %w", fallback, err)
	}
	return resolveImageYAMLPath(fallback)
}

func defaultSetupYAMLCandidates() []string {
	candidates := []string{
		filepath.Join("examples", defaultSetupYAMLName),
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "examples", defaultSetupYAMLName),
			filepath.Join(exeDir, "..", "examples", defaultSetupYAMLName),
		)
	}
	return candidates
}

func ensureSetupImage(paths config.Paths, name, absYAML string, rebuild bool) (imageRecord, bool, error) {
	catalog, err := loadImageCatalog(paths.ImagesDB)
	if err != nil {
		return imageRecord{}, false, err
	}
	if rec, ok := catalog.Images[name]; ok && !rebuild {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		statuses, err := listLimaStatuses(ctx)
		cancel()
		if err != nil {
			return imageRecord{}, false, err
		}
		if status, exists := statuses[rec.InstanceName]; exists {
			if !strings.EqualFold(status, "running") {
				if err := runStreamingCommand("limactl", "start", "-y", rec.InstanceName); err != nil {
					return imageRecord{}, false, fmt.Errorf("start existing image instance %q: %w", rec.InstanceName, err)
				}
			}
			return rec, false, nil
		}
	}

	rec, err := buildImage(paths, name, absYAML, rebuild)
	if err != nil {
		return imageRecord{}, false, err
	}
	return rec, true, nil
}

func restartAndWarmDaemon(paths config.Paths, warm bool) error {
	client := daemon.NewClient(paths.SockPath)
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, pingErr := client.Ping(stopCtx)
	cancel()
	if pingErr == nil {
		stopCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		_ = client.Shutdown(stopCtx)
		cancel()
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := daemon.EnsureDaemon(startCtx)
	if err != nil {
		return err
	}
	if !warm {
		return nil
	}

	warmCtx, warmCancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer warmCancel()
	resp, err := client.Run(warmCtx, api.RunRequest{
		Interactive: false,
		Spec: model.RunSpec{
			Command:      []string{"true"},
			Cow:          model.CowOn,
			Network:      model.NetworkNAT,
			StrictBudget: false,
			TimeoutMs:    int64((30 * time.Second).Milliseconds()),
		},
	})
	if err != nil {
		return fmt.Errorf("runtime warmup failed: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("runtime warmup failed: %s", resp.Error)
	}
	if resp.Result.ExitCode != 0 {
		return fmt.Errorf("runtime warmup failed with exit code %d: %s", resp.Result.ExitCode, strings.TrimSpace(resp.Result.Stderr))
	}
	return nil
}
