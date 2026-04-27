// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"bytes"
)

// makeConflictMarker formats a single git-style conflict block.
//
//	<<<<<<< ours
//	<ours>
//	||||||| base       (only when style == "diff3")
//	<base>
//	=======
//	<theirs>
//	>>>>>>> theirs
//
// Each side's text is normalised to end with exactly one newline.
// Empty sides render as zero lines between their markers (this is what
// git produces for delete-vs-modify too).
func makeConflictMarker(ours, base, theirs []byte, opts Options) []byte {
	var buf bytes.Buffer
	buf.WriteString("<<<<<<< ours\n")
	writeWithNL(&buf, ours)
	if opts.Style != "merge" {
		buf.WriteString("||||||| base\n")
		writeWithNL(&buf, base)
	}
	buf.WriteString("=======\n")
	writeWithNL(&buf, theirs)
	buf.WriteString(">>>>>>> theirs\n")
	return buf.Bytes()
}

func writeWithNL(buf *bytes.Buffer, b []byte) {
	if len(b) == 0 {
		return
	}
	buf.Write(b)
	if b[len(b)-1] != '\n' {
		buf.WriteByte('\n')
	}
}
