package server

import (
"database/sql"
"encoding/json"
"errors"
"log/slog"
"net/http"
"time"

gossh "golang.org/x/crypto/ssh"
"stackd/internal/crypto"
"stackd/internal/db"
)

// ---- Repos ----------------------------------------------------------------

type repoRequest struct {
Name         string `json:"name"`
URL          string `json:"url"`
Branch       string `json:"branch"`
Remote       string `json:"remote"`
AuthType     string `json:"authType"`
SSHKeyID     string `json:"sshKeyId"`
PAT          string `json:"pat"`
StacksDir    string `json:"stacksDir"`
SyncInterval int    `json:"syncInterval"`
Enabled      *bool  `json:"enabled"`
}

type repoResponse struct {
ID           string    `json:"id"`
Name         string    `json:"name"`
URL          string    `json:"url"`
Branch       string    `json:"branch"`
Remote       string    `json:"remote"`
AuthType     string    `json:"authType"`
SSHKeyID     string    `json:"sshKeyId,omitempty"`
HasPAT       bool      `json:"hasPat"`
StacksDir    string    `json:"stacksDir"`
SyncInterval int       `json:"syncInterval"`
Enabled      bool      `json:"enabled"`
CreatedAt    time.Time `json:"createdAt"`
UpdatedAt    time.Time `json:"updatedAt"`
}

func repoToResponse(r db.RepoDB) repoResponse {
return repoResponse{
ID:           r.ID,
Name:         r.Name,
URL:          r.URL,
Branch:       r.Branch,
Remote:       r.Remote,
AuthType:     r.AuthType,
SSHKeyID:     r.SSHKeyID,
HasPAT:       r.PATEnc != "",
StacksDir:    r.StacksDir,
SyncInterval: r.SyncInterval,
Enabled:      r.Enabled,
CreatedAt:    r.CreatedAt,
UpdatedAt:    r.UpdatedAt,
}
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
repos, err := db.ListRepos(r.Context(), s.db)
if err != nil {
slog.Error("list repos", "err", err)
jsonError(w, "failed to list repos", http.StatusInternalServerError)
return
}
resp := make([]repoResponse, len(repos))
for i, repo := range repos {
resp[i] = repoToResponse(repo)
}
jsonOK(w, resp)
}

func (s *Server) handleCreateRepo(w http.ResponseWriter, r *http.Request) {
var req repoRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
jsonError(w, "invalid request body", http.StatusBadRequest)
return
}
if req.Name == "" || req.URL == "" {
jsonError(w, "name and url are required", http.StatusBadRequest)
return
}
if req.Branch == "" {
req.Branch = "main"
}
if req.Remote == "" {
req.Remote = "origin"
}
if req.SyncInterval <= 0 {
req.SyncInterval = 60
}
if req.StacksDir == "" {
req.StacksDir = "."
}
enabled := true
if req.Enabled != nil {
enabled = *req.Enabled
}
var patEnc string
if req.PAT != "" {
var err error
patEnc, err = crypto.Encrypt(s.cryptoKey, req.PAT)
if err != nil {
jsonError(w, "failed to encrypt PAT", http.StatusInternalServerError)
return
}
}
repo := db.RepoDB{
Name: req.Name, URL: req.URL, Branch: req.Branch, Remote: req.Remote,
AuthType: req.AuthType, SSHKeyID: req.SSHKeyID, PATEnc: patEnc,
StacksDir: req.StacksDir, SyncInterval: req.SyncInterval, Enabled: enabled,
}
if err := db.CreateRepo(r.Context(), s.db, repo); err != nil {
slog.Error("create repo", "err", err)
jsonError(w, "failed to create repo", http.StatusInternalServerError)
return
}
repos, err := db.ListRepos(r.Context(), s.db)
if err == nil {
for _, rr := range repos {
if rr.Name == req.Name {
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusCreated)
json.NewEncoder(w).Encode(repoToResponse(rr))
return
}
}
}
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusCreated)
json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleGetRepo(w http.ResponseWriter, r *http.Request) {
id := r.PathValue("id")
repo, err := db.GetRepo(r.Context(), s.db, id)
if err != nil {
if errors.Is(err, sql.ErrNoRows) {
jsonError(w, "not found", http.StatusNotFound)
} else {
jsonError(w, "failed to get repo", http.StatusInternalServerError)
}
return
}
jsonOK(w, repoToResponse(repo))
}

