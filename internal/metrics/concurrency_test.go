package metrics_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/metrics"
)

func TestRegistryUnderConcurrentUse(t *testing.T) {
	t.Parallel()
	const (
		workers = 8
		rounds  = 400
	)
	r := metrics.NewRegistry()
	metrics.DefaultBudgets(r)

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One clock per goroutine, so the spans are exact without the
			// workers sharing state the registry does not own.
			clk := newFakeClock()
			key := r.Budget(metrics.BudgetKeyToFrame, metrics.DeadlineKeyToFrame)
			scan := r.Budget(metrics.BudgetScanRoot, metrics.DeadlineScanRoot)
			for i := range rounds {
				stop := key.Record(clk.now)
				clk.advance(time.Duration(i%4) * time.Millisecond)
				stop()
				scan.Observe(time.Duration(w) * time.Millisecond)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range rounds {
			for _, b := range r.Budgets() {
				if b.Breached > b.Count || b.P50 > b.Max {
					t.Errorf("torn budget record: %+v", b)
					return
				}
			}
		}
	}()
	wg.Wait()

	if got := r.Budgets(); len(got) != 3 {
		t.Fatalf("Budgets() holds %d budgets, want 3", len(got))
	}
	for _, name := range []string{metrics.BudgetKeyToFrame, metrics.BudgetScanRoot} {
		stats := r.Budget(name, time.Hour).Stats()
		if want := int64(workers * rounds); stats.Count != want {
			t.Errorf("%s Count = %d, want %d", name, stats.Count, want)
		}
	}
}

func TestConcurrentFirstUseYieldsOneInstrumentPerName(t *testing.T) {
	t.Parallel()
	const workers = 32
	r := metrics.NewRegistry()
	budgets := make([]*metrics.Budget, workers)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			budgets[i] = r.Budget(nameBeta, time.Duration(i)*time.Millisecond)
		}()
	}
	close(start)
	wg.Wait()

	for i := 1; i < workers; i++ {
		if budgets[i] != budgets[0] {
			t.Fatalf("worker %d received a second budget for %q", i, nameBeta)
		}
	}
	if got := r.Budgets(); len(got) != 1 {
		t.Fatalf("Budgets() holds %d budgets, want one", len(got))
	}
}
