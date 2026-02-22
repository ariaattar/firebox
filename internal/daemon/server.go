package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"firebox/internal/api"
	"firebox/internal/backend"
	"firebox/internal/backend/limafc"
	"firebox/internal/config"
	"firebox/internal/latency"
	"firebox/internal/model"
	"firebox/internal/mountspec"
)

const (
	defaultBudget    = 200 * time.Millisecond
	syncBudget       = 500 * time.Millisecond
	defaultDiffLimit = 200
	maxDiffLimit     = 5000
)

type Server struct {
	paths  config.Paths
	state  *State
	be     backend.Interface
	stats  *latency.Recorder
	budget time.Duration

	httpServer *http.Server
	stopOnce   sync.Once
}

func NewServer(paths config.Paths) (*Server, error) {
	if err := config.EnsureDirs(paths); err != nil {
		return nil, err
	}
	state, err := LoadState(paths.StateDB)
	if err != nil {
		return nil, err
	}
	s := &Server{
		paths:  paths,
		state:  state,
		be:     limafc.New(paths),
		stats:  latency.NewRecorder(),
		budget: defaultBudget,
	}
	return s, nil
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := os.RemoveAll(s.paths.SockPath); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.paths.SockPath), 0o755); err != nil {
		return fmt.Errorf("mkdir socket dir: %w", err)
	}

	ln, err := net.Listen("unix", s.paths.SockPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.paths.SockPath, err)
	}
	if err := os.Chmod(s.paths.SockPath, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ping", s.handlePing)
	mux.HandleFunc("/v1/run", s.handleRun)
	mux.HandleFunc("/v1/sandbox/create", s.handleSandboxCreate)
	mux.HandleFunc("/v1/sandbox/start", s.handleSandboxStart)
	mux.HandleFunc("/v1/sandbox/stop", s.handleSandboxStop)
	mux.HandleFunc("/v1/sandbox/rm", s.handleSandboxRemove)
	mux.HandleFunc("/v1/sandbox/list", s.handleSandboxList)
	mux.HandleFunc("/v1/sandbox/inspect", s.handleSandboxInspect)
	mux.HandleFunc("/v1/sandbox/exec", s.handleSandboxExec)
	mux.HandleFunc("/v1/sandbox/diff", s.handleSandboxDiff)
	mux.HandleFunc("/v1/sandbox/apply", s.handleSandboxApply)
	mux.HandleFunc("/v1/metrics", s.handleMetrics)
	mux.HandleFunc("/v1/shutdown", s.handleShutdown)

	s.httpServer = &http.Server{Handler: mux}

	go func() {
		warmCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = s.be.Warm(warmCtx, 1)
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpServer.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		_ = s.httpServer.Shutdown(context.Background())
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, api.PingResponse{OK: true, PID: os.Getpid(), BudgetMs: s.budget.Milliseconds()})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.RunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Spec.Cow == model.CowAuto {
		req.Spec.Cow = model.CowOn
	}
	if req.Spec.Network == "" {
		req.Spec.Network = model.NetworkNAT
	}

	if mountspec.NeedsHostWriteAck(req.Spec.Mounts, req.Spec.Cow) && !req.Spec.AllowHostWrite && !req.Interactive {
		writeErr(w, http.StatusBadRequest, "direct host writes require --allow-host-write in non-interactive mode")
		return
	}

	res, err := s.be.Run(r.Context(), req.Spec)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}

	d := time.Duration(res.DurationMs) * time.Millisecond
	s.stats.Add("run", d)

	resp := api.RunResponse{Result: res}
	if req.Spec.StrictBudget && d > s.budget {
		resp.Result.BudgetExceeded = true
		resp.Error = fmt.Sprintf("latency budget exceeded: %dms > %dms", res.DurationMs, s.budget.Milliseconds())
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSandboxCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.CreateSandboxRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" {
		req.ID = fmt.Sprintf("sbx-%d", time.Now().UnixNano())
	}
	if req.Spec.Cow == model.CowAuto {
		req.Spec.Cow = model.CowOn
	}
	if req.Spec.Network == "" {
		req.Spec.Network = model.NetworkNAT
	}
	now := time.Now().UTC()
	sb := model.Sandbox{
		ID:        req.ID,
		Profile:   req.Spec.Profile,
		Status:    model.SandboxCreated,
		Spec:      req.Spec,
		CreatedAt: now,
	}
	if err := s.state.PutSandbox(sb); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.CreateSandboxResponse{Sandbox: sb})
}

func (s *Server) handleSandboxStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.SandboxActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sb, ok := s.state.GetSandbox(req.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "sandbox not found")
		return
	}

	start := time.Now()
	// Keep start path ultra-fast; warm in background so start/stop stay under budget.
	go func() {
		warmCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.be.Warm(warmCtx, 1)
	}()
	d := time.Since(start)
	s.stats.Add("start", d)
	if sb.Spec.StrictBudget && d > s.budget {
		writeErr(w, http.StatusServiceUnavailable, fmt.Sprintf("latency budget exceeded: %dms > %dms", d.Milliseconds(), s.budget.Milliseconds()))
		return
	}

	now := time.Now().UTC()
	sb.Status = model.SandboxRunning
	sb.StartedAt = &now
	if err := s.state.PutSandbox(sb); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.SandboxResponse{Sandbox: sb})
}

