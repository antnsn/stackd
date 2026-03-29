package state

import (
	"encoding/json"
	"sync"
	"time"
)

// ActivityEvent is a single live activity notification.
type ActivityEvent struct {
	Type  string `json:"type"`  // "pulling", "applying", "done", "error"
	Repo  string `json:"repo"`
	Stack string `json:"stack,omitempty"` // empty for repo-level events
	Msg   string `json:"msg"`
}

// ActivityBus fan-outs activity events to all registered SSE subscribers.
type ActivityBus struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func NewActivityBus() *ActivityBus {
	return &ActivityBus{subs: make(map[chan []byte]struct{})}
}

// Publish sends an event to all current subscribers.
func (b *ActivityBus) Publish(ev ActivityEvent) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- data:
		default: // drop if subscriber is slow
		}
	}
}

// Subscribe returns a channel that receives SSE-formatted event bytes.
// Call the returned cancel func to unsubscribe and close the channel.
func (b *ActivityBus) Subscribe() (ch chan []byte, cancel func()) {
	ch = make(chan []byte, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	cancel = func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

type SyncStatus string

const (
	StatusOK      SyncStatus = "ok"
	StatusError   SyncStatus = "error"
	StatusSyncing SyncStatus = "syncing"
)

type ApplyStatus string

const (
	ApplyOK       ApplyStatus = "ok"
	ApplyError    ApplyStatus = "error"
	ApplyApplying ApplyStatus = "applying"
)

type RepoState struct {
	Name      string     `json:"name"`
	LastSync  time.Time  `json:"lastSync"`
	LastSHA   string     `json:"lastSha"`
	Status    SyncStatus `json:"status"`
	LastError string     `json:"lastError,omitempty"`
}

// ContainerDetail mirrors docker.ContainerDetail without creating an import cycle.
type ContainerDetail struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Image     string    `json:"image"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"startedAt"`
	Env       []string  `json:"env"`   // env vars with sensitive values masked
	Ports     []string  `json:"ports"` // ["8080:80/tcp", ...]
}

type StackState struct {
	Name          string            `json:"name"`
	RepoName      string            `json:"repoName"`
	StackDir      string            `json:"stackDir,omitempty"`
	LastApply     time.Time         `json:"lastApply"`
	Status        ApplyStatus       `json:"status"`
	LastOutput    string            `json:"lastOutput,omitempty"`
	LastError     string            `json:"lastError,omitempty"`
	Containers    []ContainerDetail `json:"containers"`
	InfisicalMode string            `json:"infisicalMode,omitempty"` // "": none, "global": global token, "per-stack": infisical.toml
}

type InfisicalState struct {
	Enabled bool   `json:"enabled"`
	Env     string `json:"env"`
}

type FullState struct {
	Repos     []RepoState    `json:"repos"`
	Stacks    []StackState   `json:"stacks"`
	Infisical InfisicalState `json:"infisical"`
}

type Store struct {
	mu        sync.RWMutex
	repos     map[string]*RepoState
	stacks    map[string]*StackState
	infisical InfisicalState
}

func New() *Store {
	return &Store{
		repos:  make(map[string]*RepoState),
		stacks: make(map[string]*StackState),
	}
}

func (s *Store) UpdateRepo(r RepoState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.repos[r.Name]
	if !ok {
		existing = &RepoState{}
		s.repos[r.Name] = existing
	}
	*existing = r
}

func (s *Store) GetRepo(name string) (RepoState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[name]
	if !ok {
		return RepoState{}, false
	}
	return *r, true
}

func (s *Store) GetAllRepos() []RepoState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RepoState, 0, len(s.repos))
	for _, r := range s.repos {
		out = append(out, *r)
	}
	return out
}

func (s *Store) UpdateStack(st StackState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := st.RepoName + "/" + st.Name
	existing, ok := s.stacks[key]
	if !ok {
		existing = &StackState{}
		s.stacks[key] = existing
	}
	*existing = st
}

func (s *Store) GetStack(repoName, name string) (StackState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.stacks[repoName+"/"+name]
	if !ok {
		return StackState{}, false
	}
	return *st, true
}

// UpdateStackContainers atomically replaces the Containers slice for an
// existing stack. It is a no-op if the stack is not yet known to the store.
func (s *Store) UpdateStackContainers(repoName, name string, containers []ContainerDetail) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := repoName + "/" + name
	if existing, ok := s.stacks[key]; ok {
		if containers == nil {
			containers = []ContainerDetail{}
		}
		existing.Containers = containers
	}
}

func (s *Store) GetAllStacks() []StackState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StackState, 0, len(s.stacks))
	for _, st := range s.stacks {
		out = append(out, *st)
	}
	return out
}

func (s *Store) SetInfisical(inf InfisicalState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.infisical = inf
}

func (s *Store) GetAll() FullState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	repos := make([]RepoState, 0, len(s.repos))
	for _, r := range s.repos {
		repos = append(repos, *r)
	}
	stacks := make([]StackState, 0, len(s.stacks))
	for _, st := range s.stacks {
		stacks = append(stacks, *st)
	}
	return FullState{
		Repos:     repos,
		Stacks:    stacks,
		Infisical: s.infisical,
	}
}
