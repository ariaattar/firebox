package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"firebox/internal/config"
	"firebox/internal/daemon"

	"github.com/spf13/cobra"
)

var imageNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var limaImagesKeyRe = regexp.MustCompile(`(?m)^\s*images\s*:`)

type imageCatalog struct {
	Images map[string]imageRecord `json:"images"`
}

type imageRecord struct {
	Name         string    `json:"name"`
	InstanceName string    `json:"instance_name"`
	YAMLFile     string    `json:"yaml_file"`
	BuiltAt      time.Time `json:"built_at"`
}

type limaListEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func newImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage persistent firebox host images",
	}
	cmd.AddCommand(newImageBuildCmd())
	cmd.AddCommand(newImageUseCmd())
	cmd.AddCommand(newImageListCmd())
	return cmd
}

func newImageBuildCmd() *cobra.Command {
	var (
		name    string
		yaml    string
		use     bool
		rebuild bool
	)

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a persistent image from a Lima YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateImageName(name); err != nil {
				return err
			}
			absYAML, err := resolveImageYAMLPath(yaml)
			if err != nil {
				return err
			}
			if err := ensureLimaInstalled(false); err != nil {
				return err
			}

			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(paths); err != nil {
				return err
			}

			fmt.Printf("Building image %s from %s\n", name, absYAML)
			rec, err := buildImage(paths, name, absYAML, rebuild)
			if err != nil {
				return err
			}

			if use {
				if err := setActiveImage(paths, rec); err != nil {
					return err
				}
				fmt.Printf("Active runtime image set to %s (%s)\n", rec.Name, rec.InstanceName)
				if running, err := daemonRunning(paths.SockPath); err == nil && running {
					fmt.Println("fireboxd is running; restart daemon to apply the new image")
				}
			}

			fmt.Printf("Image ready: %s (instance=%s)\n", rec.Name, rec.InstanceName)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Image name")
	cmd.Flags().StringVarP(&yaml, "file", "f", "", "Lima YAML file path")
	cmd.Flags().BoolVar(&use, "use", true, "Set this image as active runtime")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "Delete and rebuild if image already exists")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newImageUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Select an image as the active runtime",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(paths); err != nil {
				return err
			}
			catalog, err := loadImageCatalog(paths.ImagesDB)
			if err != nil {
				return err
			}
			rec, ok := catalog.Images[name]
			if !ok {
				return fmt.Errorf("image %q not found", name)
			}
			if err := setActiveImage(paths, rec); err != nil {
				return err
			}
			fmt.Printf("Active runtime image set to %s (%s)\n", rec.Name, rec.InstanceName)
			if running, err := daemonRunning(paths.SockPath); err == nil && running {
				fmt.Println("fireboxd is running; restart daemon to apply the new image")
			}
			return nil
		},
	}
}

func newImageListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List built images",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			catalog, err := loadImageCatalog(paths.ImagesDB)
			if err != nil {
				return err
			}
			if len(catalog.Images) == 0 {
				fmt.Println("no images")
				return nil
			}

			statuses := map[string]string{}
			if _, err := exec.LookPath("limactl"); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if out, err := listLimaStatuses(ctx); err == nil {
					statuses = out
				}
			}

			runtimeCfg, _ := config.LoadRuntimeConfig(paths.Runtime)
			activeInstance := runtimeCfg.EffectiveInstanceName()

			names := make([]string, 0, len(catalog.Images))
			for name := range catalog.Images {
				names = append(names, name)
			}
			sort.Strings(names)

			fmt.Println("NAME\tINSTANCE\tSTATUS\tACTIVE\tBUILT_AT\tYAML")
			for _, name := range names {
				rec := catalog.Images[name]
				status := statuses[rec.InstanceName]
				if status == "" {
					status = "unknown"
				}
				active := "no"
				if rec.InstanceName == activeInstance {
					active = "yes"
				}
				fmt.Printf(
					"%s\t%s\t%s\t%s\t%s\t%s\n",
					rec.Name,
					rec.InstanceName,
					status,
					active,
					rec.BuiltAt.Format(time.RFC3339),
					rec.YAMLFile,
				)
			}
			return nil
		},
	}
}

func imageInstanceName(name string) string {
	var sb strings.Builder
	sb.WriteString("firebox-img-")
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(r + ('a' - 'A'))
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r)
		case r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-', r == '_':
			sb.WriteRune(r)
		case r == '.':
			sb.WriteByte('-')
		default:
			sb.WriteByte('-')
		}
	}
	return sb.String()
}

