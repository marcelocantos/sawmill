# find_by_concept Verification

**Target:** 🎯T36 — empirical verify for `find_by_concept`.
**Status:** Verified 2026-05-17.
**Fixture:** `go/mcp/concept_verify_test.go::TestFindByConceptAgainstAuditCorpus`.

This document records the corpus, queries, ground-truth sets, and
measured precision/recall used to verify that `find_by_concept` actually
collapses the multi-round grep workflows surfaced in the May 2026
agent-usage audit (`docs/agent-usage-archaeology.md`).

## Methodology

For each of three concept queries drawn from the audit, the test:

1. Builds an in-memory representative codebase (mixed Go + Python),
   containing concept-relevant symbols, peripheral symbols, and
   documented distractors.
2. For non-built-in concepts (only `app_state` in the current set),
   teaches the project-specific concept via `teach_concept`.
3. Issues a single `find_by_concept` call with `limit=10` and
   `scope="all"`. **No follow-up grep, no manual round-trips.**
4. Compares the returned ranking against two hand-curated sets:
   - **`groundTruth`** — anchor symbols an agent must retrieve to call
     the search useful. Recall threshold is `1.0` (every anchor must
     appear in the top-10).
   - **`relevant`** — the broader set of genuinely concept-relevant
     symbols, including peripheral but on-topic matches. Used for
     `precision@5` (fraction of the top-5 that are concept-relevant).
     Threshold is `0.80`.
5. Asserts that no documented distractor appears in the top-5.

The corpus is intentionally small and code-shaped (rather than copied
from a real upstream repo) so the fixture is hermetic, version-stable,
and reviewable in one screen. The concept-relevance judgments live in
the same source file as the corpus and the assertions — a reviewer can
audit all three in one pass.

Recall is the more important metric: the audit's pain point was
*missing* relevant symbols and falling back to additional grep rounds
or manual reading. Precision@5 is a secondary guard — when top-5 is
dominated by relevant hits, an agent can confidently act on the first
batch instead of reading the full top-10.

## Results

Recorded on 2026-05-17 against commit `9596a2d` + this PR.

| Scenario | Query | Aliases expanded | Recall (ground truth, top-10) | Precision@5 (relevant) |
|---|---|---:|---:|---:|
| `gesture-swipe-handling` | `swipe handling` | 22 | **1.00** (5/5) | **1.00** (5/5) |
| `backboard-app-state` | `app_state` (taught) | 18 | **1.00** (5/5) | **1.00** (5/5) |
| `retry-backoff` | `retry backoff` | 12 | **1.00** (5/5) | **0.80** (4/5) |

All three scenarios meet thresholds. The single non-1.00 cell is
`retry-backoff` precision@5: the top-5 surfaces `sleepBackoff` (a
correctly relevant helper) which isn't in the curated `relevant` set
because it's an unexported implementation detail rather than a public
anchor. Tightening the `relevant` set to include it would lift the
score to 1.00; leaving it out is the conservative reading.

## Scenarios

### 1 — gesture / swipe handling

**Audit source:** transcripts where agents grep for "swipe", read
results, grep for "gesture", read results, grep for "pan" or
"GestureDetector", and stitch the cross-language picture by hand.

**Query:** `"swipe handling"` (uses the built-in `swipe` concept; no
project teach needed).

**Codebase:** `ui/swipe.go` (Go free functions for swipe + pan + fling
handling) + `ui/touch_input.py` (Python `GestureDetector` and
`SwipeRecognizer` classes) + distractor files `core/math.go` and
`config/loader.py`.

**Ground truth (must be in top-10):** `OnSwipeLeft`, `HandlePan`,
`DetectFlingDirection`, `SwipeRecognizer`, `recognise_swipe`.

**Top-10 returned (single call):** `GestureDetector`, `HandlePan`,
`DetectFlingDirection`, `on_pan`, `on_pinch`, `on_double_tap`,
`SwipeRecognizer`, `recognise_swipe`, `OnSwipeLeft`, `OnSwipeRight`.

