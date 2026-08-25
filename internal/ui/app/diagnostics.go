package app

import (
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/watch"
)

// observerDiagnostics raises what the observer and the index could not see in
// the shape of a finding about a document: a registered code, the severity the
// registry publishes for it, and a hint naming the flag that lifts the ceiling.
//
// The status line ranks the same conditions, and each surface words them for
// what it answers: the line fits a phrase into a terminal, a diagnostic names
// the ceiling and the flag that raises it. A diagnostic carrying the line's
// phrase reports one condition twice in the same words to a reader holding
// both.
//
// recoveries is how many times the root has been lost and reopened. A
// recovered root leaves a gap in the change stream that the rebuilt tree does
// not show, so the gap is reported for as long as the process runs rather than
// for the frame the recovery arrived in.
func observerDiagnostics(ws watch.Stats, ix index.Stats, recoveries uint64) []diag.Diagnostic {
	// Each package reports the conditions it detects, so a ceiling is worded
	// once by the code that reached it rather than restated here.
	return append(ws.Diagnostics(recoveries), ix.Diagnostics()...)
}

// shedDiagnostic reports the findings a bounded store could not hold. A
// workspace that is wrong in one way is wrong in that way once per document,
// so the overflow is counted rather than listed.
func shedDiagnostic(n int) diag.Diagnostic {
	return diag.Observed(diag.CodeDiagnosticsDropped,
		"Retained findings reached the ceiling, so the remainder is counted rather than listed.",
		"Raise --max-diagnostics, or resolve the findings that are listed; the same defect repeats across documents.",
		diag.Tally(n, "finding not listed", "findings not listed"))
}
