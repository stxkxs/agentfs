package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stxkxs/agentfs/internal/fileview"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/watch"
)

// waitForBatch reads one batch from the observer.
//
// Exactly one of these is outstanding at any moment: [Model.Init] issues the
// first, and every batch message issues the next. Two would race for the
// channel and deliver batches to the model out of order.
func (m *Model) waitForBatch() tea.Cmd {
	obs := m.obs
	if obs == nil {
		return nil
	}
	return func() tea.Msg {
		b, ok := <-obs.Batches()
		if !ok {
			return nil
		}
		return batchMsg(b)
	}
}

// readPending reads every directory the index is waiting for, off the update
// goroutine. A read of a network export blocks for as long as the mount takes,
// and doing it here means the frame stays responsive while it does.
func (m *Model) readPending() tea.Cmd {
	reqs := m.index.Pending()
	if len(reqs) == 0 {
		if m.obs != nil {
			m.obs.Track(m.index.VisibleDirs())
		}
		return nil
	}

	ix := m.index
	cmds := make([]tea.Cmd, 0, len(reqs))
	for _, p := range reqs {
		cmds = append(cmds, func() tea.Msg { return loadedMsg(ix.Read(p)) })
	}
	return tea.Batch(cmds...)
}

// loadPreview reads a bounded window of one file.
func (m *Model) loadPreview(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	root, cfg := m.root, m.cfg
	return func() tea.Msg {
		w := fileview.Load(root, path, fileview.Options{
			MaxBytes:   cfg.MaxPreviewBytes,
			Tail:       tailByDefault(path),
			RedactKeys: cfg.RedactKeys,
		})
		return previewMsg{path: path, window: w}
	}
}

// tailByDefault reports whether a file is one a reader wants the end of. A log
// is read at its end; a state document is read at its start.
func tailByDefault(path string) bool {
	return fileview.DetectKind(path, nil) == fileview.KindLog
}

// rescan rebuilds the agent bar. The walk of the root is what
// [metrics.BudgetScanRoot] names, and it is timed inside the command rather
// than around it: the command runs off the update goroutine, so timing the call
// that returns it would measure the frame that dispatched the work instead of
// the work.
func (m *Model) rescan() tea.Cmd {
	scanner, now := m.scanner, m.now
	budget := m.metrics.Budget(metrics.BudgetScanRoot, metrics.DeadlineScanRoot)
	return func() tea.Msg {
		done := budget.Record(now)
		defer done()
		return scanMsg(scanner.Scan())
	}
}

// loadRuns reads the runs recorded under one agent.
func (m *Model) loadRuns(agent string) tea.Cmd {
	scanner := m.scanner
	return func() tea.Msg { return runsMsg{agent: agent, runs: scanner.Runs(agent)} }
}

// tick drives recency decay and the periodic rescan that reports a document
// which went stale without anything being written.
func tick() tea.Cmd {
	return tea.Tick(rescanInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Observer returns the change source the model reads, for a caller that must
// close it.
func (m *Model) Observer() watch.Observer { return m.obs }

// Index returns the workspace tree, for a caller assembling a report from the
// same state the frame renders.
func (m *Model) Index() *index.Index { return m.index }
