package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestPatchCarriesCommittedAndUncommittedHunks: the patch matches Resolve's
// change semantics — the committed diff since the base plus working-tree
// edits — so the LLM extractor reads exactly the change the receipt is about.
func TestPatchCarriesCommittedAndUncommittedHunks(t *testing.T) {
	dir := initRepo(t, "main")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.go")
	git("commit", "-q", "-m", "base file")
	git("checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Committed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-am", "committed change")
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\n\nfunc Uncommitted() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "b.go") // staged but uncommitted

	patch, err := Patch(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+func Committed() {}", "+func Uncommitted() {}"} {
		if !strings.Contains(patch, want) {
			t.Errorf("patch missing %q:\n%s", want, patch)
		}
	}
}

// TestResolveIncludesUntrackedButNotHiddenState: a new file not yet `git
// add`ed is part of the change; untracked files under hidden directories are
// tooling state and are not.
func TestResolveIncludesUntrackedButNotHiddenState(t *testing.T) {
	dir := initRepo(t, "main")
	gitrun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitrun("checkout", "-q", "-b", "feature")
	// Tracked territory: pkg/ holds a committed file.
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitrun("add", "pkg/a.go")
	gitrun("commit", "-q", "-m", "tracked package")
	for _, d := range []string{".tooling", "toolcache/deep"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for rel, content := range map[string]string{
		"new_root_test.go":         "package a\n", // root: always tracked territory
		"pkg/new_test.go":          "package a\n", // beside tracked code
		".tooling/state.json":      "{}",          // hidden tooling state
		"toolcache/deep/blob.json": "{}",          // entirely untracked tree
	} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	change, err := Resolve(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"new_root_test.go", "pkg/new_test.go"} {
		if !contains(change.Files, want) {
			t.Errorf("files = %v, want untracked %s included (tracked territory)", change.Files, want)
		}
	}
	for _, reject := range []string{".tooling/state.json", "toolcache/deep/blob.json"} {
		if contains(change.Files, reject) {
			t.Errorf("files = %v — %s is tooling output, not the change", change.Files, reject)
		}
	}
	if change.BaseSHA == "" || change.HeadSHA == "" {
		t.Errorf("SHA pins missing: %+v", change)
	}
}
