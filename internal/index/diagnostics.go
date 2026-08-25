package index

import "github.com/stxkxs/agentfs/internal/diag"

// Diagnostics reports the ceilings the index reached, in the shape of a finding
// about a document.
//
// A tree held to a ceiling is a partial view, and a caller that cannot tell a
// partial view from a complete one draws the wrong conclusion from an absence.
// Reporting the condition as a coded diagnostic rather than as prose means the
// machine output carries it too, in the vocabulary the rest of the output
// already uses.
func (s Stats) Diagnostics() []diag.Diagnostic {
	var out []diag.Diagnostic

	if s.NodeCeilingHit {
		out = append(out, diag.Observed(diag.CodeNodeCeiling,
			"The tree reached the node ceiling, so what is held is a prefix of the workspace.",
			"Raise --max-nodes if the host has the memory; the node table is proportional to it.",
			diag.Tally(s.Nodes, "node held", "nodes held")))
	}
	if s.TruncatedDirs > 0 {
		out = append(out, diag.Observed(diag.CodeEntriesTruncated,
			"A directory holds more entries than the per-directory ceiling, so what is held is a prefix of it.",
			"Raise --max-entries-per-dir, or write bulk output into artifacts/ rather than into one directory.",
			diag.Tally(s.TruncatedDirs, "directory", "directories")))
	}
	if s.DepthTruncated > 0 {
		out = append(out, diag.Observed(diag.CodeDepthTruncated,
			"A subtree lies below the depth ceiling, so it was not opened.",
			"Raise --max-depth, or point agentfs deeper into the tree.",
			diag.Tally(s.DepthTruncated, "subtree", "subtrees")))
	}
	return out
}
