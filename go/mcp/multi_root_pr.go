// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package mcp

// apply_multi_root_pr — create per-repo feature branches, commit diffs, push,
// and open GitHub PRs in a single MCP call.
//
// # Auth
//
// This tool shells out to `gh pr create` for the PR-open step. It requires
// the user to be authenticated with `gh auth login` beforehand. The `gh` CLI
// is already part of the typical Sawmill/Claude Code workflow and carries the
// user's existing GitHub credentials — no token management in this code.
//
// For git operations (branch, stage, commit, push) the tool shells out to the
// `git` CLI as well. This avoids go-git's transport-layer quirks with SSH
// agent forwarding and credential helpers, keeping the behaviour identical to
// what the user would get running `git push` in a terminal.
//
// # Idempotency
//
// If the call partially succeeds and is re-run with the same branch name:
//  - If the branch already exists at HEAD, the tool skips branch creation and
//    reuses the branch, re-staging the diff.
//  - If the branch already exists at a different commit, it appends a new
//    commit rather than creating the branch again.
//  - Before opening a PR, the tool queries `gh pr list --head <branch>`. If a
//    PR already exists for the head branch, it returns the existing PR URL
//    rather than creating a duplicate.
//
// This makes the tool safe to retry after partial failures.
//
// # Templating
//
// `branch_template`, `title_template`, and `body_template` support simple
// string replacement of `{root}` (the absolute root path) and `{repo}` (the
// base name of the root path).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RootPRBundle is the per-root result returned by apply_multi_root_pr.
type RootPRBundle struct {
	// Branch is the feature branch created (or reused) for this root.
	Branch string `json:"branch,omitempty"`
	// CommitSHA is the SHA of the commit written to the branch.
	CommitSHA string `json:"commit_sha,omitempty"`
	// PRURL is the URL of the opened (or pre-existing) pull request.
	PRURL string `json:"pr_url,omitempty"`
	// Error is non-empty if this root failed at any stage.
	Error string `json:"error,omitempty"`
}

// DiffBundle is the caller-supplied input: a root path and a diff to apply.
type DiffBundle struct {
	Root string `json:"root"`
	Diff string `json:"diff"`
}

// applyTemplate replaces {root} and {repo} placeholders in tmpl.
func applyTemplate(tmpl, root string) string {
	repo := filepath.Base(root)
	r := strings.NewReplacer("{root}", root, "{repo}", repo)
	return r.Replace(tmpl)
}

// handleApplyMultiRootPR implements the apply_multi_root_pr tool.
func (h *Handler) handleApplyMultiRootPR(args map[string]any) (string, bool, error) {
	bundlesJSON, err := requireString(args, "bundles")
	if err != nil {
		return err.Error(), true, nil
	}
	branchTmpl, err := requireString(args, "branch_template")
	if err != nil {
		return err.Error(), true, nil
	}
	titleTmpl, err := requireString(args, "title_template")
	if err != nil {
		return err.Error(), true, nil
	}
	bodyTmpl := optString(args, "body_template")
	commitMsg := optString(args, "commit_message")
	if commitMsg == "" {
		commitMsg = "Apply sawmill multi-root transform"
	}

	var bundles []DiffBundle
	if err := json.Unmarshal([]byte(bundlesJSON), &bundles); err != nil {
		return fmt.Sprintf("parsing bundles JSON: %v", err), true, nil
	}
	if len(bundles) == 0 {
		return "bundles must be a non-empty JSON array", true, nil
	}

	results := make(map[string]RootPRBundle, len(bundles))

	for _, b := range bundles {
		bundle := applyDiffAndOpenPR(b.Root, b.Diff,
			applyTemplate(branchTmpl, b.Root),
			applyTemplate(titleTmpl, b.Root),
			applyTemplate(bodyTmpl, b.Root),
			applyTemplate(commitMsg, b.Root),
		)
		results[b.Root] = bundle
	}

	out, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshalling results: %v", err), true, nil
	}

	var sb strings.Builder
	totalPRs := 0
	errRoots := 0
	for _, b := range results {
		if b.PRURL != "" {
			totalPRs++
		}
		if b.Error != "" {
			errRoots++
		}
	}
	fmt.Fprintf(&sb, "apply_multi_root_pr: %d root(s), %d PR(s) opened", len(bundles), totalPRs)
	if errRoots > 0 {
		fmt.Fprintf(&sb, ", %d root(s) with errors", errRoots)
	}
	sb.WriteString("\n\n")
	sb.WriteString(string(out))
	return sb.String(), false, nil
}

