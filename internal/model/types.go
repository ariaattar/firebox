package model

import "time"

type CowMode string

const (
	CowOn   CowMode = "on"
	CowOff  CowMode = "off"
	CowAuto CowMode = ""
)

type AccessMode string

const (
	AccessRW AccessMode = "rw"
	AccessRO AccessMode = "ro"
)

type NetworkMode string

const (
	NetworkNAT  NetworkMode = "nat"
	NetworkNone NetworkMode = "none"
)

type MountSpec struct {
	HostPath  string     `json:"host_path"`
	GuestPath string     `json:"guest_path"`
	Access    AccessMode `json:"access"`
	Cow       CowMode    `json:"cow,omitempty"`
}

func (m MountSpec) EffectiveCow(global CowMode) CowMode {
	if m.Cow == CowOn || m.Cow == CowOff {
		return m.Cow
	}
	if global == CowOff {
		return CowOff
	}
	return CowOn
}

func (m MountSpec) DirectHostWrite(global CowMode) bool {
	return m.Access == AccessRW && m.EffectiveCow(global) == CowOff
}

type RunSpec struct {
	Command        []string    `json:"command"`
	Env            []string    `json:"env,omitempty"`
	Mounts         []MountSpec `json:"mounts,omitempty"`
	Cow            CowMode     `json:"cow"`
	CowRoot        CowMode     `json:"cow_root,omitempty"`
	Network        NetworkMode `json:"network"`
	Profile        string      `json:"profile,omitempty"`
	Workdir        string      `json:"workdir,omitempty"`
	SessionID      string      `json:"session_id,omitempty"`
	PersistSession bool        `json:"persist_session,omitempty"`
	AllowHostWrite bool        `json:"allow_host_write,omitempty"`
	StrictBudget   bool        `json:"strict_budget"`
	TimeoutMs      int64       `json:"timeout_ms,omitempty"`
}

type ExecResult struct {
	Stdout         string `json:"stdout,omitempty"`
	Stderr         string `json:"stderr,omitempty"`
	ExitCode       int    `json:"exit_code"`
	DurationMs     int64  `json:"duration_ms"`
	BudgetExceeded bool   `json:"budget_exceeded,omitempty"`
}

type SandboxStatus string

const (
	SandboxCreated SandboxStatus = "created"
	SandboxRunning SandboxStatus = "running"
	SandboxStopped SandboxStatus = "stopped"
)

type Sandbox struct {
	ID        string        `json:"id"`
	Profile   string        `json:"profile"`
	Status    SandboxStatus `json:"status"`
	Spec      RunSpec       `json:"spec"`
	CreatedAt time.Time     `json:"created_at"`
	StartedAt *time.Time    `json:"started_at,omitempty"`
	StoppedAt *time.Time    `json:"stopped_at,omitempty"`
	LastExit  *int          `json:"last_exit,omitempty"`
	LastErr   string        `json:"last_err,omitempty"`
}

type DiffOp string

const (
	DiffAdd    DiffOp = "add"
	DiffModify DiffOp = "modify"
	DiffDelete DiffOp = "delete"
)

type SandboxDiffChange struct {
	Op   DiffOp `json:"op"`
	Path string `json:"path"`
}

type SandboxMountDiff struct {
	GuestPath string              `json:"guest_path"`
	HostPath  string              `json:"host_path"`
	Added     int                 `json:"added"`
	Modified  int                 `json:"modified"`
	Deleted   int                 `json:"deleted"`
	Changes   []SandboxDiffChange `json:"changes,omitempty"`
	Truncated bool                `json:"truncated,omitempty"`
}

type SandboxDiffResult struct {
	SandboxID  string             `json:"sandbox_id"`
	Path       string             `json:"path,omitempty"`
	Added      int                `json:"added"`
	Modified   int                `json:"modified"`
	Deleted    int                `json:"deleted"`
	DurationMs int64              `json:"duration_ms"`
	Mounts     []SandboxMountDiff `json:"mounts,omitempty"`
}

type SandboxMountApply struct {
	GuestPath string `json:"guest_path"`
	HostPath  string `json:"host_path"`
	Applied   int    `json:"applied"`
	Deleted   int    `json:"deleted"`
}

type SandboxApplyResult struct {
	SandboxID  string              `json:"sandbox_id"`
	Path       string              `json:"path,omitempty"`
	Applied    int                 `json:"applied"`
	Deleted    int                 `json:"deleted"`
	DurationMs int64               `json:"duration_ms"`
	Mounts     []SandboxMountApply `json:"mounts,omitempty"`
}
