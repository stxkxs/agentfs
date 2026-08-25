//go:build !windows

package cli

import (
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/ui/app"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
)

// The terminal command wires a real observer to a real model, and nothing else
// in the suite does. Building that pair is where a blocking observer would hang
// before anything was drawn, so it is exercised here rather than left to the
// first person who runs the binary.
func TestTheTerminalModelBuildsAgainstARealObserver(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root, err := fsx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	cfg := config.Defaults()
	cfg.Root = dir

	built := make(chan *app.Model, 1)
	go func() {
		obs, obsErr := watch.New(root, watch.Options{
			Mode:     cfg.Watch,
			Interval: cfg.SweepInterval,
		})
		if obsErr != nil {
			built <- nil
			return
		}
		t.Cleanup(func() { _ = obs.Close() })

		registry := metrics.NewRegistry()
		metrics.DefaultBudgets(registry)
		built <- app.New(app.Options{
			Root:     root,
			Observer: obs,
			Config:   cfg,
			Palette:  theme.Plain(),
			Metrics:  registry,
		})
	}()

	select {
	case m := <-built:
		if m == nil {
			t.Fatal("the observer could not be created")
		}
		if got := m.SchemaVersion(); got == "" {
			t.Fatal("the model reports no contract version")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("building the terminal model against a real observer blocked")
	}
}
