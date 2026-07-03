package docker

import (
	"reflect"
	"testing"
)

func TestValidateDockerHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{"empty is local", "", false},
		{"ssh host", "ssh://user@host", false},
		{"ssh host with port", "ssh://user@host:2222", false},
		{"ssh host no user", "ssh://host", false},
		{"ssh host ip", "ssh://deploy@10.0.0.5:22", false},
		{"tcp rejected", "tcp://host:2375", true},
		{"http rejected", "http://host:2375", true},
		{"unix rejected", "unix:///var/run/docker.sock", true},
		{"junk rejected", "not-a-url", true},
		{"ssh with path rejected", "ssh://user@host/extra", true},
		{"ssh empty host rejected", "ssh://", true},
		{"leading-dash host rejected", "ssh://user@-oProxyCommand=evil", true},
		{"leading-dash user rejected", "ssh://-oProxy@host", true},
		{"encoded newline in user rejected", "ssh://u%0aProxyCommand=evil@host", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDockerHost(tt.host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateDockerHost(%q) err=%v, wantErr=%v", tt.host, err, tt.wantErr)
			}
		})
	}
}

func TestParseSSHHost(t *testing.T) {
	tests := []struct {
		name                   string
		host                   string
		wantUser, wantH, wantP string
		wantErr                bool
	}{
		{"full", "ssh://deploy@example.com:2222", "deploy", "example.com", "2222", false},
		{"no port", "ssh://deploy@example.com", "deploy", "example.com", "", false},
		{"no user", "ssh://example.com:22", "", "example.com", "22", false},
		{"ip", "ssh://root@192.168.1.10", "root", "192.168.1.10", "", false},
		{"not ssh", "tcp://host", "", "", "", true},
		{"empty host", "ssh://", "", "", "", true},
		{"dash host", "ssh://user@-bad", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, host, port, err := parseSSHHost(tt.host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSSHHost(%q) err=%v wantErr=%v", tt.host, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if user != tt.wantUser || host != tt.wantH || port != tt.wantP {
				t.Fatalf("parseSSHHost(%q) = (%q,%q,%q), want (%q,%q,%q)",
					tt.host, user, host, port, tt.wantUser, tt.wantH, tt.wantP)
			}
		})
	}
}

func TestValidateTransport(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty defaults to forward", "", TransportForward, false},
		{"forward", "forward", TransportForward, false},
		{"dial-stdio", "dial-stdio", TransportDialStdio, false},
		{"unknown rejected", "tcp", "", true},
		{"case-sensitive reject", "Forward", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateTransport(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTransport(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("ValidateTransport(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateRemoteSocket(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty defaults", "", DefaultRemoteSocket, false},
		{"absolute default", "/var/run/docker.sock", "/var/run/docker.sock", false},
		{"absolute rootless", "/run/user/1000/docker.sock", "/run/user/1000/docker.sock", false},
		{"relative rejected", "var/run/docker.sock", "", true},
		{"space rejected", "/var/run/docker sock.sock", "", true},
		{"tab control char rejected", "/var/run/\tdocker.sock", "", true},
		{"newline control char rejected", "/var/run/docker.sock\n", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateRemoteSocket(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRemoteSocket(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("ValidateRemoteSocket(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildSSHArgs(t *testing.T) {
	tests := []struct {
		name                                             string
		user, host, port, keyPath, knownHost, dockerPath string
		want                                             []string
	}{
		{
			name: "full remote default docker path",
			user: "deploy", host: "example.com", port: "2222",
			keyPath: "/keys/host-abc.key", knownHost: "/ssh/known_hosts",
			want: []string{
				"-i", "/keys/host-abc.key",
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "UserKnownHostsFile=/ssh/known_hosts",
				"-o", "BatchMode=yes",
				"-o", "ConnectTimeout=10",
				"-p", "2222",
				"--", "deploy@example.com", "docker", "system", "dial-stdio",
			},
		},
		{
			name: "no user no port no key",
			host: "example.com", knownHost: "/ssh/known_hosts",
			want: []string{
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "UserKnownHostsFile=/ssh/known_hosts",
				"-o", "BatchMode=yes",
				"-o", "ConnectTimeout=10",
				"--", "example.com", "docker", "system", "dial-stdio",
			},
		},
		{
			name: "custom absolute docker path (Synology)",
			user: "plecto", host: "madara.home",
			dockerPath: "/usr/local/bin/docker",
			want: []string{
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "BatchMode=yes",
				"-o", "ConnectTimeout=10",
				"--", "plecto@madara.home", "/usr/local/bin/docker", "system", "dial-stdio",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSSHArgs(tt.user, tt.host, tt.port, tt.keyPath, tt.knownHost, tt.dockerPath)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("buildSSHArgs = %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestValidateDockerPath(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty defaults to docker", "", DefaultDockerPath, false},
		{"bare docker", "docker", "docker", false},
		{"absolute path", "/usr/local/bin/docker", "/usr/local/bin/docker", false},
		{"leading dash rejected", "-x", "", true},
		{"space rejected", "/usr/local/bin/docker foo", "", true},
		{"control char rejected", "docker\n", "", true},
		{"semicolon injection rejected", "/usr/local/bin/docker;id", "", true},
		{"redirect injection rejected", "docker>/tmp/x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateDockerPath(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateDockerPath(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("ValidateDockerPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
