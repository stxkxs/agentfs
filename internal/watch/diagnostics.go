package watch

import "github.com/stxkxs/agentfs/internal/diag"

// Diagnostics reports what the observer could not see, in the shape of a
// finding about a document.
//
// recoveries is how many times the root has been lost and reopened. A recovered
// root leaves a gap in the change stream that the rebuilt tree does not show,
// so the gap is reported for as long as the process runs rather than for the
// moment the recovery arrived in.
func (s Stats) Diagnostics(recoveries uint64) []diag.Diagnostic {
	var out []diag.Diagnostic

	// A lost root is raised by the scan that could not read it, so it is not
	// repeated here: one condition reported twice reads as two.
	if recoveries > 0 && !s.RootLost {
		out = append(out, diag.Observed(diag.CodeRootRecovered,
			"The workspace root became readable again and the tree was rebuilt from it.",
			"Changes written while the root was unreadable were never observed, so the activity feed has a gap the tree does not.",
			diag.Tally(recoveries, "outage", "outages")))
	}
	if s.Dropped > 0 {
		out = append(out, diag.Observed(diag.CodeBatchTruncated,
			"Changes were lost, so the tree was rebuilt from the filesystem rather than from the batch.",
			dropHint(s),
			diag.Tally(s.Dropped, "change dropped", "changes dropped")))
	}
	if s.WatchesRefused > 0 {
		out = append(out, diag.Observed(diag.CodeWatchBudget,
			"Directories could not be watched, so they are swept instead and report a change within one sweep interval.",
			"Raise the kernel watch limit, or accept detection at the sweep interval; --max-watches bounds what agentfs asks for.",
			diag.Tally(s.WatchesRefused, "directory swept", "directories swept")))
	}
	return out
}

// dropHint names the ceiling that lost the changes, because raising the other
// one would leave the loss exactly where it was.
func dropHint(s Stats) string {
	switch {
	case s.QueueOverflows > 0 && s.BatchCeilingHits > 0:
		return "Raise --max-queue and --max-batch; discovery outran delivery and a single batch also " +
			"reached its ceiling. The tree is correct either way; the individual entries are what was lost."
	case s.QueueOverflows > 0:
		return "Raise --max-queue: discovery outran delivery, so changes were lost before a batch was " +
			"assembled. The tree is correct either way."
	default:
		return "Raise --max-batch: one batch held more changes than its ceiling. The tree is correct " +
			"either way; the individual entries are what was lost."
	}
}