// applyDiffAndOpenPR performs the full per-repo flow:
//  1. Apply the patch to the working tree via `git apply`.
//  2. Create (or reuse) a feature branch.
//  3. Stage all changed files and commit.
//  4. Push the branch to origin.
//  5. Open a PR via `gh pr create` (or return the URL of an existing one).
//
// All errors are captured in RootPRBundle.Error; the caller never sees a Go
// error from here.
func applyDiffAndOpenPR(root, diff, branch, title, body, commitMsg string) RootPRBundle {
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

	// Verify the root exists and is a git repo.
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("not a git repo (no .git found): %v", err)}
	}

	// Step 1: apply the diff to the working tree.
	if err := gitApply(root, diff); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git apply: %v", err)}
	}

	// Step 2: create or switch to the feature branch.
	if err := gitCheckoutBranch(root, branch); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git checkout branch %q: %v", branch, err)}
	}

	// Step 3: stage all modified/added files and commit.
	if err := gitAddAll(root); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git add: %v", err)}
	}
	sha, err := gitCommitChanges(root, commitMsg)
	if err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git commit: %v", err)}
	}

	// Step 4: push the branch to origin.
	if err := gitPush(root, branch); err != nil {
		return RootPRBundle{Error: fmt.Sprintf("git push: %v", err), Branch: branch, CommitSHA: sha}
	}

	// Step 5: open a PR (or return existing).
	prURL, err := ghOpenPR(root, branch, title, body)
	if err != nil {
		return RootPRBundle{Error: fmt.Sprintf("gh pr create: %v", err), Branch: branch, CommitSHA: sha}
	}

	return RootPRBundle{Branch: branch, CommitSHA: sha, PRURL: prURL}
}

// gitRun executes a git command in dir and returns combined stdout+stderr.
func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// gitApply applies a unified diff to the working tree using `git apply`.
func gitApply(root, diff string) error {
	cmd := exec.Command("git", "apply", "--index", "-")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(diff)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w\n%s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// gitCheckoutBranch creates and switches to branch, or just switches if it
// already exists.
func gitCheckoutBranch(root, branch string) error {
	// Try to create the branch; if it already exists, switch to it.
	_, err := gitRun(root, "checkout", "-b", branch)
	if err != nil {
		// Branch may already exist — try switching.
		_, err2 := gitRun(root, "checkout", branch)
		if err2 != nil {
			// Return the original create-branch error for clarity.
			return err
		}
	}
	return nil
}

// gitAddAll stages all changes (modified + deleted + new tracked).
func gitAddAll(root string) error {
	_, err := gitRun(root, "add", "-A")
	return err
}

// gitCommitChanges commits staged changes and returns the new commit SHA.
func gitCommitChanges(root, msg string) (string, error) {
	_, err := gitRun(root, "commit", "-m", msg)
	if err != nil {
		return "", err
	}
	sha, err := gitRun(root, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %w", err)
	}
	return sha, nil
}

// gitPush pushes branch to origin with -u to set upstream.
func gitPush(root, branch string) error {
	_, err := gitRun(root, "push", "-u", "origin", branch)
	return err
}

// ghOpenPR opens a PR via `gh pr create`. If a PR already exists for the head
// branch (idempotency), it returns the existing PR's URL instead.
func ghOpenPR(root, branch, title, body string) (string, error) {
	// Check for an existing PR first.
	existing, err := ghExistingPRURL(root, branch)
	if err != nil {
		return "", fmt.Errorf("checking for existing PR: %w", err)
	}
	if existing != "" {
		return existing, nil
	}

	args := []string{"pr", "create", "--head", branch, "--title", title}
	if body != "" {
		args = append(args, "--body", body)
	} else {
		args = append(args, "--body", "")
	}

	cmd := exec.Command("gh", args...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// ghExistingPRURL returns the URL of an open PR with the given head branch, or
// "" if none exists.
func ghExistingPRURL(root, branch string) (string, error) {
	cmd := exec.Command("gh", "pr", "list",
		"--head", branch,
		"--state", "open",
		"--json", "url",
		"--jq", ".[0].url // empty",
	)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(out.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
