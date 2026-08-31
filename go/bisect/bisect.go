// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package bisect

import (
	"fmt"
	"io"
	"strings"

	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
	"github.com/marcelocantos/sawmill/semdiff"
)

// Indexer is the subset of gitindex.Indexer that bisect needs.
type Indexer interface {
	EnsureCommitIndexed(sha string) error
}

// Result is the output of a successful Bisect call.
type Result struct {
	Predicate        *Predicate            `json:"predicate"`
	GoodSHA          string                `json:"good"`
	BadSHA           string                `json:"bad"`
	GoodValue        bool                  `json:"good_value"`
	BadValue         bool                  `json:"bad_value"`
	FlipSHA          string                `json:"flip_commit"`
	FlipParentSHA    string                `json:"flip_parent_commit"`
	Author           string                `json:"author"`
	Email            string                `json:"email,omitempty"`
	Date             string                `json:"date"`
	Message          string                `json:"message"`
	StructuralChange *semdiff.SymbolChange `json:"structural_change,omitempty"`
	CommitsExamined  int                   `json:"commits_examined"`
	CommitsInRange   int                   `json:"commits_in_range"`
}

// Bisect finds the commit (in the first-parent ancestry of badSHA, between
// goodSHA and badSHA inclusive) where the predicate's value flipped relative
// to goodSHA. The predicate must have different values at goodSHA and badSHA.
// Commits are indexed lazily as the binary search visits them.
func Bisect(ix Indexer, store *gitindex.Store, repo *gitrepo.Repo, pred *Predicate, goodSHA, badSHA string) (*Result, error) {
	if goodSHA == badSHA {
		return nil, fmt.Errorf("good and bad commits are the same (%s)", goodSHA)
	}

	// Index endpoints and evaluate the predicate at both.
	if err := ix.EnsureCommitIndexed(goodSHA); err != nil {
		return nil, fmt.Errorf("indexing good commit: %w", err)
	}
	if err := ix.EnsureCommitIndexed(badSHA); err != nil {
		return nil, fmt.Errorf("indexing bad commit: %w", err)
	}
	goodVal, err := pred.Eval(store, repo, goodSHA)
	if err != nil {
		return nil, fmt.Errorf("evaluating predicate at good: %w", err)
	}
	badVal, err := pred.Eval(store, repo, badSHA)
	if err != nil {
		return nil, fmt.Errorf("evaluating predicate at bad: %w", err)
	}
	if goodVal == badVal {
		return nil, fmt.Errorf("predicate has same value (%v) at good (%s) and bad (%s) — no flip in this range",
			goodVal, shortSHA(goodSHA), shortSHA(badSHA))
	}

	// Walk the first-parent chain from bad back to good. commits[0] = bad,
	// commits[len-1] = good.
	commits, err := walkRange(repo, goodSHA, badSHA)
	if err != nil {
		return nil, err
	}

	// Binary search for the boundary index. We look for the smallest index i
	// (newest direction) such that pred(commits[i]) == badVal — that commit
	// is the flip; commits[i+1] is its first-parent ancestor where the
	// predicate still had the good value.
	//
	// Invariant during search:
	//   pred(commits[hi]) == badVal
	//   pred(commits[lo]) == goodVal
	//   (hi < lo by construction; we shrink lo - hi until it equals 1)
	cache := map[int]bool{
		0:               badVal,
		len(commits) - 1: goodVal,
	}
	hi := 0
	lo := len(commits) - 1
	examined := 2

	for lo-hi > 1 {
		mid := (lo + hi) / 2
		v, ok := cache[mid]
		if !ok {
			if err := ix.EnsureCommitIndexed(commits[mid].SHA); err != nil {
				return nil, fmt.Errorf("indexing commit %s: %w", commits[mid].SHA, err)
			}
			v, err = pred.Eval(store, repo, commits[mid].SHA)
			if err != nil {
				return nil, fmt.Errorf("evaluating predicate at %s: %w", commits[mid].SHA, err)
			}
			cache[mid] = v
			examined++
		}
		if v == badVal {
			hi = mid
		} else {
			lo = mid
		}
	}

	flip := commits[hi]
	parent := commits[lo]

	// Compute the structural change between parent and flip to attribute
	// the flip to a specific symbol/signature change.
	var change *semdiff.SymbolChange
	if diff, derr := semdiff.Diff(store, repo, parent.SHA, flip.SHA); derr == nil {
		change = findRelevantChange(diff, pred)
	}

	return &Result{
		Predicate:        pred,
		GoodSHA:          goodSHA,
		BadSHA:           badSHA,
		GoodValue:        goodVal,
		BadValue:         badVal,
		FlipSHA:          flip.SHA,
		FlipParentSHA:    parent.SHA,
		Author:           flip.Author,
		Email:            flip.Email,
		Date:             flip.When.UTC().Format("2006-01-02T15:04:05Z"),
		Message:          firstLine(flip.Message),
		StructuralChange: change,
		CommitsExamined:  examined,
		CommitsInRange:   len(commits),
	}, nil
}

// walkRange returns commits from badSHA back to (and including) goodSHA, in
// newest-first order. Returns an error if goodSHA is not in the first-parent
// ancestry of badSHA.
func walkRange(repo *gitrepo.Repo, goodSHA, badSHA string) ([]*gitrepo.Commit, error) {
	var commits []*gitrepo.Commit
	foundGood := false
	walkErr := repo.WalkCommits(badSHA, func(c *gitrepo.Commit) error {
		// Copy — WalkCommits's pointer may be reused.
		copied := *c
		commits = append(commits, &copied)
		if c.SHA == goodSHA {
			foundGood = true
			return io.EOF
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking commits from %s: %w", shortSHA(badSHA), walkErr)
	}
	if !foundGood {
		return nil, fmt.Errorf("good commit %s is not in the first-parent ancestry of bad commit %s",
			shortSHA(goodSHA), shortSHA(badSHA))
	}
	return commits, nil
}

// findRelevantChange picks the SymbolChange most relevant to the predicate
// (matched by subject name).
func findRelevantChange(diff *semdiff.DiffResult, pred *Predicate) *semdiff.SymbolChange {
	subject := pred.Subject()
	if subject == "" {
		return nil
	}
	for _, f := range diff.Files {
		for i, s := range f.Symbols {
			if s.Name == subject || s.NewName == subject {
				return &f.Symbols[i]
			}
		}
	}
	return nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
