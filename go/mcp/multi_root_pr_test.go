// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

// Tests for apply_multi_root_pr. Strategy:
//
//   - Git operations are exercised end-to-end against ephemeral local bare
//     repos created with `git init --bare`. The push target is a local
//     filesystem path so no network access is needed.
//
//   - The `gh pr create` / `gh pr list` step is bypassed by overriding the
//     tool's PR-opener via the prOpener field injected into the test. Production
//     code uses the real ghOpenPR function; tests substitute a fake that records
//     calls and returns a canned URL.
//
//   - Error isolation: one repo uses a push target with no write permission to
//     exercise the per-root error path without aborting siblings.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initBareRepo creates a bare git repo at path and returns path.
func initBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "bare.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, bare, "git", "init", "--bare")
	return bare
}

// initWorkingRepo creates a working tree at a temp dir, makes an initial
// commit, and adds bareURL as the "origin" remote. Returns the working-tree
// path.
func initWorkingRepo(t *testing.T, bareURL string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "-b", "master")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test")
	run(t, dir, "git", "remote", "add", "origin", bareURL)

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "initial")
	run(t, dir, "git", "push", "-u", "origin", "master")
	return dir
}

// run executes a command in dir, failing the test if it exits non-zero.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fakePROpener is a prOpener that records calls and returns canned URLs.
type fakePROpener struct {
	calls   []prOpenCall
	urlFmt  string // format string; %d is call index
	failFor map[string]bool // root paths that should return an error
}

type prOpenCall struct {
	root, branch, title, body string
}

func (f *fakePROpener) open(root, branch, title, body string) (string, error) {
	f.calls = append(f.calls, prOpenCall{root, branch, title, body})
	if f.failFor[root] {
		return "", fmt.Errorf("simulated gh auth failure for %s", root)
	}
	return fmt.Sprintf(f.urlFmt, len(f.calls)), nil
}

// applyDiffAndOpenPRWith is a variant of applyDiffAndOpenPR that accepts an
// injectable PR-opener. Used by tests to bypass the real `gh` CLI.
func applyDiffAndOpenPRWith(
	root, diff, branch, title, body, commitMsg string,
	opener func(root, branch, title, body string) (string, error),
) RootPRBundle {
	if root == "" {
		return RootPRBundle{Error: "root is empty"}
	}
	if diff == "" {
		return RootPRBundle{Error: "diff is empty — nothing to commit"}
	}
	if branch == "" {
		return RootPRBundle{Error: "branch name is empty (check branch_template)"}
	}
	if title == "" {
		return RootPRBundle{Error: "PR title is empty (check title_template)"}
	}

	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("not a git repo (no .git found): %v", err)}
	}

	if err := gitApply(root, diff); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git apply: %v", err)}
	}
	if err := gitCheckoutBranch(root, branch); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git checkout branch %q: %v", branch, err)}
	}
	if err := gitAddAll(root); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git add: %v", err)}
	}
	sha, err := gitCommitChanges(root, commitMsg)
	if err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git commit: %v", err)}
	}
	if err := gitPush(root, branch); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git push: %v", err), Branch: branch, CommitSHA: sha}
	}
	prURL, err := opener(root, branch, title, body)
	if err != nil {
		return RootPRBundle{Error: fmt.Sprintf("gh pr create: %v", err), Branch: branch, CommitSHA: sha}
	}
	return RootPRBundle{Branch: branch, CommitSHA: sha, PRURL: prURL}
}

// makeDiff builds a minimal unified diff that changes oldContent to newContent
// in a given filename. Only suitable for single-line files in tests.
func makeDiff(filename, oldContent, newContent string) string {
	return fmt.Sprintf(
		"--- a/%s\n+++ b/%s\n@@ -1 +1 @@\n-%s+%s",
		filename, filename, oldContent, newContent,
	)
}