func (s *Server) handleUpdateRepo(w http.ResponseWriter, r *http.Request) {
id := r.PathValue("id")
existing, err := db.GetRepo(r.Context(), s.db, id)
if err != nil {
jsonError(w, "not found", http.StatusNotFound)
return
}
var req repoRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
jsonError(w, "invalid request body", http.StatusBadRequest)
return
}
if req.Name != "" {
existing.Name = req.Name
}
if req.URL != "" {
existing.URL = req.URL
}
if req.Branch != "" {
existing.Branch = req.Branch
}
if req.Remote != "" {
existing.Remote = req.Remote
}
if req.AuthType != "" {
existing.AuthType = req.AuthType
}
existing.SSHKeyID = req.SSHKeyID
if req.StacksDir != "" {
existing.StacksDir = req.StacksDir
}
if req.SyncInterval > 0 {
existing.SyncInterval = req.SyncInterval
}
if req.Enabled != nil {
existing.Enabled = *req.Enabled
}
if req.PAT != "" {
patEnc, err := crypto.Encrypt(s.cryptoKey, req.PAT)
if err != nil {
jsonError(w, "failed to encrypt PAT", http.StatusInternalServerError)
return
}
existing.PATEnc = patEnc
}
existing.ID = id
if err := db.UpdateRepo(r.Context(), s.db, existing); err != nil {
jsonError(w, "failed to update repo", http.StatusInternalServerError)
return
}
updated, err := db.GetRepo(r.Context(), s.db, id)
if err != nil {
jsonOK(w, map[string]bool{"ok": true})
return
}
jsonOK(w, repoToResponse(updated))
}

func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
id := r.PathValue("id")
if err := db.DeleteRepo(r.Context(), s.db, id); err != nil {
jsonError(w, "failed to delete repo", http.StatusInternalServerError)
return
}
jsonOK(w, map[string]bool{"ok": true})
}

// ---- SSH Keys -------------------------------------------------------------

type sshKeyRequest struct {
Name       string `json:"name"`
PrivateKey string `json:"privateKey"`
}

type sshKeyResponse struct {
ID        string    `json:"id"`
Name      string    `json:"name"`
PublicKey string    `json:"publicKey"`
CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) handleListSSHKeys(w http.ResponseWriter, r *http.Request) {
keys, err := db.ListSSHKeys(r.Context(), s.db)
if err != nil {
jsonError(w, "failed to list SSH keys", http.StatusInternalServerError)
return
}
resp := make([]sshKeyResponse, len(keys))
for i, k := range keys {
resp[i] = sshKeyResponse{ID: k.ID, Name: k.Name, PublicKey: k.PublicKey, CreatedAt: k.CreatedAt}
}
jsonOK(w, resp)
}

func (s *Server) handleCreateSSHKey(w http.ResponseWriter, r *http.Request) {
var req sshKeyRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
jsonError(w, "invalid request body", http.StatusBadRequest)
return
}
if req.Name == "" || req.PrivateKey == "" {
jsonError(w, "name and privateKey are required", http.StatusBadRequest)
return
}
signer, err := gossh.ParsePrivateKey([]byte(req.PrivateKey))
if err != nil {
jsonError(w, "invalid private key: "+err.Error(), http.StatusBadRequest)
return
}
pubKey := string(gossh.MarshalAuthorizedKey(signer.PublicKey()))
privKeyEnc, err := crypto.Encrypt(s.cryptoKey, req.PrivateKey)
if err != nil {
jsonError(w, "failed to encrypt private key", http.StatusInternalServerError)
return
}
k := db.SSHKeyDB{Name: req.Name, PrivateKeyEnc: privKeyEnc, PublicKey: pubKey}
if err := db.CreateSSHKey(r.Context(), s.db, k); err != nil {
jsonError(w, "failed to create SSH key", http.StatusInternalServerError)
return
}
keys, _ := db.ListSSHKeys(r.Context(), s.db)
for _, kk := range keys {
if kk.Name == req.Name {
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusCreated)
json.NewEncoder(w).Encode(sshKeyResponse{ID: kk.ID, Name: kk.Name, PublicKey: kk.PublicKey, CreatedAt: kk.CreatedAt})
return
}
}
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusCreated)
json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleDeleteSSHKey(w http.ResponseWriter, r *http.Request) {
id := r.PathValue("id")
if err := db.DeleteSSHKey(r.Context(), s.db, id); err != nil {
jsonError(w, "failed to delete SSH key", http.StatusInternalServerError)
return
}
jsonOK(w, map[string]bool{"ok": true})
}

