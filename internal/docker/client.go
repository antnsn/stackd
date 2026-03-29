package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// ContainerDetail holds runtime information about a single container.
type ContainerDetail struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Image     string    `json:"image"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"startedAt"`
	Env       []string  `json:"env"`   // env vars with sensitive values masked
	Ports     []string  `json:"ports"` // ["8080:80/tcp", ...]
}

type ContainerStatus string

const (
	StatusRunning  ContainerStatus = "running"
	StatusStopped  ContainerStatus = "stopped"
	StatusNotFound ContainerStatus = "not_found"
)

type Client struct {
	cli *client.Client
}

func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

func (c *Client) Close() error {
	return c.cli.Close()
}

// GetContainerStatus returns the running state of a container by name.
func (c *Client) GetContainerStatus(ctx context.Context, name string) (ContainerStatus, error) {
	ctrs, err := c.cli.ContainerList(ctx, dockertypes.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", name)),
	})
	if err != nil {
		return StatusNotFound, fmt.Errorf("list containers: %w", err)
	}
	for _, ct := range ctrs {
		for _, n := range ct.Names {
			if n == "/"+name || n == name {
				if ct.State == "running" {
					return StatusRunning, nil
				}
				return StatusStopped, nil
			}
		}
	}
	return StatusNotFound, nil
}

// StreamLogs streams container logs to w until ctx is cancelled or the container stops.
// Handles Docker's multiplexed stdout/stderr framing via stdcopy.
func (c *Client) StreamLogs(ctx context.Context, name string, w io.Writer) error {
	rc, err := c.cli.ContainerLogs(ctx, name, dockertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       "100",
		Timestamps: false,
	})
	if err != nil {
		return fmt.Errorf("container logs %q: %w", name, err)
	}
	defer rc.Close()

	// stdcopy demultiplexes the Docker stream format; both streams go to w.
	_, err = stdcopy.StdCopy(w, w, rc)
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("read logs: %w", err)
	}
	return nil
}

// ListStackContainerDetails returns runtime details for all containers belonging
// to the compose project whose directory is stackDir. It calls ContainerInspect
// per container to populate StartedAt. Context cancellation is respected.
func (c *Client) ListStackContainerDetails(ctx context.Context, stackDir string) ([]ContainerDetail, error) {
	parts := strings.Split(strings.TrimRight(stackDir, "/"), "/")
	projectName := strings.ToLower(parts[len(parts)-1])

	ctrs, err := c.cli.ContainerList(ctx, dockertypes.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", "com.docker.compose.project="+projectName)),
	})
	if err != nil {
		return nil, fmt.Errorf("list stack containers: %w", err)
	}

	details := make([]ContainerDetail, 0, len(ctrs))
	for _, ct := range ctrs {
		if ctx.Err() != nil {
			return details, ctx.Err()
		}
		name := ""
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		d := ContainerDetail{
			ID:     ct.ID,
			Name:   name,
			Image:  ct.Image,
			Status: ct.State,
		}
		if len(d.ID) > 12 {
			d.ID = d.ID[:12]
		}
		info, err := c.cli.ContainerInspect(ctx, ct.ID)
		if err == nil {
			if info.State != nil {
				d.StartedAt, _ = time.Parse(time.RFC3339Nano, info.State.StartedAt)
			}
			if info.Config != nil {
				d.Env = maskEnvVars(info.Config.Env)
			}
			if info.NetworkSettings != nil {
				d.Ports = formatPorts(info.NetworkSettings.Ports)
			}
		}
		details = append(details, d)
	}
	return details, nil
}

func maskEnvVars(envs []string) []string {
	sensitive := []string{"TOKEN", "SECRET", "KEY", "PASSWORD", "PASS", "CREDENTIAL"}
	result := make([]string, 0, len(envs))
	for _, e := range envs {
		eq := strings.Index(e, "=")
		if eq >= 0 {
			upper := strings.ToUpper(e[:eq])
			masked := false
			for _, s := range sensitive {
				if strings.Contains(upper, s) {
					result = append(result, e[:eq]+"=[redacted]")
					masked = true
					break
				}
			}
			if !masked {
				result = append(result, e)
			}
		} else {
			result = append(result, e)
		}
	}
	return result
}