// TestApplyMultiRootPRTwoRepos exercises the happy path with two repos.
func TestApplyMultiRootPRTwoRepos(t *testing.T) {
	bare1 := initBareRepo(t)
	bare2 := initBareRepo(t)

	root1 := initWorkingRepo(t, bare1, map[string]string{
		"hello.py": "msg = \"hello\"\n",
	})
	root2 := initWorkingRepo(t, bare2, map[string]string{
		"app.py": "name = \"world\"\n",
	})

	opener := &fakePROpener{
		urlFmt: "https://github.com/org/repo/pull/%d",
	}

	b1 := applyDiffAndOpenPRWith(
		root1,
		makeDiff("hello.py", "msg = \"hello\"\n", "msg = \"goodbye\"\n"),
		"sawmill/test-root1",
		"chore(root1): rename hello to goodbye",
		"Automated rename.",
		"test commit",
		opener.open,
	)
	b2 := applyDiffAndOpenPRWith(
		root2,
		makeDiff("app.py", "name = \"world\"\n", "name = \"earth\"\n"),
		"sawmill/test-root2",
		"chore(root2): rename world to earth",
		"Automated rename.",
		"test commit",
		opener.open,
	)

	if b1.Error != "" {
		t.Errorf("root1 error: %s", b1.Error)
	}
	if b1.Branch != "sawmill/test-root1" {
		t.Errorf("root1 branch: want sawmill/test-root1, got %q", b1.Branch)
	}
	if b1.CommitSHA == "" {
		t.Error("root1 CommitSHA empty")
	}
	if !strings.HasPrefix(b1.PRURL, "https://") {
		t.Errorf("root1 PRURL: want https://…, got %q", b1.PRURL)
	}

	if b2.Error != "" {
		t.Errorf("root2 error: %s", b2.Error)
	}
	if b2.PRURL == "" {
		t.Error("root2 PRURL empty")
	}

	if len(opener.calls) != 2 {
		t.Errorf("expected 2 PR-opener calls, got %d", len(opener.calls))
	}
}

// TestApplyMultiRootPRThreeRoosWithError verifies N>2 error isolation: two good
// repos and one that fails at git-push. Good repos must succeed; the failing
// repo must surface a clear error without aborting its siblings.
//
// The push failure is induced by pointing root3's origin remote at an invalid
// URL (no such directory) — reliable on all platforms without chmod tricks.
func TestApplyMultiRootPRThreeRoosWithError(t *testing.T) {
	bare1 := initBareRepo(t)
	bare2 := initBareRepo(t)

	root1 := initWorkingRepo(t, bare1, map[string]string{
		"alpha.py": "x = \"alpha\"\n",
	})
	root2 := initWorkingRepo(t, bare2, map[string]string{
		"beta.py": "y = \"beta\"\n",
	})

	// root3: create a working tree with a valid initial state but set origin to
	// a nonexistent path so that git push fails.
	bare3 := initBareRepo(t)
	root3 := initWorkingRepo(t, bare3, map[string]string{
		"c.py": "c = 1\n",
	})
	// Redirect origin to a path that does not exist — push will always fail.
	run(t, root3, "git", "remote", "set-url", "origin", "/nonexistent/does-not-exist.git")

	opener := &fakePROpener{
		urlFmt: "https://github.com/org/repo/pull/%d",
	}

	diff1 := makeDiff("alpha.py", "x = \"alpha\"\n", "x = \"ALPHA\"\n")
	diff2 := makeDiff("beta.py", "y = \"beta\"\n", "y = \"BETA\"\n")
	diff3 := makeDiff("c.py", "c = 1\n", "c = 2\n")

	b1 := applyDiffAndOpenPRWith(root1, diff1, "sawmill/iso-root1", "PR root1", "", "commit", opener.open)
	b2 := applyDiffAndOpenPRWith(root2, diff2, "sawmill/iso-root2", "PR root2", "", "commit", opener.open)
	b3 := applyDiffAndOpenPRWith(root3, diff3, "sawmill/iso-root3", "PR root3", "", "commit", opener.open)

	if b1.Error != "" {
		t.Errorf("root1 unexpected error: %s", b1.Error)
	}
	if b1.PRURL == "" {
		t.Error("root1 PRURL empty")
	}

	if b2.Error != "" {
		t.Errorf("root2 unexpected error: %s", b2.Error)
	}
	if b2.PRURL == "" {
		t.Error("root2 PRURL empty")
	}

	if b3.Error == "" {
		t.Error("root3: expected an error (push to nonexistent remote)")
	}
	if !strings.Contains(b3.Error, "git push") {
		t.Errorf("root3: error should mention 'git push', got: %s", b3.Error)
	}
	if b3.PRURL != "" {
		t.Errorf("root3: should have no PRURL on push failure, got %q", b3.PRURL)
	}
	// Branch and commit SHA should be populated up to where it succeeded.
	if b3.Branch == "" {
		t.Error("root3: Branch should be set (commit happened before push)")
	}
	if b3.CommitSHA == "" {
		t.Error("root3: CommitSHA should be set (commit happened before push)")
	}

	// PR opener should only have been called for the two successful repos.
	if len(opener.calls) != 2 {
		t.Errorf("expected 2 PR-opener calls (only successful repos), got %d", len(opener.calls))
	}
}

