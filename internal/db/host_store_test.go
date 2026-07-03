package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// openTestDB opens a fresh migrated sqlite database in a temp dir.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := Open("sqlite://" + path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestResolveHostID(t *testing.T) {
	tests := []struct {
		name     string
		override string
		repo     string
		want     string
	}{
		{"override wins", "hostA", "hostB", "hostA"},
		{"repo when no override", "", "hostB", "hostB"},
		{"local when neither", "", "", LocalHostID},
		{"override over empty repo", "hostA", "", "hostA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveHostID(tt.override, tt.repo); got != tt.want {
				t.Fatalf("ResolveHostID(%q,%q)=%q want %q", tt.override, tt.repo, got, tt.want)
			}
		})
	}
}

func TestLocalHostSeeded(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	h, err := GetHost(ctx, database, LocalHostID)
	if err != nil {
		t.Fatalf("local host should be seeded: %v", err)
	}
	if h.Name != "local" || h.DockerHost != "" {
		t.Fatalf("unexpected local host: %+v", h)
	}
}

func TestHostCRUD(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	created, err := CreateHost(ctx, database, HostDB{
		Name: "remote1", DockerHost: "ssh://deploy@example.com", SSHKeyID: "key1", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated id")
	}

	got, err := GetHost(ctx, database, created.ID)
	if err != nil {
		t.Fatalf("get host: %v", err)
	}
	if got.DockerHost != "ssh://deploy@example.com" || got.SSHKeyID != "key1" {
		t.Fatalf("unexpected host: %+v", got)
	}

	got.Name = "remote1-renamed"
	got.Enabled = false
	if err := UpdateHost(ctx, database, got); err != nil {
		t.Fatalf("update host: %v", err)
	}
	after, _ := GetHost(ctx, database, created.ID)
	if after.Name != "remote1-renamed" || after.Enabled {
		t.Fatalf("update not applied: %+v", after)
	}

	hosts, err := ListHosts(ctx, database)
	if err != nil {
		t.Fatalf("list hosts: %v", err)
	}
	if len(hosts) != 2 { // local + remote1
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}

	if err := DeleteHost(ctx, database, created.ID); err != nil {
		t.Fatalf("delete host: %v", err)
	}
	if _, err := GetHost(ctx, database, created.ID); err == nil {
		t.Fatal("expected host to be deleted")
	}
}

func TestDeleteLocalHostRejected(t *testing.T) {
	database := openTestDB(t)
	err := DeleteHost(context.Background(), database, LocalHostID)
	if !errors.Is(err, ErrLocalHostImmutable) {
		t.Fatalf("expected ErrLocalHostImmutable, got %v", err)
	}
}

func TestDeleteHostInUseRejected(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	h, err := CreateHost(ctx, database, HostDB{Name: "busy", DockerHost: "ssh://u@h", SSHKeyID: "k", Enabled: true})
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	if err := CreateRepo(ctx, database, RepoDB{
		Name: "repo1", URL: "https://example.com/r.git", Branch: "main", Remote: "origin",
		AuthType: "none", StacksDir: ".", HostID: h.ID, Enabled: true,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := DeleteHost(ctx, database, h.ID); !errors.Is(err, ErrHostInUse) {
		t.Fatalf("expected ErrHostInUse, got %v", err)
	}
}

func TestRepoDefaultsToLocalHost(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	if err := CreateRepo(ctx, database, RepoDB{
		Name: "repoLocal", URL: "https://example.com/r.git", Branch: "main", Remote: "origin",
		AuthType: "none", StacksDir: ".", Enabled: true, // HostID intentionally empty
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	r, err := GetRepoByName(ctx, database, "repoLocal")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if r.HostID != LocalHostID {
		t.Fatalf("expected host_id=%q, got %q", LocalHostID, r.HostID)
	}
}

func TestStackOverrides(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	// No override yet.
	if _, ok, err := GetStackOverride(ctx, database, "repoX", "web"); err != nil || ok {
		t.Fatalf("expected no override, ok=%v err=%v", ok, err)
	}

	if err := SetStackOverride(ctx, database, "repoX", "web", "hostA"); err != nil {
		t.Fatalf("set override: %v", err)
	}
	hostID, ok, err := GetStackOverride(ctx, database, "repoX", "web")
	if err != nil || !ok || hostID != "hostA" {
		t.Fatalf("get override = (%q,%v,%v)", hostID, ok, err)
	}

	// Upsert replaces.
	if err := SetStackOverride(ctx, database, "repoX", "web", "hostB"); err != nil {
		t.Fatalf("upsert override: %v", err)
	}
	hostID, _, _ = GetStackOverride(ctx, database, "repoX", "web")
	if hostID != "hostB" {
		t.Fatalf("expected hostB after upsert, got %q", hostID)
	}

	list, err := ListStackOverridesByRepo(ctx, database, "repoX")
	if err != nil || len(list) != 1 {
		t.Fatalf("list overrides = %v err=%v", list, err)
	}

	if err := DeleteStackOverride(ctx, database, "repoX", "web"); err != nil {
		t.Fatalf("delete override: %v", err)
	}
	if _, ok, _ := GetStackOverride(ctx, database, "repoX", "web"); ok {
		t.Fatal("expected override cleared")
	}
}

func TestResolveStackHostIDPrecedence(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	// No override → repo host.
	got, err := ResolveStackHostID(ctx, database, "repoX", "web", "repoHost")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "repoHost" {
		t.Fatalf("expected repoHost, got %q", got)
	}

	// Override present → override wins.
	if err := SetStackOverride(ctx, database, "repoX", "web", "overrideHost"); err != nil {
		t.Fatalf("set override: %v", err)
	}
	got, _ = ResolveStackHostID(ctx, database, "repoX", "web", "repoHost")
	if got != "overrideHost" {
		t.Fatalf("expected overrideHost, got %q", got)
	}

	// No override and empty repo host → local.
	got, _ = ResolveStackHostID(ctx, database, "repoY", "db", "")
	if got != LocalHostID {
		t.Fatalf("expected %q, got %q", LocalHostID, got)
	}
}
