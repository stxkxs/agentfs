package app_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/watch"
)

// A budget nobody records is a deadline the build states and never measures
// itself against. Every budget [metrics.DefaultBudgets] registers is therefore
// required to carry observations after a session that pressed a key, took a
// change batch and walked the root.
func TestEveryRegisteredBudgetIsRecorded(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		h.key("j")
		h.emit(watch.Change{Path: "agent-writer/state.json", Op: watch.OpModify})

		got := h.metrics.Budgets()
		if len(got) == 0 {
			h.t.Fatal("the registry holds no budgets")
		}
		for _, b := range got {
			if b.Count == 0 {
				h.t.Errorf("%s carries no observation: the path it names records nothing", b.Name)
			}
		}
	})
}

// The root scan is the longest thing a person waits through without having
// asked for it, so it is measured — and this asserts that it is measured, and
// against the deadline the reference publishes.
//
// It deliberately does not assert the deadline was met. The model runs inside a
// synctest bubble where the clock advances only when every goroutine blocks, so
// a scan takes zero virtual nanoseconds and any comparison against a deadline
// is true however slow the real code becomes. A gate that holds at a
// one-nanosecond budget measures nothing; whether Met discriminates is a
// property of the budget itself, asserted in internal/metrics.
func TestScanningTheRootIsMeasuredAgainstItsPublishedDeadline(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		for _, b := range h.metrics.Budgets() {
			if b.Name != metrics.BudgetScanRoot {
				continue
			}
			if b.Count == 0 {
				h.t.Fatalf("%s carries no observation, so nothing times the scan", b.Name)
			}
			if b.Deadline != metrics.DeadlineScanRoot {
				h.t.Errorf("%s is held to %v, and the reference publishes %v",
					b.Name, b.Deadline, metrics.DeadlineScanRoot)
			}
			return
		}
		h.t.Fatalf("%s is not registered", metrics.BudgetScanRoot)
	})
}

// TestEveryBudgetIsPublished requires the overlay to name every budget
// [metrics.DefaultBudgets] registers.
//
// A budget the session records and no surface shows leaves an operator unable
// to learn whether agentfs met the response times it publishes. The names come
// from a registry built here rather than from a list restated here, so adding a
// budget extends the requirement without touching this test, and dropping one
// from the render path fails it.
func TestEveryBudgetIsPublished(t *testing.T) {
	t.Parallel()

	declared := metrics.NewRegistry()
	metrics.DefaultBudgets(declared)
	held := declared.Budgets()
	if len(held) == 0 {
		t.Fatal("DefaultBudgets registered nothing, so the requirement covers nothing")
	}

	run(t, workspaceFixture(), standard, func(h *harness) {
		h.key(budgetKey)
		frame := h.frame()
		for _, b := range held {
			if !strings.Contains(frame, b.Name) {
				h.t.Errorf("%s is registered and the overlay does not publish it:\n%s", b.Name, frame)
			}
		}
	})
}

// The overlay reports this session's record rather than a table of zeroes, so
// each budget's row carries the count the registry holds for it.
func TestThePublishedBudgetsCarryWhatTheSessionSpent(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		h.key("j")
		h.emit(watch.Change{Path: "agent-writer/state.json", Op: watch.OpModify})

		h.key(budgetKey)
		lines := h.lines()
		for _, b := range h.metrics.Budgets() {
			if b.Count == 0 {
				h.t.Fatalf("%s carries no observation, so the overlay has nothing to report", b.Name)
			}
			row := rowNaming(h, lines, b.Name)
			if !strings.Contains(row, strconv.FormatInt(b.Count, 10)) {
				h.t.Errorf("%s recorded %d observations and its row reports %q", b.Name, b.Count, row)
			}
		}
	})
}

// rowNaming returns the one rendered row naming want, failing when the overlay
// shows it nowhere or on more than one row.
func rowNaming(h *harness, lines []string, want string) string {
	h.t.Helper()
	var found []string
	for _, line := range lines {
		if strings.Contains(line, want) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		h.t.Fatalf("%s appears on %d rows of the overlay:\n%s", want, len(found), strings.Join(lines, "\n"))
	}
	return found[0]
}

// The overlay is a mode, so the key that opens it closes it and leaves the
// browse panes where they were.
func TestTheBudgetOverlayClosesOnTheKeyThatOpenedIt(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		before := h.frame()
		h.key(budgetKey)
		if opened := h.frame(); opened == before {
			h.t.Fatalf("the budget key changed nothing:\n%s", opened)
		}
		h.key(budgetKey)
		if got := h.frame(); got != before {
			h.t.Errorf("closing the overlay did not restore the frame:\n%s", got)
		}
	})
}

// Cancelling leaves the overlay, which is the gesture an operator reaches for
// without knowing which key opened it.
func TestCancelLeavesTheBudgetOverlay(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		before := h.frame()
		h.key(budgetKey)
		h.key("esc")
		if got := h.frame(); got != before {
			h.t.Errorf("cancelling the overlay did not restore the frame:\n%s", got)
		}
	})
}

// budgetKey opens the overlay. It is the spelling the shipped table binds to
// the action, resolved rather than restated, so a rebinding moves these tests
// with it.
var budgetKey = spellingOf(keys.ActionToggleBudgets)

// spellingOf returns the first global spelling bound to a, or the empty string
// when nothing binds it — which a press then leaves unanswered, failing the
// test that pressed it.
func spellingOf(a keys.Action) string {
	for _, b := range keys.Default().Bindings() {
		if b.Action == a && b.Scope == keys.ScopeGlobal && len(b.Keys) > 0 {
			return b.Keys[0]
		}
	}
	return ""
}
