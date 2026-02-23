package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"firebox/internal/config"
	"firebox/internal/daemon"

	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	var daemonID string

	cmd := &cobra.Command{Use: "daemon", Short: "Manage firebox daemon"}

	serve := &cobra.Command{
		Use:    "serve",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyDaemonIDEnv(daemonID); err != nil {
				return err
			}
			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			srv, err := daemon.NewServer(paths)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.ListenAndServe(ctx)
		},
	}

	start := &cobra.Command{
		Use:   "start",
		Short: "Start daemon in background",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyDaemonIDEnv(daemonID); err != nil {
				return err
			}
			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(paths); err != nil {
				return err
			}

			client := daemon.NewClient(paths.SockPath)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := client.Ping(ctx); err == nil {
				fmt.Println(styleWarn("fireboxd is already running"))
				return nil
			}

			exe, err := os.Executable()
			if err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Dir(paths.LogFile), 0o755); err != nil {
				return err
			}
			logf, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer logf.Close()

			proc := exec.Command(exe, "daemon", "serve")
			proc.Stdout = logf
			proc.Stderr = logf
			proc.Stdin = nil
			proc.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := proc.Start(); err != nil {
				return err
			}

			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_, err := client.Ping(ctx)
				cancel()
				if err == nil {
					fmt.Println(styleSuccess(fmt.Sprintf("fireboxd started (pid=%d)", proc.Process.Pid)))
					return nil
				}
				time.Sleep(150 * time.Millisecond)
			}
			return fmt.Errorf("daemon did not become ready; check %s", paths.LogFile)
		},
	}

	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyDaemonIDEnv(daemonID); err != nil {
				return err
			}
			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			client := daemon.NewClient(paths.SockPath)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.Shutdown(ctx); err != nil {
				return err
			}
			fmt.Println(styleSuccess("fireboxd stopped"))
			return nil
		},
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyDaemonIDEnv(daemonID); err != nil {
				return err
			}
			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			client := daemon.NewClient(paths.SockPath)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ping, err := client.Ping(ctx)
			if err != nil {
				return fmt.Errorf("fireboxd unavailable: %w", err)
			}
			statusLine := fmt.Sprintf("ok=%t pid=%d budget=%dms", ping.OK, ping.PID, ping.BudgetMs)
			fmt.Println(styleSuccess(statusLine))
			return nil
		},
	}
	cmd.PersistentFlags().StringVar(&daemonID, "id", "", "Daemon namespace id (same as --daemon-id)")

	cmd.AddCommand(serve, start, stop, status)
	return cmd
}
