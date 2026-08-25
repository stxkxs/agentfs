package app

import (
	"time"

	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
)

// ScopeForTest returns the scope a press resolves in, so a test that means to
// exercise one pane's keys can require the fixture to have left that pane in
// front instead of assuming it. A fixture that lands on the wrong pane
// exercises the wrong table and passes on whatever that pane happens to answer.
func (m *Model) ScopeForTest() keys.Scope { return m.scope() }

// BatchMsgForTest wraps a batch as the message the observer command produces,
// so a test drives the model through the same path the runtime does rather than
// reaching into its state.
func BatchMsgForTest(b watch.Batch) any { return batchMsg(b) }

// BatchWithStatsForTest wraps observer statistics as a batch message, so a test
// asserts what the status line renders for a condition rather than contriving
// the condition itself.
func BatchWithStatsForTest(s watch.Stats) any { return batchMsg(watch.Batch{Stats: s}) }

// IsTickMsgForTest reports whether a message is the recurring tick. A harness
// drops it: the tick re-arms itself, so delivering it would keep a test running
// without changing what the frame shows.
func IsTickMsgForTest(m any) bool {
	_, ok := m.(tickMsg)
	return ok
}

// RescanIntervalForTest is how long a harness advances the clock by to let the
// pending tick fire, so no command is left outstanding when a test ends.
const RescanIntervalForTest = rescanInterval

// SelectedPathForTest returns the path under the tree cursor, so a harness can
// walk to a file without reimplementing the tree's navigation.
func (m *Model) SelectedPathForTest() string {
	if n, ok := m.tree.Selected(m.index); ok {
		return n.Path
	}
	return ""
}

// TickMsgForTest is the recurring tick, so a test can drive the clock-driven
// path a quiet workspace depends on.
func TickMsgForTest(at time.Time) any { return tickMsg(at) }

// Palette returns the palette the model renders with, so a test can assert
// against the glyph a condition renders as rather than against a colour.
func (m *Model) Palette() theme.Palette { return m.pal }

// ObserverDiagnosticsForTest raises the observer's and the index's conditions
// from statistics a test supplies, so the mapping from a condition to a code is
// exercised without contriving an exhausted kernel watch budget.
func ObserverDiagnosticsForTest(ws watch.Stats, ix index.Stats, recoveries uint64) []diag.Diagnostic {
	return observerDiagnostics(ws, ix, recoveries)
}
