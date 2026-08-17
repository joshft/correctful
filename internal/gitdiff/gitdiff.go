// Package gitdiff resolves the set of files a change touches, relative to a base
// ref. This is the substrate correctful harvests claims from.
package gitdiff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	// Excluded discloses what Resolve deliberately left OUT of Files. Those
	// files never reach the harvest, so the coverage section cannot mention
	// them — this is the one place the exclusion can be stated, and "blind
	// spots are always stated" applies to the scope boundary itself.
	Excluded []Exclusion
	// InputDigest pins the exact content harvested (see the InputDigest
	// function). HeadSHA identifies a clean tree; a mid-branch receipt over
	// staged, unstaged, or untracked work needs this to be reproducible.
	InputDigest string
}

// Exclusion is one scope rule's deliberate effect on the change: how many
// files the resolver left out, why, and (for the territory rule) which
// top-level trees they lived in. Counts, not paths — the measured case was
// thousands of cache files, and reproducing the list would drown the receipt
// in exactly the noise the rule exists to exclude.
type Exclusion struct {
	Reason string   // stable identifier: "untracked-territory" | "untracked-hidden"
	Count  int      // files excluded by this rule
	Dirs   []string // top-level trees affected (territory rule only), sorted
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
	// Hidden is checked FIRST so a hidden file inside a never-tracked tree is
	// attributed to the harvest-wide hidden-path principle, not the narrower
	// territory rule. Tracked hidden files (a CI workflow) are unaffected —
	// the exclusions apply to the untracked union only.
	hidden := 0
	territoryDirs := map[string]bool{}
	territory := 0
	for _, f := range nonEmptyLines(untracked) {
		if hiddenPath(f) {
			hidden++
			continue
		}
		top, _, inDir := strings.Cut(f, "/")
		if inDir && !trackedTop[top] {
			// An entirely untracked top-level tree; the root itself is
			// always tracked territory.
			territoryDirs[top] = true
			territory++
			continue
		}
		if contains(files, f) {
			continue
		}
		files = append(files, f)
	}
	var excluded []Exclusion
	if territory > 0 {
		dirs := make([]string, 0, len(territoryDirs))
		for d := range territoryDirs {
			dirs = append(dirs, d)
		}
		sort.Strings(dirs)
		excluded = append(excluded, Exclusion{Reason: "untracked-territory", Count: territory, Dirs: dirs})
	}
	if hidden > 0 {
		excluded = append(excluded, Exclusion{Reason: "untracked-hidden", Count: hidden})
	}

	baseSHA, _ := run(ctx, dir, "merge-base", baseRef, "HEAD")
	headSHA, _ := run(ctx, dir, "rev-parse", "HEAD")
	return Change{
		Repo:     strings.TrimSpace(repo),
		BaseRef:  baseRef,
		HeadRef:  strings.TrimSpace(head),
		BaseSHA:  strings.TrimSpace(baseSHA),
		HeadSHA:  strings.TrimSpace(headSHA),
		Files:    files,
		Excluded: excluded,
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

// InputDigest computes a SHA-256 pin over the exact content the harvest will
// read, so a receipt over a DIRTY tree — staged, unstaged, or untracked work
// that no commit SHA identifies — is still reproducible: same file set, same
// bytes, same digest.
//
// The formula, so anyone can recompute it: for each file of the resolved set
// in ascending path order, feed the outer SHA-256 the path, a NUL, then the
// 32 raw bytes of the file content's own SHA-256 (the string "absent" instead
// when the path is not a readable regular file — a deletion is part of the
// change's identity too), then a newline. Per-file inner hashing makes file
// boundaries unambiguous regardless of content bytes.
func InputDigest(dir string, files []string) string {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	outer := sha256.New()
	for _, f := range sorted {
		io.WriteString(outer, f)
		outer.Write([]byte{0})
		if sum, ok := fileSHA256(filepath.Join(dir, f)); ok {
			outer.Write(sum)
		} else {
			io.WriteString(outer, "absent")
		}
		outer.Write([]byte{'\n'})
	}
	return hex.EncodeToString(outer.Sum(nil))
}

// fileSHA256 streams a regular file into a SHA-256, reporting !ok for
// anything unreadable or non-regular (deleted files, directories, symlink
// targets outside the tree).
func fileSHA256(abs string) ([]byte, bool) {
	fh, err := os.Open(abs)
	if err != nil {
		return nil, false
	}
	defer fh.Close()
	if fi, err := fh.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return nil, false
	}
	return h.Sum(nil), true
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
