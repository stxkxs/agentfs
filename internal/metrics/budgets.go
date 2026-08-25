package metrics

import "time"

// The names of the budgets agentfs holds itself to.
const (
	// BudgetKeyToFrame times a keypress to the frame that answers it.
	BudgetKeyToFrame = "key_to_frame"
	// BudgetEventToFrame times a filesystem event to the frame that shows
	// it.
	BudgetEventToFrame = "event_to_frame"
	// BudgetScanRoot times one full walk of a watched root.
	BudgetScanRoot = "scan_root"
)

// The deadlines those budgets carry, placed against Nielsen's response-time
// limits: 100ms reads as direct manipulation, 1s keeps a train of thought
// unbroken, and the Doherty threshold at 400ms is where perceived productivity
// falls away.
//
// A keypress has to land inside the direct-manipulation limit with a frame
// still to draw, so it gets a fraction of that limit. A filesystem event
// reports a change the person did not make and may arrive perceptibly later. A
// root scan is held to Doherty because it is the longest thing a person waits
// through without having asked for it.
const (
	// DeadlineKeyToFrame is the deadline for [BudgetKeyToFrame].
	DeadlineKeyToFrame = 50 * time.Millisecond
	// DeadlineEventToFrame is the deadline for [BudgetEventToFrame].
	DeadlineEventToFrame = 250 * time.Millisecond
	// DeadlineScanRoot is the deadline for [BudgetScanRoot].
	DeadlineScanRoot = 400 * time.Millisecond
)

// DefaultBudgets registers the budgets agentfs holds itself to on r. Calling it
// twice leaves one budget per name with its record intact, so a component may
// call it to be sure the budgets exist without knowing whether the session
// already did.
func DefaultBudgets(r *Registry) {
	r.Budget(BudgetKeyToFrame, DeadlineKeyToFrame)
	r.Budget(BudgetEventToFrame, DeadlineEventToFrame)
	r.Budget(BudgetScanRoot, DeadlineScanRoot)
}