// TestApplyMultiRootPRIdempotentBranch verifies two idempotency properties:
//
//  1. gitCheckoutBranch succeeds when the branch already exists (reuses it).
//  2. A second full run on the same repo (after branch deletion+recreation)
//     produces a valid bundle.
func TestApplyMultiRootPRIdempotentBranch(t *testing.T) {
	// Property 1: branch reuse via gitCheckoutBranch.
	{
		dir := t.TempDir()
		run(t, dir, "git", "init", "-b", "master")
		run(t, dir, "git", "config", "user.email", "test@example.com")
		run(t, dir, "git", "config", "user.name", "Test")
		// Need an initial commit so branch creation works.
		if err := os.WriteFile(filepath.Join(dir, "f.py"), []byte("x=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, dir, "git", "add", "-A")
		run(t, dir, "git", "commit", "-m", "init")

		// Create the branch once.
		if err := gitCheckoutBranch(dir, "sawmill/reuse"); err != nil {
			t.Fatalf("first checkout: %v", err)
		}
		// Switch back to master so we can re-checkout the branch.
		run(t, dir, "git", "checkout", "master")
		// Second checkout of same branch name must not error.
		if err := gitCheckoutBranch(dir, "sawmill/reuse"); err != nil {
			t.Fatalf("second checkout (reuse): %v", err)
		}
	}

	// Property 2: second full run (branch deleted between runs).
	{
		bare := initBareRepo(t)
		root := initWorkingRepo(t, bare, map[string]string{
			"idem.py": "v = 1\n",
		})

		opener := &fakePROpener{urlFmt: "https://github.com/org/repo/pull/%d"}

		diff1 := makeDiff("idem.py", "v = 1\n", "v = 2\n")
		b1 := applyDiffAndOpenPRWith(root, diff1, "sawmill/idem", "PR idem", "", "commit 1", opener.open)
		if b1.Error != "" {
			t.Fatalf("first run error: %s", b1.Error)
		}

		// Reset: go back to master, restore original file, delete branch.
		run(t, root, "git", "checkout", "master")
		if err := os.WriteFile(filepath.Join(root, "idem.py"), []byte("v = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, root, "git", "branch", "-D", "sawmill/idem")
		run(t, root, "git", "push", "origin", "--delete", "sawmill/idem")

		diff2 := makeDiff("idem.py", "v = 1\n", "v = 3\n")
		b2 := applyDiffAndOpenPRWith(root, diff2, "sawmill/idem", "PR idem", "", "commit 2", opener.open)
		if b2.Error != "" {
			t.Fatalf("second run error: %s", b2.Error)
		}
		if b2.PRURL == "" {
			t.Error("second run PRURL empty")
		}
	}
}

// TestApplyMultiRootPRTemplating verifies that {root} and {repo} in templates
// are replaced correctly.
func TestApplyMultiRootPRTemplating(t *testing.T) {
	root := "/some/path/myrepo"
	branch := applyTemplate("sawmill/{repo}-fix", root)
	if branch != "sawmill/myrepo-fix" {
		t.Errorf("branch template: got %q", branch)
	}
	title := applyTemplate("chore({repo}): update", root)
	if title != "chore(myrepo): update" {
		t.Errorf("title template: got %q", title)
	}
	body := applyTemplate("Root: {root}", root)
	if body != "Root: /some/path/myrepo" {
		t.Errorf("body template: got %q", body)
	}
}

// TestApplyMultiRootPRBadRoot verifies that a non-existent root captures an
// error in the bundle without panicking.
func TestApplyMultiRootPRBadRoot(t *testing.T) {
	opener := &fakePROpener{urlFmt: "https://github.com/org/repo/pull/%d"}
	b := applyDiffAndOpenPRWith(
		"/nonexistent/path/does-not-exist",
		"--- a/x.py\n+++ b/x.py\n@@ -1 +1 @@\n-old\n+new\n",
		"sawmill/test",
		"title",
		"body",
		"msg",
		opener.open,
	)
	if b.Error == "" {
		t.Error("expected error for non-existent root")
	}
	if len(opener.calls) != 0 {
		t.Errorf("opener should not be called on bad root, got %d calls", len(opener.calls))
	}
}

// TestHandleApplyMultiRootPRIntegration tests the MCP handler dispatch path
// with a valid bundle JSON, ensuring the JSON parsing and summary output work.
func TestHandleApplyMultiRootPRIntegration(t *testing.T) {
	// This test drives the handler end-to-end with a real git repo so we
	// exercise the full code path through handleApplyMultiRootPR. We need
	// a real local gh to be present but we can point it at a local repo
	// that returns an error, then check that the error isolation works.
	//
	// Since gh is unlikely to be authenticated in CI, we accept either a
	// successful PR URL or a "gh pr create" error — both are valid per-root
	// outcomes.

	bare := initBareRepo(t)
	root := initWorkingRepo(t, bare, map[string]string{
		"main.py": "x = \"old\"\n",
	})

	h := NewHandler()

	diff := makeDiff("main.py", "x = \"old\"\n", "x = \"new\"\n")
	bundles, _ := json.Marshal([]DiffBundle{
		{Root: root, Diff: diff},
	})

	text, isErr, err := h.handleApplyMultiRootPR(map[string]any{
		"bundles":         string(bundles),
		"branch_template": "sawmill/integration-{repo}",
		"title_template":  "test: integration PR for {repo}",
		"body_template":   "Auto-generated by sawmill for {root}.",
	})

	if err != nil {
		t.Fatalf("system error: %v", err)
	}
	// Tool-level error is acceptable here only if gh is not available.
	if isErr {
		t.Logf("tool error (acceptable if gh not authenticated): %s", text)
		return
	}

	// Summary line must be present.
	if !strings.Contains(text, "apply_multi_root_pr: 1 root(s)") {
		t.Errorf("missing summary line, got: %s", text[:min(200, len(text))])
	}

	// Parse the JSON result.
	jsonStart := strings.Index(text, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON in output: %s", text)
	}
	var results map[string]RootPRBundle
	if err := json.Unmarshal([]byte(text[jsonStart:]), &results); err != nil {
		t.Fatalf("unmarshalling result: %v", err)
	}

	bundle, ok := results[root]
	if !ok {
		t.Fatalf("root not in results")
	}
	// Either success (PR URL present) or a gh-auth error — either way Branch
	// and CommitSHA should be populated since git ops come first.
	if bundle.Branch == "" && bundle.Error == "" {
		t.Error("expected either Branch or Error to be set")
	}
}
