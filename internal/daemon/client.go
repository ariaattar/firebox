package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"firebox/internal/api"
	"firebox/internal/config"
)

type Client struct {
	http   *http.Client
	socket string
}

func NewClient(socketPath string) *Client {
	transport := &http.Transport{}
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}
	return &Client{
		http:   &http.Client{Transport: transport, Timeout: 30 * time.Second},
		socket: socketPath,
	}
}

func (c *Client) Ping(ctx context.Context) (api.PingResponse, error) {
	var out api.PingResponse
	err := c.do(ctx, http.MethodGet, "/v1/ping", nil, &out)
	return out, err
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/shutdown", map[string]any{}, nil)
}

func (c *Client) Run(ctx context.Context, req api.RunRequest) (api.RunResponse, error) {
	var out api.RunResponse
	err := c.do(ctx, http.MethodPost, "/v1/run", req, &out)
	return out, err
}

func (c *Client) CreateSandbox(ctx context.Context, req api.CreateSandboxRequest) (api.CreateSandboxResponse, error) {
	var out api.CreateSandboxResponse
	err := c.do(ctx, http.MethodPost, "/v1/sandbox/create", req, &out)
	return out, err
}

func (c *Client) StartSandbox(ctx context.Context, id string) (api.SandboxResponse, error) {
	var out api.SandboxResponse
	err := c.do(ctx, http.MethodPost, "/v1/sandbox/start", api.SandboxActionRequest{ID: id}, &out)
	return out, err
}

func (c *Client) StopSandbox(ctx context.Context, id string) (api.SandboxResponse, error) {
	var out api.SandboxResponse
	err := c.do(ctx, http.MethodPost, "/v1/sandbox/stop", api.SandboxActionRequest{ID: id}, &out)
	return out, err
}

func (c *Client) RemoveSandbox(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/sandbox/rm", api.SandboxActionRequest{ID: id}, nil)
}

func (c *Client) ListSandboxes(ctx context.Context) (api.SandboxListResponse, error) {
	var out api.SandboxListResponse
	err := c.do(ctx, http.MethodGet, "/v1/sandbox/list", nil, &out)
	return out, err
}

func (c *Client) InspectSandbox(ctx context.Context, id string) (api.SandboxResponse, error) {
	var out api.SandboxResponse
	err := c.do(ctx, http.MethodGet, "/v1/sandbox/inspect?id="+id, nil, &out)
	return out, err
}

func (c *Client) ExecSandbox(ctx context.Context, req api.SandboxExecRequest) (api.RunResponse, error) {
	var out api.RunResponse
	err := c.do(ctx, http.MethodPost, "/v1/sandbox/exec", req, &out)
	return out, err
}

func (c *Client) DiffSandbox(ctx context.Context, req api.SandboxDiffRequest) (api.SandboxDiffResponse, error) {
	var out api.SandboxDiffResponse
	err := c.do(ctx, http.MethodPost, "/v1/sandbox/diff", req, &out)
	return out, err
}

func (c *Client) ApplySandbox(ctx context.Context, req api.SandboxApplyRequest) (api.SandboxApplyResponse, error) {
	var out api.SandboxApplyResponse
	err := c.do(ctx, http.MethodPost, "/v1/sandbox/apply", req, &out)
	return out, err
}

func (c *Client) Metrics(ctx context.Context) (api.MetricsResponse, error) {
	var out api.MetricsResponse
	err := c.do(ctx, http.MethodGet, "/v1/metrics", nil, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, reqBody any, out any) error {
	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(payload)
	}
	url := "http://unix" + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var er api.ErrorResponse
		if json.NewDecoder(resp.Body).Decode(&er) == nil && er.Error != "" {
			return errors.New(er.Error)
		}
		return fmt.Errorf("request failed: %s", resp.Status)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func EnsureDaemon(ctx context.Context) (*Client, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return nil, err
	}
	if err := config.EnsureDirs(paths); err != nil {
		return nil, err
	}

	client := NewClient(paths.SockPath)
	if _, err := client.Ping(ctx); err == nil {
		return client, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.LogFile), 0o755); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer logf.Close()

	cmd := exec.Command(exe, "daemon", "serve")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := client.Ping(ctx); err == nil {
			return client, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, fmt.Errorf("daemon did not become ready; check logs at %s", paths.LogFile)
}
