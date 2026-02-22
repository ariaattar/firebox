package backend

import (
	"context"

	"firebox/internal/model"
)

type Interface interface {
	EnsureHost(ctx context.Context) error
	Warm(ctx context.Context, size int) error
	Run(ctx context.Context, spec model.RunSpec) (model.ExecResult, error)
	SandboxDiff(ctx context.Context, sandboxID string, spec model.RunSpec, guestPath string, limit int) (model.SandboxDiffResult, error)
	SandboxApply(ctx context.Context, sandboxID string, spec model.RunSpec, guestPath string) (model.SandboxApplyResult, error)
	CleanupSandbox(ctx context.Context, sandboxID string) error
}