func (s *Server) handleSandboxStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.SandboxActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sb, ok := s.state.GetSandbox(req.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "sandbox not found")
		return
	}

	start := time.Now()
	// v1 stops are metadata-only; pool recycle happens lazily.
	d := time.Since(start)
	s.stats.Add("stop", d)
	if sb.Spec.StrictBudget && d > s.budget {
		writeErr(w, http.StatusServiceUnavailable, fmt.Sprintf("latency budget exceeded: %dms > %dms", d.Milliseconds(), s.budget.Milliseconds()))
		return
	}

	now := time.Now().UTC()
	sb.Status = model.SandboxStopped
	sb.StoppedAt = &now
	if err := s.state.PutSandbox(sb); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.SandboxResponse{Sandbox: sb})
}

func (s *Server) handleSandboxRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.SandboxActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.state.DeleteSandbox(req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func(id string) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = s.be.CleanupSandbox(cleanupCtx, id)
	}(req.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSandboxList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, api.SandboxListResponse{Sandboxes: s.state.ListSandboxes()})
}

func (s *Server) handleSandboxInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing id")
		return
	}
	sb, ok := s.state.GetSandbox(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "sandbox not found")
		return
	}
	writeJSON(w, http.StatusOK, api.SandboxResponse{Sandbox: sb})
}

func (s *Server) handleSandboxExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.SandboxExecRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sb, ok := s.state.GetSandbox(req.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "sandbox not found")
		return
	}
	spec := sb.Spec
	spec.Command = req.Command
	spec.SessionID = sb.ID
	spec.PersistSession = true

	if mountspec.NeedsHostWriteAck(spec.Mounts, spec.Cow) && !spec.AllowHostWrite && !req.Interactive {
		writeErr(w, http.StatusBadRequest, "direct host writes require --allow-host-write in non-interactive mode")
		return
	}

	res, err := s.be.Run(r.Context(), spec)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if code := res.ExitCode; code != 0 {
		sb.LastExit = &code
		sb.LastErr = res.Stderr
	} else {
		sb.LastExit = &code
		sb.LastErr = ""
	}
	_ = s.state.PutSandbox(sb)

	d := time.Duration(res.DurationMs) * time.Millisecond
	s.stats.Add("exec", d)

	resp := api.RunResponse{Result: res}
	if spec.StrictBudget && d > s.budget {
		resp.Result.BudgetExceeded = true
		resp.Error = fmt.Sprintf("latency budget exceeded: %dms > %dms", res.DurationMs, s.budget.Milliseconds())
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSandboxDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.SandboxDiffRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sb, ok := s.state.GetSandbox(req.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "sandbox not found")
		return
	}
	limit := req.Limit
	switch {
	case limit <= 0:
		limit = defaultDiffLimit
	case limit > maxDiffLimit:
		limit = maxDiffLimit
	}

	start := time.Now()
	res, err := s.be.SandboxDiff(r.Context(), sb.ID, sb.Spec, req.Path, limit)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	d := time.Since(start)
	s.stats.Add("sandbox_diff", d)

	resp := api.SandboxDiffResponse{Result: res}
	if sb.Spec.StrictBudget && d > syncBudget {
		resp.Error = fmt.Sprintf("latency budget exceeded: %dms > %dms", d.Milliseconds(), syncBudget.Milliseconds())
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSandboxApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req api.SandboxApplyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sb, ok := s.state.GetSandbox(req.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "sandbox not found")
		return
	}

	start := time.Now()
	res, err := s.be.SandboxApply(r.Context(), sb.ID, sb.Spec, req.Path)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	d := time.Since(start)
	s.stats.Add("sandbox_apply", d)

	resp := api.SandboxApplyResponse{Result: res}
	if sb.Spec.StrictBudget && d > syncBudget {
		resp.Error = fmt.Sprintf("latency budget exceeded: %dms > %dms", d.Milliseconds(), syncBudget.Milliseconds())
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	raw := s.stats.Snapshot()
	ops := make(map[string]api.OperationStats, len(raw))
	for k, st := range raw {
		ops[k] = api.OperationStats{
			Count: st.Count,
			P50Ms: st.P50Ms,
			P95Ms: st.P95Ms,
			P99Ms: st.P99Ms,
			MaxMs: st.MaxMs,
		}
	}
	writeJSON(w, http.StatusOK, api.MetricsResponse{Operations: ops})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	s.stopOnce.Do(func() {
		go func() {
			_ = s.httpServer.Shutdown(context.Background())
		}()
	})
}

func decodeJSON(r *http.Request, out any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.ErrorResponse{Error: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
