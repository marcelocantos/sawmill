// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package merge

import "bytes"

// assembleResult concatenates the top-level mergePlan into a single
// merged buffer, fixes up Conflict offsets to be global rather than
// per-resolution, and tallies stats.
func assembleResult(_, _, _ []byte, plan mergePlan, opts Options) Result {
	var buf bytes.Buffer
	r := Result{}
	for _, res := range plan.Resolutions {
		base := buf.Len()
		// Ensure each non-empty resolution sits on its own line(s).
		// If the previous output didn't end in a newline, insert one
		// so we don't accidentally splice two declarations together.
		if base > 0 && bytes.LastIndexByte(buf.Bytes(), '\n') != base-1 {
			buf.WriteByte('\n')
			base = buf.Len()
		}
		if len(res.Bytes) == 0 {
			// Deletion contributes nothing.
			if res.TextMerged {
				r.Stats.DeclsTextMerged++
			} else {
				r.Stats.DeclsResolved++
			}
			continue
		}
		buf.Write(res.Bytes)
		// Ensure trailing newline.
		if res.Bytes[len(res.Bytes)-1] != '\n' {
			buf.WriteByte('\n')
		}
		// Translate per-resolution conflict offsets to global ones.
		for _, c := range res.Conflicts {
			c.Start += base
			c.End += base
			if c.Path == "" {
				c.Path = opts.Path
			}
			r.Conflicts = append(r.Conflicts, c)
		}
		if res.TextMerged {
			r.Stats.DeclsTextMerged++
		} else {
			r.Stats.DeclsResolved++
		}
	}
	r.Merged = buf.Bytes()
	r.Stats.Conflicts = len(r.Conflicts)
	return r
}