// ---- General Settings -----------------------------------------------------

type generalSettingsResponse struct {
InfisicalTokenSet   bool   `json:"infisicalTokenSet"`
InfisicalProjectID  string `json:"infisicalProjectId"`
InfisicalEnv        string `json:"infisicalEnv"`
InfisicalURL        string `json:"infisicalUrl"`
GitUserName         string `json:"gitUserName"`
GitUserEmail        string `json:"gitUserEmail"`
}

type generalSettingsRequest struct {
InfisicalToken      *string `json:"infisicalToken"`
InfisicalProjectID  *string `json:"infisicalProjectId"`
InfisicalEnv        *string `json:"infisicalEnv"`
InfisicalURL        *string `json:"infisicalUrl"`
GitUserName         *string `json:"gitUserName"`
GitUserEmail        *string `json:"gitUserEmail"`
}

func (s *Server) handleGetGeneralSettings(w http.ResponseWriter, r *http.Request) {
settings, err := db.GetAllSettings(r.Context(), s.db)
if err != nil {
jsonError(w, "failed to get settings", http.StatusInternalServerError)
return
}
tokenRaw, _, _ := db.GetSetting(r.Context(), s.db, "infisical_token")
jsonOK(w, generalSettingsResponse{
InfisicalTokenSet:  tokenRaw != "",
InfisicalProjectID: settings["infisical_project_id"],
InfisicalEnv:       settings["infisical_env"],
InfisicalURL:       settings["infisical_url"],
GitUserName:        settings["git_user_name"],
GitUserEmail:       settings["git_user_email"],
})
}

func (s *Server) handleUpdateGeneralSettings(w http.ResponseWriter, r *http.Request) {
var req generalSettingsRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
jsonError(w, "invalid request body", http.StatusBadRequest)
return
}
type kv struct{ key, value string }
var updates []kv
if req.InfisicalToken != nil && *req.InfisicalToken != "" {
enc, err := crypto.Encrypt(s.cryptoKey, *req.InfisicalToken)
if err != nil {
jsonError(w, "failed to encrypt token", http.StatusInternalServerError)
return
}
updates = append(updates, kv{"infisical_token", enc})
}
if req.InfisicalProjectID != nil {
updates = append(updates, kv{"infisical_project_id", *req.InfisicalProjectID})
}
if req.InfisicalEnv != nil {
updates = append(updates, kv{"infisical_env", *req.InfisicalEnv})
}
if req.InfisicalURL != nil {
updates = append(updates, kv{"infisical_url", *req.InfisicalURL})
}
if req.GitUserName != nil {
updates = append(updates, kv{"git_user_name", *req.GitUserName})
}
if req.GitUserEmail != nil {
updates = append(updates, kv{"git_user_email", *req.GitUserEmail})
}
for _, u := range updates {
if err := db.SetSetting(r.Context(), s.db, u.key, u.value); err != nil {
slog.Error("update setting", "key", u.key, "err", err)
jsonError(w, "failed to update setting: "+u.key, http.StatusInternalServerError)
return
}
}
jsonOK(w, map[string]bool{"ok": true})
}

// ---- helpers --------------------------------------------------------------

func jsonOK(w http.ResponseWriter, v any) {
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(code)
json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
