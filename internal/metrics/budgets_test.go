package metrics_test

import (
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/metrics"
)

func TestDefaultBudgetsRegistersTheNamedDeadlines(t *testing.T) {
	t.Parallel()
	r := metrics.NewRegistry()
	metrics.DefaultBudgets(r)

	want := []metrics.BudgetStats{
		{Name: metrics.BudgetEventToFrame, Deadline: metrics.DeadlineEventToFrame},
		{Name: metrics.BudgetKeyToFrame, Deadline: metrics.DeadlineKeyToFrame},
		{Name: metrics.BudgetScanRoot, Deadline: metrics.DeadlineScanRoot},
	}
	got := r.Budgets()
	if len(got) != len(want) {
		t.Fatalf("Budgets = %+v, want %d of them", got, len(want))
	}
	for i, w := range want {
		if got[i].Name != w.Name || got[i].Deadline != w.Deadline {
			t.Errorf("Budgets[%d] = %s/%v, want %s/%v", i, got[i].Name, got[i].Deadline, w.Name, w.Deadline)
		}
		if got[i].Count != 0 {
			t.Errorf("Budgets[%d] arrived with %d observations", i, got[i].Count)
		}
	}
}

func TestDefaultBudgetsLeavesAnExistingRecordAlone(t *testing.T) {
	t.Parallel()
	r := metrics.NewRegistry()
	metrics.DefaultBudgets(r)
	r.Budget(metrics.BudgetScanRoot, metrics.DeadlineScanRoot).Observe(time.Second)

	metrics.DefaultBudgets(r)

	if got := r.Budgets(); len(got) != 3 {
		t.Fatalf("Budgets() = %+v, want 3: a second registration duplicated a name", got)
	}
	scan := r.Budget(metrics.BudgetScanRoot, metrics.DeadlineScanRoot).Stats()
	if scan.Count != 1 || scan.Breached != 1 {
		t.Errorf("scan_root = %+v, want its one breached observation intact", scan)
	}
}

// TestDeadlinesRankByHowDirectlyThePathAnswersTheUser holds the deadlines to
// the reasoning that sets them: a keypress is answered inside the
// direct-manipulation limit, a filesystem event may arrive perceptibly later,
// and a root scan is allowed the Doherty threshold and no more.
func TestDeadlinesRankByHowDirectlyThePathAnswersTheUser(t *testing.T) {
	t.Parallel()
	const (
		directManipulation = 100 * time.Millisecond
		doherty            = 400 * time.Millisecond
	)
	if metrics.DeadlineKeyToFrame >= directManipulation {
		t.Errorf("DeadlineKeyToFrame = %v, want less than %v", metrics.DeadlineKeyToFrame, directManipulation)
	}
	if metrics.DeadlineEventToFrame <= metrics.DeadlineKeyToFrame {
		t.Errorf("DeadlineEventToFrame = %v, want more than %v", metrics.DeadlineEventToFrame, metrics.DeadlineKeyToFrame)
	}
	if metrics.DeadlineScanRoot != doherty {
		t.Errorf("DeadlineScanRoot = %v, want the Doherty threshold %v", metrics.DeadlineScanRoot, doherty)
	}
	if metrics.DeadlineEventToFrame >= metrics.DeadlineScanRoot {
		t.Errorf("DeadlineEventToFrame = %v, want less than %v", metrics.DeadlineEventToFrame, metrics.DeadlineScanRoot)
	}
}

func TestBudgetNamesAreDistinct(t *testing.T) {
	t.Parallel()
	names := map[string]bool{}
	for _, n := range []string{metrics.BudgetKeyToFrame, metrics.BudgetEventToFrame, metrics.BudgetScanRoot} {
		if n == "" {
			t.Error("a budget name is empty")
		}
		if names[n] {
			t.Errorf("budget name %q is used twice", n)
		}
		names[n] = true
	}
}
