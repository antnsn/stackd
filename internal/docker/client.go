package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types/container"
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

// ListStackContainers returns the names of all containers belonging to the
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
