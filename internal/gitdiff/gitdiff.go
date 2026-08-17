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

// Change is the resolved file set of a diff. BaseSHA/HeadSHA pin the receipt
// to immutable inputs: BaseSHA is the MERGE BASE actually diffed against (not
// the symbolic ref, which moves), HeadSHA the commit under the working tree.
type Change struct {
	Repo    string
	BaseRef string
	HeadRef string
	BaseSHA string
	HeadSHA string
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
	// before the work is committed: tracked edits AND untracked files — a new
	// test file that is not yet `git add`ed is still part of the change, and
	// leaving it out would hide it even from the coverage disclosure, the one
	// place a blind spot must never be invisible.
	//
	// Untracked inclusion is scoped to territory the repo already TRACKS: a
	// new file beside tracked code joins the change; an entirely untracked
	// top-level tree is some other tool's output, not this change. Measured
	// on a live tree, the unscoped union drowned a 101-file change under
	// thousands of untracked cache files from an installed tool (plus
	// hidden-directory tooling state, excluded by the same principle the
	// harvest layer applies to claim origins). The trade-off is disclosed: a
	// brand-new top-level directory of real work stays invisible until its
	// first `git add`.
	unstaged, _ := run(ctx, dir, "diff", "--name-only", "HEAD")
	for _, f := range nonEmptyLines(unstaged) {
		if !contains(files, f) {
			files = append(files, f)
		}
	}
	tracked, _ := run(ctx, dir, "ls-files")
	trackedTop := map[string]bool{}
	for _, f := range nonEmptyLines(tracked) {
		top, _, _ := strings.Cut(f, "/")
		trackedTop[top] = true
	}
	untracked, _ := run(ctx, dir, "ls-files", "--others", "--exclude-standard")
	for _, f := range nonEmptyLines(untracked) {
		top, _, inDir := strings.Cut(f, "/")
		if inDir && !trackedTop[top] {
			continue // an entirely untracked top-level tree; the root itself is always tracked territory
		}
		if hiddenPath(f) || contains(files, f) {
			continue
		}
		files = append(files, f)
	}

	baseSHA, _ := run(ctx, dir, "merge-base", baseRef, "HEAD")
	headSHA, _ := run(ctx, dir, "rev-parse", "HEAD")
	return Change{
		Repo:    strings.TrimSpace(repo),
		BaseRef: baseRef,
		HeadRef: strings.TrimSpace(head),
		BaseSHA: strings.TrimSpace(baseSHA),
		HeadSHA: strings.TrimSpace(headSHA),
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
	headSHA, _ := run(ctx, dir, "rev-parse", "HEAD")
	return Change{
		Repo:    strings.TrimSpace(repo),
		BaseRef: "(whole-tree)",
		HeadRef: strings.TrimSpace(head),
		HeadSHA: strings.TrimSpace(headSHA),
		Files:   files,
	}, nil
}

// Patch returns the unified diff text of the change Resolve describes: the
// working tree against the MERGE BASE, in one diff — committed and
// uncommitted work together, with no overlapping sections for a file changed
// in both. This is the substrate the LLM extractor reads — the change itself,
// not the repository. (Untracked files have no diff and are absent here; the
// coverage disclosure still lists them.)
func Patch(ctx context.Context, dir, baseRef string) (string, error) {
	mb, err := run(ctx, dir, "merge-base", baseRef, "HEAD")
	if err != nil {
		return "", err
	}
	return run(ctx, dir, "diff", strings.TrimSpace(mb))
}

// TrackedByPattern lists tracked files matching the given pathspec patterns
// (e.g. "*.md"), repo-relative. Tracked only, deliberately: the definition
// corpus a claim resolves against should be what the repo COMMITS to, not a
// scratch note in the working tree.
func TrackedByPattern(ctx context.Context, dir string, patterns ...string) ([]string, error) {
	args := append([]string{"ls-files", "--"}, patterns...)
	out, err := run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// hiddenPath reports whether any component of a repo-relative path is hidden
// (the harvest layer applies the same rule to claim origins).
func hiddenPath(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
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
