// Package gitdiff resolves the set of files a change touches, relative to a base
// ref. This is the substrate correctful harvests claims from.
package gitdiff

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Change is the resolved file set of a diff.
type Change struct {
	Repo    string
	BaseRef string
	HeadRef string
	Files   []string
}

// Resolve returns the files changed between baseRef and the working tree head in
// the git repository rooted at dir. It uses `git diff --name-only baseRef...HEAD`
// (three-dot: files changed on HEAD's side since the merge base), which is the
// set a merge would introduce — the correct scope for a per-change receipt.
func Resolve(ctx context.Context, dir, baseRef string) (Change, error) {
	head, err := run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return Change{}, err
	}
	repo, _ := run(ctx, dir, "rev-parse", "--show-toplevel")

	out, err := run(ctx, dir, "diff", "--name-only", baseRef+"...HEAD")
	if err != nil {
		return Change{}, err
	}
	files := nonEmptyLines(out)

	// Include not-yet-committed changes too, so a receipt can be run mid-branch
	// before the work is committed. Union with the committed diff.
	unstaged, _ := run(ctx, dir, "diff", "--name-only", "HEAD")
	for _, f := range nonEmptyLines(unstaged) {
		if !contains(files, f) {
			files = append(files, f)
		}
	}

	return Change{
		Repo:    strings.TrimSpace(repo),
		BaseRef: baseRef,
		HeadRef: strings.TrimSpace(head),
		Files:   files,
	}, nil
}

// DetectBase resolves the base ref to diff against when the caller asked for
// automatic detection, trying in order:
//
//  1. $GITHUB_BASE_REF (set in pull-request CI) as origin/<branch>, then bare;
//  2. the remote's default branch via origin/HEAD;
//  3. origin/main, origin/master, main, master.
//
// The first candidate that resolves to a commit wins. The chosen ref lands in
// the receipt's change header, so the resolution is always disclosed, never
// silent.
func DetectBase(ctx context.Context, dir string) (string, error) {
	var candidates []string
	if b := os.Getenv("GITHUB_BASE_REF"); b != "" {
		candidates = append(candidates, "origin/"+b, b)
	}
	if head, err := run(ctx, dir, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(head); ref != "" {
			candidates = append(candidates, strings.TrimPrefix(ref, "refs/remotes/"))
		}
	}
	candidates = append(candidates, "origin/main", "origin/master", "main", "master")

	for _, c := range candidates {
		if _, err := run(ctx, dir, "rev-parse", "--verify", "--quiet", c+"^{commit}"); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no base ref found (tried GITHUB_BASE_REF, origin/HEAD, main, master)")
}

// ResolveAll treats the entire working tree as the change: every tracked file
// plus every untracked-not-ignored file. This backs the sweep organ (the
// accumulated-state pass) and lets a receipt run on a fresh repo that has no
// base ref to diff against yet.
func ResolveAll(ctx context.Context, dir string) (Change, error) {
	repo, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Change{}, err
	}
	head, _ := run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")

	tracked, _ := run(ctx, dir, "ls-files")
	untracked, _ := run(ctx, dir, "ls-files", "--others", "--exclude-standard")

	files := nonEmptyLines(tracked)
	for _, f := range nonEmptyLines(untracked) {
		if !contains(files, f) {
			files = append(files, f)
		}
	}
	return Change{
		Repo:    strings.TrimSpace(repo),
		BaseRef: "(whole-tree)",
		HeadRef: strings.TrimSpace(head),
		Files:   files,
	}, nil
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}
