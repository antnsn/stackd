package main

import (
	"strings"
	"testing"
	"time"

	"stackd/internal/db"
)

func TestBackoffLifecycle(t *testing.T) {
	repo := "backoff-test-repo"
	t.Cleanup(func() {
		repoBackoffs.Delete(repo)
	})

	// Fresh repo is not skipped.
	if shouldSkipSync(repo) {
		t.Fatal("fresh repo should not be skipped")
	}

	// One failure sets a future nextAllowed => skip.
	if suspended := recordSyncFailure(repo, time.Minute); suspended {
		t.Fatal("single failure should not suspend")
	}
	if !shouldSkipSync(repo) {
		t.Fatal("repo should be skipped while in backoff")
	}

	// Success clears backoff.
	recordSyncSuccess(repo)
	if shouldSkipSync(repo) {
		t.Fatal("repo should not be skipped after success")
	}
}

func TestBackoffSuspends(t *testing.T) {
	repo := "suspend-test-repo"
	t.Cleanup(func() { repoBackoffs.Delete(repo) })

	var suspended bool
	for i := 0; i < maxSyncFailures; i++ {
		suspended = recordSyncFailure(repo, time.Second)
	}
	if !suspended {
		t.Fatalf("expected suspension after %d failures", maxSyncFailures)
	}
	if !shouldSkipSync(repo) {
		t.Fatal("suspended repo must be skipped")
	}
	resetBackoff(repo)
	if shouldSkipSync(repo) {
		t.Fatal("resetBackoff should clear suspension")
	}
}

func TestScheduling(t *testing.T) {
	repo := "sched-test-repo"
	t.Cleanup(func() { repoNextDue.Delete(repo) })

	// Never-scheduled repo is due.
	if !dueForSync(repo) {
		t.Fatal("unscheduled repo should be due")
	}
	markScheduled(repo, time.Hour)
	if dueForSync(repo) {
		t.Fatal("repo scheduled an hour out should not be due")
	}
	repoNextDue.Store(repo, time.Now().Add(-time.Second))
	if !dueForSync(repo) {
		t.Fatal("repo whose next-due is in the past should be due")
	}
}

func TestEffectiveInterval(t *testing.T) {
	tests := []struct {
		name string
		repo db.RepoDB
		def  time.Duration
		want time.Duration
	}{
		{"repo interval wins", db.RepoDB{SyncInterval: 30}, 90 * time.Second, 30 * time.Second},
		{"default fallback", db.RepoDB{SyncInterval: 0}, 90 * time.Second, 90 * time.Second},
		{"hard fallback", db.RepoDB{SyncInterval: 0}, 0, 60 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveInterval(tt.repo, tt.def); got != tt.want {
				t.Fatalf("effectiveInterval=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestSafeRepoPath(t *testing.T) {
	base := "/var/lib/stackd/repos"
	tests := []struct {
		name    string
		repo    string
		wantErr bool
	}{
		{"simple", "myrepo", false},
		{"traversal", "../etc", true},
		{"dotdot", "..", true},
		{"slashes", "a/b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeRepoPath(base, tt.repo)
			if (err != nil) != tt.wantErr {
				t.Fatalf("safeRepoPath(%q) err=%v wantErr=%v", tt.repo, err, tt.wantErr)
			}
			if err == nil && !strings.HasPrefix(got, base) {
				t.Fatalf("safeRepoPath(%q)=%q escaped base %q", tt.repo, got, base)
			}
		})
	}
}

func TestExtractErrorSummary(t *testing.T) {
	if got := extractErrorSummary("", "fallback"); got != "fallback" {
		t.Fatalf("empty output should return fallback, got %q", got)
	}
	out := "line1\nline2\nline3\nline4\nline5\nline6\nline7"
	got := extractErrorSummary(out, "fb")
	if strings.Contains(got, "line1") {
		t.Fatalf("summary should only keep the last 5 lines, got %q", got)
	}
	if !strings.Contains(got, "line7") {
		t.Fatalf("summary should keep the final line, got %q", got)
	}
}