func formatPorts(portMap nat.PortMap) []string {
	var ports []string
	for port, bindings := range portMap {
		for _, b := range bindings {
			if b.HostPort != "" {
				ports = append(ports, b.HostPort+":"+port.Port()+"/"+port.Proto())
			}
		}
		if len(bindings) == 0 {
			ports = append(ports, port.Port()+"/"+port.Proto())
		}
	}
	return ports
}

// StartContainer starts a stopped container by name.
func (c *Client) StartContainer(ctx context.Context, name string) error {
	if err := c.cli.ContainerStart(ctx, name, dockertypes.StartOptions{}); err != nil {
		return fmt.Errorf("StartContainer %s: %w", name, err)
	}
	return nil
}

// StopContainer stops a running container by name.
func (c *Client) StopContainer(ctx context.Context, name string) error {
	if err := c.cli.ContainerStop(ctx, name, dockertypes.StopOptions{}); err != nil {
		return fmt.Errorf("StopContainer %s: %w", name, err)
	}
	return nil
}

// RestartContainer restarts a container by name.
func (c *Client) RestartContainer(ctx context.Context, name string) error {
	if err := c.cli.ContainerRestart(ctx, name, dockertypes.StopOptions{}); err != nil {
		return fmt.Errorf("RestartContainer %s: %w", name, err)
	}
	return nil
}

// ExecResult holds the exec ID and attached streams for an interactive exec session.
type ExecResult struct {
	ExecID string
	types.HijackedResponse
}

// ExecInteractive creates a PTY exec session in the container, probing shells
// in order (bash → sh → ash) and using the first available one.
func (c *Client) ExecInteractive(ctx context.Context, containerID string) (*ExecResult, error) {
	shell, err := c.detectShell(ctx, containerID)
	if err != nil {
		return nil, err
	}

	exec, err := c.cli.ContainerExecCreate(ctx, containerID, dockertypes.ExecOptions{
		Cmd:          []string{shell},
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create %s: %w", containerID, err)
	}

	resp, err := c.cli.ContainerExecAttach(ctx, exec.ID, dockertypes.ExecStartOptions{Tty: true})
	if err != nil {
		return nil, fmt.Errorf("exec attach %s: %w", containerID, err)
	}
	return &ExecResult{ExecID: exec.ID, HijackedResponse: resp}, nil
}

// detectShell probes candidate shells in order and returns the first available.
func (c *Client) detectShell(ctx context.Context, containerID string) (string, error) {
	candidates := []string{"/bin/bash", "/bin/sh", "/ash", "/busybox/sh"}
	for _, shell := range candidates {
		probe, err := c.cli.ContainerExecCreate(ctx, containerID, dockertypes.ExecOptions{
			Cmd: []string{shell, "-c", "exit 0"},
		})
		if err != nil || probe.ID == "" {
			continue
		}
		if err := c.cli.ContainerExecStart(ctx, probe.ID, dockertypes.ExecStartOptions{}); err != nil {
			continue
		}
		insp, err := c.cli.ContainerExecInspect(ctx, probe.ID)
		if err != nil || insp.ExitCode != 0 {
			continue
		}
		return shell, nil
	}
	return "", fmt.Errorf("no usable shell found in container %s", containerID)
}

// ExecResize resizes the PTY for a running exec session.
func (c *Client) ExecResize(ctx context.Context, execID string, height, width uint) error {
	return c.cli.ContainerExecResize(ctx, execID, dockertypes.ResizeOptions{
		Height: height,
		Width:  width,
	})
}
// compose project whose directory is stackDir (matched by project label).
func (c *Client) ListStackContainers(ctx context.Context, stackDir string) ([]string, error) {
	// Compose derives the project name from the directory name, lowercased.
	parts := strings.Split(strings.TrimRight(stackDir, "/"), "/")
	projectName := strings.ToLower(parts[len(parts)-1])

	ctrs, err := c.cli.ContainerList(ctx, dockertypes.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", "com.docker.compose.project="+projectName)),
	})
	if err != nil {
		return nil, fmt.Errorf("list stack containers: %w", err)
	}
	names := make([]string, 0, len(ctrs))
	for _, ct := range ctrs {
		if len(ct.Names) > 0 {
			names = append(names, strings.TrimPrefix(ct.Names[0], "/"))
		}
	}
	return names, nil
}
