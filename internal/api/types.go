package api

import "firebox/internal/model"

type ErrorResponse struct {
	Error string `json:"error"`
}

type PingResponse struct {
	OK       bool  `json:"ok"`
	PID      int   `json:"pid"`
	BudgetMs int64 `json:"budget_ms"`
}

type RunRequest struct {
	Spec        model.RunSpec `json:"spec"`
	Interactive bool          `json:"interactive"`
}

type RunResponse struct {
	Result model.ExecResult `json:"result"`
	Error  string           `json:"error,omitempty"`
}

type CreateSandboxRequest struct {
	ID   string        `json:"id,omitempty"`
	Spec model.RunSpec `json:"spec"`
}

type CreateSandboxResponse struct {
	Sandbox model.Sandbox `json:"sandbox"`
}

type SandboxActionRequest struct {
	ID string `json:"id"`
}

type SandboxExecRequest struct {
	ID          string   `json:"id"`
	Command     []string `json:"command"`
	Interactive bool     `json:"interactive"`
}

type SandboxDiffRequest struct {
	ID    string `json:"id"`
	Path  string `json:"path,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type SandboxApplyRequest struct {
	ID   string `json:"id"`
	Path string `json:"path,omitempty"`
}

type SandboxResponse struct {
	Sandbox model.Sandbox `json:"sandbox"`
}

type SandboxListResponse struct {
	Sandboxes []model.Sandbox `json:"sandboxes"`
}

type SandboxDiffResponse struct {
	Result model.SandboxDiffResult `json:"result"`
	Error  string                  `json:"error,omitempty"`
}

type SandboxApplyResponse struct {
	Result model.SandboxApplyResult `json:"result"`
	Error  string                   `json:"error,omitempty"`
}

type MetricsResponse struct {
	Operations map[string]OperationStats `json:"operations"`
}

type OperationStats struct {
	Count int     `json:"count"`
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}
