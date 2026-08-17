package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a real throwaway git repo with one commit on the named
// branch — DetectBase talks to real git, so its test does too.
func initRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", branch},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// TestDetectBaseFallsBackToLocalDefaultBranch: with no remote and no CI env,
// detection lands on the local main/master branch.
func TestDetectBaseFallsBackToLocalDefaultBranch(t *testing.T) {
	t.Setenv("GITHUB_BASE_REF", "")
	for _, branch := range []string{"main", "master"} {
		dir := initRepo(t, branch)
		got, err := DetectBase(context.Background(), dir)
		if err != nil {
			t.Fatalf("branch %s: %v", branch, err)
		}
		if got != branch {
			t.Errorf("DetectBase = %q, want %q", got, branch)
		}
	}
}

// TestDetectBaseHonorsCIBaseRef: in pull-request CI, $GITHUB_BASE_REF names the
// PR's base branch and wins over local defaults.
func TestDetectBaseHonorsCIBaseRef(t *testing.T) {
	dir := initRepo(t, "main")
	// Create the branch CI names, so the bare-name candidate resolves.
	cmd := exec.Command("git", "branch", "release")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}
	t.Setenv("GITHUB_BASE_REF", "release")
	got, err := DetectBase(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "release" {
		t.Errorf("DetectBase = %q, want release (from GITHUB_BASE_REF)", got)
	}
}

// TestDetectBaseFailsLoudWithNoCandidate: a repo with no recognizable base
// yields an error, not a silent guess.
func TestDetectBaseFailsLoudWithNoCandidate(t *testing.T) {
	t.Setenv("GITHUB_BASE_REF", "")
	dir := initRepo(t, "trunk") // not in the candidate list
	if _, err := DetectBase(context.Background(), dir); err == nil {
		t.Fatal("expected an error when no candidate base resolves")
	}
}

// TestTrackedByPattern: only tracked files matching the patterns come back —
// an untracked document is not part of the definition corpus.
func TestTrackedByPattern(t *testing.T) {
	dir := initRepo(t, "main")
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("spec.md", "# Spec\n")
	write("main.go", "package main\n")
	for _, args := range [][]string{
		{"add", "spec.md", "main.go"},
		{"commit", "-q", "-m", "add files"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write("scratch.md", "# untracked\n")

	got, err := TrackedByPattern(context.Background(), dir, "*.md", "*.markdown")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "spec.md" {
		t.Fatalf("TrackedByPattern = %v, want [spec.md]", got)
	}
}