func validateImageName(name string) error {
	if !imageNameRe.MatchString(name) {
		return fmt.Errorf("invalid image name %q: use [A-Za-z0-9._-], max 64 chars", name)
	}
	return nil
}

func resolveImageYAMLPath(yaml string) (string, error) {
	if strings.TrimSpace(yaml) == "" {
		return "", errors.New("missing --file path")
	}
	absYAML, err := filepath.Abs(yaml)
	if err != nil {
		return "", fmt.Errorf("resolve yaml path: %w", err)
	}
	if _, err := os.Stat(absYAML); err != nil {
		return "", fmt.Errorf("yaml file %q: %w", absYAML, err)
	}
	yamlData, err := os.ReadFile(absYAML)
	if err != nil {
		return "", fmt.Errorf("read yaml file %q: %w", absYAML, err)
	}
	if !limaImagesKeyRe.Match(yamlData) {
		return "", fmt.Errorf("yaml file %q must define an images: section", absYAML)
	}
	return absYAML, nil
}

func buildImage(paths config.Paths, name, absYAML string, rebuild bool) (imageRecord, error) {
	instance := imageInstanceName(name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	statuses, err := listLimaStatuses(ctx)
	cancel()
	if err != nil {
		return imageRecord{}, err
	}
	if _, exists := statuses[instance]; exists {
		if !rebuild {
			return imageRecord{}, fmt.Errorf("image %q already exists (instance %q). Use --rebuild to replace it", name, instance)
		}
		fmt.Printf("Rebuilding image %s (deleting %s)\n", name, instance)
		if err := runStreamingCommand("limactl", "delete", "-f", instance); err != nil {
			return imageRecord{}, fmt.Errorf("delete existing instance %q: %w", instance, err)
		}
	}
	if err := runStreamingCommand("limactl", "start", "-y", "--name", instance, absYAML); err != nil {
		return imageRecord{}, fmt.Errorf("build image %q: %w", name, err)
	}

	catalog, err := loadImageCatalog(paths.ImagesDB)
	if err != nil {
		return imageRecord{}, err
	}
	rec := imageRecord{
		Name:         name,
		InstanceName: instance,
		YAMLFile:     absYAML,
		BuiltAt:      time.Now().UTC(),
	}
	catalog.Images[name] = rec
	if err := saveImageCatalog(paths.ImagesDB, catalog); err != nil {
		return imageRecord{}, err
	}
	return rec, nil
}

func setActiveImage(paths config.Paths, rec imageRecord) error {
	cfg, err := config.LoadRuntimeConfig(paths.Runtime)
	if err != nil {
		return err
	}
	cfg.InstanceName = rec.InstanceName
	cfg.ImageName = rec.Name
	return config.SaveRuntimeConfig(paths.Runtime, cfg)
}

func loadImageCatalog(path string) (imageCatalog, error) {
	db := imageCatalog{Images: map[string]imageRecord{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return db, nil
		}
		return db, fmt.Errorf("read images db: %w", err)
	}
	if len(data) == 0 {
		return db, nil
	}
	if err := json.Unmarshal(data, &db); err != nil {
		return db, fmt.Errorf("decode images db: %w", err)
	}
	if db.Images == nil {
		db.Images = map[string]imageRecord{}
	}
	return db, nil
}

func saveImageCatalog(path string, db imageCatalog) error {
	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return fmt.Errorf("encode images db: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write images db: %w", err)
	}
	return nil
}

func listLimaStatuses(ctx context.Context) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "limactl", "list", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("limactl list --json: %w", err)
	}
	status := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ent limaListEntry
		if err := json.Unmarshal([]byte(line), &ent); err != nil {
			continue
		}
		if ent.Name != "" {
			status[ent.Name] = ent.Status
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan limactl list output: %w", err)
	}
	return status, nil
}

func daemonRunning(sockPath string) (bool, error) {
	client := daemon.NewClient(sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	_, err := client.Ping(ctx)
	if err == nil {
		return true, nil
	}
	return false, nil
}

func ensureLimaInstalled(autoInstall bool) error {
	if _, err := exec.LookPath("limactl"); err == nil {
		return nil
	}
	if !autoInstall {
		return fmt.Errorf("limactl not found: install lima (e.g. brew install lima)")
	}
	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("limactl not found and Homebrew is unavailable: install lima manually")
	}
	fmt.Println("Installing lima via Homebrew...")
	if err := runStreamingCommand("brew", "install", "lima"); err != nil {
		return fmt.Errorf("install lima via brew: %w", err)
	}
	if _, err := exec.LookPath("limactl"); err != nil {
		return fmt.Errorf("limactl not found after brew install")
	}
	return nil
}

func runStreamingCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
