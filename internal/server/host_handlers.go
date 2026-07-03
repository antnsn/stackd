package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"stackd/internal/db"
	"stackd/internal/docker"
)

// ---- Hosts ----------------------------------------------------------------

type hostResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	DockerHost string    `json:"dockerHost"`
	SSHKeyID   string    `json:"sshKeyId,omitempty"`
	Enabled    bool      `json:"enabled"`
	IsLocal    bool      `json:"isLocal"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func hostToResponse(h db.HostDB) hostResponse {
	return hostResponse{
		ID:         h.ID,
		Name:       h.Name,
		DockerHost: h.DockerHost,
		SSHKeyID:   h.SSHKeyID,
		Enabled:    h.Enabled,
		IsLocal:    h.ID == db.LocalHostID || h.DockerHost == "",
		CreatedAt:  h.CreatedAt,
		UpdatedAt:  h.UpdatedAt,
	}
}

// hostRequest uses pointers so PUT can distinguish "field omitted" from "set to
// zero value". For POST, Name and DockerHost are read directly.
type hostRequest struct {
	Name       *string `json:"name"`
	DockerHost *string `json:"dockerHost"`
	SSHKeyID   *string `json:"sshKeyId"`
	Enabled    *bool   `json:"enabled"`
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := db.ListHosts(r.Context(), s.db)
	if err != nil {
		jsonError(w, "failed to list hosts", http.StatusInternalServerError)
		return
	}
	resp := make([]hostResponse, len(hosts))
	for i, h := range hosts {
		resp[i] = hostToResponse(h)
	}
	jsonOK(w, resp)
}

func (s *Server) handleGetHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h, err := db.GetHost(r.Context(), s.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			jsonError(w, "not found", http.StatusNotFound)
		} else {
			jsonError(w, "failed to get host", http.StatusInternalServerError)
		}
		return
	}
	jsonOK(w, hostToResponse(h))
}

// validateSSHKeyExists reports whether an ssh key id refers to an existing key.
func (s *Server) sshKeyExists(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	if _, err := db.GetSSHKey(ctx, s.db, id); err != nil {
		return false
	}
	return true
}

func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	var req hostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == nil || *req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	name := *req.Name
	dockerHost := ""
	if req.DockerHost != nil {
		dockerHost = *req.DockerHost
	}
	if err := docker.ValidateDockerHost(dockerHost); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	sshKeyID := ""
	if req.SSHKeyID != nil {
		sshKeyID = *req.SSHKeyID
	}
	// A remote host must have an ssh key to authenticate the tunnel.
	if dockerHost != "" {
		if sshKeyID == "" {
			jsonError(w, "sshKeyId is required for a remote (ssh://) host", http.StatusBadRequest)
			return
		}
		if !s.sshKeyExists(r.Context(), sshKeyID) {
			jsonError(w, "sshKeyId does not exist", http.StatusBadRequest)
			return
		}
	} else {
		// Local host uses the socket; ignore any provided key.
		sshKeyID = ""
	}
	// Enforce unique name (also protected by the DB constraint, but give a 400).
	existing, err := db.ListHosts(r.Context(), s.db)
	if err != nil {
		jsonError(w, "failed to validate host", http.StatusInternalServerError)
		return
	}
	for _, h := range existing {
		if h.Name == name {
			jsonError(w, "a host with that name already exists", http.StatusBadRequest)
			return
		}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	created, err := db.CreateHost(r.Context(), s.db, db.HostDB{
		Name: name, DockerHost: dockerHost, SSHKeyID: sshKeyID, Enabled: enabled,
	})
	if err != nil {
		jsonError(w, "failed to create host", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(hostToResponse(created))
}

func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := db.GetHost(r.Context(), s.db, id)
	if err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	// The built-in local host is immutable: its docker_host must stay "" so the
	// stable 'local' reference always means "local socket".
	if id == db.LocalHostID {
		jsonError(w, "the built-in local host cannot be modified", http.StatusBadRequest)
		return
	}
	var req hostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name != nil && *req.Name != "" {
		existing.Name = *req.Name
	}
	if req.DockerHost != nil {
		if err := docker.ValidateDockerHost(*req.DockerHost); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		existing.DockerHost = *req.DockerHost
	}
	if req.SSHKeyID != nil {
		existing.SSHKeyID = *req.SSHKeyID
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	// A remote host still needs a usable key after the update.
	if existing.DockerHost != "" {
		if existing.SSHKeyID == "" {
			jsonError(w, "sshKeyId is required for a remote (ssh://) host", http.StatusBadRequest)
			return
		}
		if !s.sshKeyExists(r.Context(), existing.SSHKeyID) {
			jsonError(w, "sshKeyId does not exist", http.StatusBadRequest)
			return
		}
	} else {
		existing.SSHKeyID = ""
	}
	if err := db.UpdateHost(r.Context(), s.db, existing); err != nil {
		jsonError(w, "failed to update host", http.StatusInternalServerError)
		return
	}
	// Drop any cached client so the new connection details take effect.
	s.registry.Invalidate(id)
	updated, err := db.GetHost(r.Context(), s.db, id)
	if err != nil {
		jsonOK(w, map[string]bool{"ok": true})
		return
	}
	jsonOK(w, hostToResponse(updated))
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := db.DeleteHost(r.Context(), s.db, id)
	switch {
	case errors.Is(err, db.ErrLocalHostImmutable):
		jsonError(w, "the built-in local host cannot be deleted", http.StatusBadRequest)
		return
	case errors.Is(err, db.ErrHostInUse):
		jsonError(w, "host is still referenced by one or more repos", http.StatusBadRequest)
		return
	case err != nil:
		jsonError(w, "failed to delete host", http.StatusInternalServerError)
		return
	}
	s.registry.Invalidate(id)
	jsonOK(w, map[string]bool{"ok": true})
}

// handleTestHost attempts a short-lived Ping/version over the host's transport.
// POST /api/settings/hosts/{id}/test
func (s *Server) handleTestHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cli, _, err := s.resolver.ClientForHostID(ctx, id)
	if err != nil || cli == nil {
		msg := "failed to build client"
		if err != nil {
			msg = err.Error()
		}
		jsonOK(w, map[string]any{"ok": false, "error": msg})
		return
	}
	version, perr := cli.Ping(ctx)
	if perr != nil {
		jsonOK(w, map[string]any{"ok": false, "error": perr.Error()})
		return
	}
	jsonOK(w, map[string]any{"ok": true, "serverVersion": version})
}

// ---- Stack host override --------------------------------------------------

type stackHostResponse struct {
	HostID         string `json:"effectiveHostId"`   // effective host id
	HostName       string `json:"effectiveHostName"` // effective host name
	OverrideHostID string `json:"overrideHostId"`    // "" when the stack inherits the repo host
	RepoHostID     string `json:"repoHostId"`        // the repo's own host id
}

type stackHostRequest struct {
	HostID string `json:"hostId"` // "" clears the override; else pins the stack to that host
}

// handleGetStackHost returns the effective and overridden host for a stack.
// GET /api/stacks/{repo}/{stack}/host
func (s *Server) handleGetStackHost(w http.ResponseWriter, r *http.Request) {
	repoName := r.PathValue("repo")
	stackName := r.PathValue("stack")
	repo, err := db.GetRepoByName(r.Context(), s.db, repoName)
	if err != nil {
		jsonError(w, "repo not found", http.StatusNotFound)
		return
	}
	override, ok, err := db.GetStackOverride(r.Context(), s.db, repo.ID, stackName)
	if err != nil {
		jsonError(w, "failed to load override", http.StatusInternalServerError)
		return
	}
	if !ok {
		override = ""
	}
	effectiveID := db.ResolveHostID(override, repo.HostID)
	info, err := s.resolver.LoadHost(r.Context(), effectiveID)
	if err != nil {
		jsonError(w, "failed to load host", http.StatusInternalServerError)
		return
	}
	jsonOK(w, stackHostResponse{
		HostID:         info.ID,
		HostName:       info.Name,
		OverrideHostID: override,
		RepoHostID:     repo.HostID,
	})
}

// handlePutStackHost sets or clears a stack's host override.
// PUT /api/stacks/{repo}/{stack}/host  body {hostId}
func (s *Server) handlePutStackHost(w http.ResponseWriter, r *http.Request) {
	repoName := r.PathValue("repo")
	stackName := r.PathValue("stack")
	repo, err := db.GetRepoByName(r.Context(), s.db, repoName)
	if err != nil {
		jsonError(w, "repo not found", http.StatusNotFound)
		return
	}
	var req stackHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.HostID == "" {
		// Clear the override so the stack inherits its repo's host.
		if err := db.DeleteStackOverride(r.Context(), s.db, repo.ID, stackName); err != nil {
			jsonError(w, "failed to clear override", http.StatusInternalServerError)
			return
		}
	} else {
		if _, err := db.GetHost(r.Context(), s.db, req.HostID); err != nil {
			jsonError(w, "host does not exist", http.StatusBadRequest)
			return
		}
		if err := db.SetStackOverride(r.Context(), s.db, repo.ID, stackName, req.HostID); err != nil {
			jsonError(w, "failed to set override", http.StatusInternalServerError)
			return
		}
	}
	// Reflect the new host on the in-memory stack state immediately so the next
	// /api/status badge and any log/exec routing use the right host.
	effectiveID := db.ResolveHostID(req.HostID, repo.HostID)
	if info, lerr := s.resolver.LoadHost(r.Context(), effectiveID); lerr == nil {
		if st, ok := s.store.GetStack(repoName, stackName); ok {
			st.HostID = info.ID
			st.HostName = info.Name
			s.store.UpdateStack(st)
		}
	}
	s.handleGetStackHost(w, r)
}