### 2 — BackBoard-style app-state plumbing

**Audit source:** transcripts where the agent had to discover the
foreground/background lifecycle hooks (`willEnterForeground`,
`didEnterBackground`, BackBoard observers) across multiple files,
typically taking 3–6 grep rounds with synonym variations.

**Query:** `"app_state"` (project-taught concept; built-in dictionary
does not cover lifecycle terminology).

**Teach call:** `teach_concept("app_state", aliases=["app_state",
"appstate", "lifecycle", "foreground", "background",
"willenterforeground", "didenterbackground", "didbecomeactive",
"willresignactive", "willterminate", "applicationstate", "backboard",
"scenedelegate", "applicationdelegate", "on_resume", "on_pause",
"on_stop", "on_start"])`.

**Codebase:** `app/lifecycle.go` (Go free functions named after iOS
delegate hooks) + `app/backboard.py` (`BackBoardObserver` and
`SceneDelegate` classes) + distractors `util/strings.go`,
`util/json_helpers.py`.

**Ground truth:** `WillEnterForeground`, `DidEnterBackground`,
`DidBecomeActive`, `BackBoardObserver`, `SceneDelegate`.

**Top-10 returned (single call):** `BackBoardObserver`,
`WillEnterForeground`, `DidEnterBackground`, `on_resume`, `on_pause`,
`on_stop`, `SceneDelegate`, `DidBecomeActive`, `WillResignActive`,
`AppLifecycle`.

This scenario exercises the **project-extensible** dictionary path —
the acceptance criterion that distinguishes Sawmill's concept search
from generic identifier search.

### 3 — retry / backoff

**Audit source:** transcripts where the agent searches for "retry",
then "backoff", then "exponential", then traces transient-error
handling sites across HTTP clients and worker pools.

**Query:** `"retry backoff"` (uses the built-in `retry` concept).

**Codebase:** `net/retry.go` (Go `CallWithRetry`, `RetryPolicy`,
`ExponentialBackoff`, `IsTransient`) + `net/http_client.py` (Python
`request_with_retry`, `RetryableSession`, `TransientError`) +
distractors `data/transform.go`, `data/validation.py`.

**Ground truth:** `CallWithRetry`, `ExponentialBackoff`, `RetryPolicy`,
`request_with_retry`, `RetryableSession`.

**Top-10 returned (single call):** `ExponentialBackoff`, `RetryPolicy`,
`request_with_retry`, `RetryableSession`, `sleepBackoff`,
`_sleep_backoff`, `CallWithRetry`, `IsTransient`.

## Displacement vs the audit baseline

The audit reported 3–8 grep-and-read cycles per query of this shape,
each cycle consuming a meaningful share of agent context (the grep
output, plus a follow-up read for each plausible hit). The verify
fixture demonstrates the same workflows reduced to:

- **One** `find_by_concept` call returning ranked symbol hits with
  file:line locations.
- **Zero** follow-up greps required to anchor on the right symbols.
- For project-specific terminology (scenario 2), one upfront
  `teach_concept` call amortises across every future query of that
  shape.

The audit's estimate of "~15–20% of total grep volume is displaceable"
therefore holds for the three exemplar queries: each is fully
collapsed by a single tool call, and the recall achieved means an
agent acting on the result will not need a grep fallback.

## Reproducing

```bash
cd go
go test ./mcp/ -run TestFindByConceptAgainstAuditCorpus -v -count=1
```

The test self-reports a summary table at completion. To extend the
corpus with a new query, add a scenario constructor to
`concept_verify_test.go` (model after `gestureSwipeScenario`) and
register it in the `scenarios` slice. Keep the corpus hermetic — the
fixture is the source of truth for "does this query work right now,"
not a curated snapshot of any external repo.
