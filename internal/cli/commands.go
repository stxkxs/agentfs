package cli

import (
	"context"
	"errors"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/buildinfo"
	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/report"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/app"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
	"github.com/stxkxs/agentfs/internal/workspace"
)

// openRoot opens the workspace, reporting a lost or unreadable root as the
// distinct exit code a caller can branch on.
//
// A machine reading the result gets it in the form it asked for: an envelope
// whose kind is [report.KindError] carries the same finding the text form
// prints, so a caller parsing standard output is not handed prose on the one
// path where the command produced no result.
func openRoot(env Env, opts Options) (*fsx.Root, report.Code) {
	root, err := fsx.Open(opts.Root())
	if err == nil {
		return root, report.CodeOK
	}
	if opts.JSON() {
		e := report.NewEnvelope(report.KindError, buildinfo.Get().Version, opts.Root(), report.CodePath)
		e.Diagnostics = []diag.Diagnostic{rootLostDiagnostic(opts.Root(), err)}
		if failed := e.WriteJSON(env.Stdout); failed != nil {
			return nil, writeErr(env, failed, report.CodePath)
		}
		return nil, report.CodePath
	}
	p := newPrinter(env.Stderr)
	p.printf("agentfs: cannot read workspace %s: %v\n", opts.Root(), err)
	return nil, report.CodePath
}

// rootLostDiagnostic names the workspace root a command could not read, with
// the hint that separates a path mistake from a mount that is away.
func rootLostDiagnostic(root string, err error) diag.Diagnostic {
	return diag.About(diag.CodeRootLost, root, "",
		"The workspace root could not be read.",
		"Check that the path exists, names a directory, and its mount is available.",
		err.Error())
}

// scanOptions builds the scanner settings from the resolved configuration.
func scanOptions(env Env, cfg config.Config) workspace.Options {
	return workspace.Options{
		Now:              env.now,
		StaleAfter:       cfg.StaleAfter,
		SkewTolerance:    cfg.SkewTolerance,
		MaxDocumentBytes: cfg.MaxDocumentBytes,
		MaxExtraBytes:    cfg.MaxExtraBytes,
		SettleReads:      1, // A one-shot command reads once, so nothing can settle.
	}
}

// runScan reports the agents a workspace declares.
func runScan(_ context.Context, env Env, opts Options) report.Code {
	root, code := openRoot(env, opts)
	if code != report.CodeOK {
		return code
	}
	defer func() { _ = root.Close() }()

	res := workspace.New(root, scanOptions(env, opts.Config)).Scan()
	exit := report.CodeOK
	if worst, raised := res.Worst(); raised && worst == diag.Error {
		exit = report.CodeFindings
	}

	p, errs := newPrinter(env.Stdout), newPrinter(env.Stderr)

	if opts.JSON() {
		e := report.NewEnvelope(report.KindScan, buildinfo.Get().Version, root.Name(), exit)
		e.Data = res
		e.Diagnostics = res.Diagnostics
		if err := e.WriteJSON(env.Stdout); err != nil {
			return writeErr(env, err, exit)
		}
		return exit
	}

	if len(res.Agents) == 0 {
		p.printf("no agents in %s\n", root.Name())
	}
	for _, a := range res.Agents {
		// A workspace names its own directories and declares its own text, so
		// what reaches the terminal from here is neutralized first. The
		// terminal interface sanitizes in its panes; this is the same boundary
		// for the one-shot commands, and it is the command the non-terminal
		// path steers a caller toward.
		p.printf("%-24s %-10s %-10s %s\n",
			textx.Sanitize(a.Name), a.Presence, a.Status(), agentSummary(a))
		for _, d := range a.Diagnostics {
			p.printf("  %s\n", d)
		}
	}
	for _, d := range res.Diagnostics {
		errs.printf("%s\n", d)
	}
	return p.finish(env, exit)
}

// agentSummary renders what an agent declared, neutralized: every member here
// is text the workspace wrote.
func agentSummary(a workspace.Agent) string {
	var parts []string
	if a.State.Task != "" {
		parts = append(parts, textx.Sanitize(a.State.Task))
	}
	if s := a.State.Step.String(); s != "" {
		parts = append(parts, "step "+textx.Sanitize(s))
	}
	if a.State.Model != "" {
		parts = append(parts, textx.Sanitize(a.State.Model))
	}
	if a.State.Problem != "" {
		parts = append(parts, "problem: "+textx.Sanitize(a.State.Problem))
	}
	return strings.Join(parts, " · ")
}

// runValidate checks every state document against the contract.
//
// It exits with the findings code when any document raises an error-severity
// diagnostic, so it drops into a pipeline as a gate rather than needing its
// output parsed.
func runValidate(_ context.Context, env Env, opts Options) report.Code {
	root, code := openRoot(env, opts)
	if code != report.CodeOK {
		return code
	}
	defer func() { _ = root.Close() }()

	scanner := workspace.New(root, scanOptions(env, opts.Config))
	docs := scanner.Documents()
	res := scanner.Scan()

	var all []diag.Diagnostic
	for _, d := range docs {
		all = append(all, d.Diagnostics...)
	}
	all = append(all, res.Diagnostics...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})

	if opts.Config.Strict {
		// A strict run holds a workspace to the whole contract: a warning is a
		// finding the gate reports rather than one it reads past.
		for i := range all {
			if all[i].Severity == diag.Warning {
				all[i].Severity = diag.Error
			}
		}
	}

	errorCount, warnCount := 0, 0
	for _, d := range all {
		switch d.Severity {
		case diag.Error:
			errorCount++
		case diag.Warning:
			warnCount++
		case diag.Info:
		}
	}
	exit := report.CodeOK
	if errorCount > 0 {
		exit = report.CodeFindings
	}
	p := newPrinter(env.Stdout)

	if opts.JSON() {
		e := report.NewEnvelope(report.KindValidate, buildinfo.Get().Version, root.Name(), exit)
		e.Data = validation{
			Schema:    agentstate.SchemaVersion,
			Documents: len(docs),
			Errors:    errorCount,
			Warnings:  warnCount,
		}
		e.Diagnostics = all
		if err := e.WriteJSON(env.Stdout); err != nil {
			return writeErr(env, err, exit)
		}
		return exit
	}

	for _, d := range all {
		p.println(d)
	}
	p.printf("%d documents · %d errors · %d warnings · contract %s\n",
		len(docs), errorCount, warnCount, agentstate.SchemaVersion)
	return p.finish(env, exit)
}

// validation is the payload of a validate result.
type validation struct {
	// Schema is the contract version the documents were read against.
	Schema string `json:"schema"`
	// Documents is the number of agent workspaces examined.
	Documents int `json:"documents"`
	// Errors and Warnings count the diagnostics by severity.
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// runSchema prints the contract.
func runSchema(_ context.Context, env Env, opts Options) report.Code {
	b, err := agentstate.SchemaJSON()
	if err != nil {
		return writeErr(env, err, report.CodeInternal)
	}
	_ = opts

	p := newPrinter(env.Stdout)
	p.write(b)
	return p.finish(env, report.CodeOK)
}

// runDoctor reports how the workspace is observed and what that costs, so an
// operator reads the answer rather than inferring it from an empty feed.
func runDoctor(_ context.Context, env Env, opts Options) report.Code {
	root, code := openRoot(env, opts)
	if code != report.CodeOK {
		return code
	}
	defer func() { _ = root.Close() }()

	filesystem := fsx.Classify(root.Name())
	mode := opts.Config.FilesystemMode(filesystem.Kind)

	ix := index.New(root, index.Limits{
		MaxDepth:         opts.Config.MaxDepth,
		MaxEntriesPerDir: opts.Config.MaxEntriesPerDir,
		MaxNodes:         opts.Config.MaxNodes,
	})
	res := workspace.New(root, scanOptions(env, opts.Config)).Scan()

	// A ceiling is only observable by reaching it, so the command whose job is
	// to report what agentfs can and cannot see in this workspace looks. The
	// walk is bounded by the ceilings it reports.
	stats := ix.Survey()

	ceilings := stats.Diagnostics()
	findings := make([]diag.Diagnostic, 0, len(res.Diagnostics)+len(ceilings))
	findings = append(findings, res.Diagnostics...)
	findings = append(findings, ceilings...)

	d := diagnosis{
		Root:            root.Name(),
		Filesystem:      filesystem.Type,
		FilesystemKind:  filesystem.Kind.String(),
		EventsComplete:  filesystem.Kind.EventsAreComplete(),
		Mode:            mode.String(),
		Agents:          len(res.Agents),
		Nodes:           stats.Nodes,
		DirectoriesRead: stats.Reads,
		TrackedDirs:     len(ix.VisibleDirs()),
		SweepBudget:     opts.Config.SweepBudget,
		SweepInterval:   opts.Config.SweepInterval.String(),
		WatchBudget:     opts.Config.MaxWatches,
		Confinement:     confinementNote(),
		SchemaVersion:   agentstate.SchemaVersion,
		OperationsPerHr: sweepCost(mode, opts.Config),
	}

	if opts.JSON() {
		e := report.NewEnvelope(report.KindDoctor, buildinfo.Get().Version, root.Name(), report.CodeOK)
		e.Data = d
		e.Diagnostics = findings
		if err := e.WriteJSON(env.Stdout); err != nil {
			return writeErr(env, err, report.CodeOK)
		}
		return report.CodeOK
	}

	p, errs := newPrinter(env.Stdout), newPrinter(env.Stderr)
	p.printf("workspace     %s\n", textx.Sanitize(d.Root))
	p.printf("filesystem    %s (%s)\n", d.Filesystem, d.FilesystemKind)
	p.printf("detection     %s\n", d.Mode)
	if !d.EventsComplete {
		p.printf("              kernel notification does not observe a write by another client of this\n")
		p.printf("              filesystem, so the tracked directories are swept every %s as well\n", d.SweepInterval)
	}
	p.printf("confinement   %s\n", d.Confinement)
	p.printf("agents        %d\n", d.Agents)
	p.printf("tracked dirs  %d (sweep budget %d per cycle)\n", d.TrackedDirs, d.SweepBudget)
	p.printf("watch budget  %d\n", d.WatchBudget)
	p.printf("contract      %s\n", d.SchemaVersion)
	if d.OperationsPerHr > 0 {
		p.printf("sweep cost    up to %d filesystem operations per hour from this process;\n", d.OperationsPerHr)
		p.printf("              N instances against one export cost N times that\n")
	}
	p.printf("tree          %d nodes from %d directory reads\n", d.Nodes, d.DirectoriesRead)
	for _, dg := range findings {
		errs.printf("%s\n", dg)
	}
	return p.finish(env, report.CodeOK)
}

// diagnosis is the payload of a doctor result.
type diagnosis struct {
	Root            string `json:"root"`
	Filesystem      string `json:"filesystem"`
	FilesystemKind  string `json:"filesystem_kind"`
	EventsComplete  bool   `json:"events_complete"`
	Mode            string `json:"mode"`
	Agents          int    `json:"agents"`
	Nodes           int    `json:"nodes"`
	DirectoriesRead int    `json:"directories_read"`
	TrackedDirs     int    `json:"tracked_dirs"`
	SweepBudget     int    `json:"sweep_budget"`
	SweepInterval   string `json:"sweep_interval"`
	WatchBudget     int    `json:"watch_budget"`
	Confinement     string `json:"confinement"`
	SchemaVersion   string `json:"schema_version"`
	OperationsPerHr int    `json:"operations_per_hour"`
}

// sweepCost is the ceiling on filesystem operations this process spends
// sweeping, which is the number an operator multiplies by the number of
// instances pointed at one shared export. A mode that does not sweep spends
// none, and reporting a figure for it would invite a capacity calculation
// against work that never happens.
func sweepCost(mode config.Mode, cfg config.Config) int {
	if cfg.SweepInterval <= 0 || !sweeps(mode) {
		return 0
	}
	cyclesPerHour := int(float64(3600) / cfg.SweepInterval.Seconds())
	return cyclesPerHour * cfg.SweepBudget
}

// runWatch draws the workspace, or streams its changes.
//
// Drawing needs a terminal. Without one there is nothing to draw, and a caller
// that piped the output wanted the changes rather than an escape stream, so
// the record stream is what a pipe gets whether or not it asked for it by
// name.
func runWatch(ctx context.Context, env Env, opts Options) report.Code {
	root, code := openRoot(env, opts)
	if code != report.CodeOK {
		return code
	}
	defer func() { _ = root.Close() }()

	obs, err := watch.New(root, watch.Options{
		Mode:         opts.Config.Watch,
		Interval:     opts.Config.SweepInterval,
		SweepBudget:  opts.Config.SweepBudget,
		MaxBatch:     opts.Config.MaxBatch,
		MaxQueue:     opts.Config.MaxQueue,
		MaxWatches:   opts.Config.MaxWatches,
		DedupTTL:     opts.Config.DedupTTL,
		RootRetryMin: opts.Config.RootRetryMin,
		RootRetryMax: opts.Config.RootRetryMax,
	})
	if err != nil {
		out := newPrinter(env.Stderr)
		out.printf("agentfs: cannot observe %s: %v\n", opts.Root(), err)
		return report.CodePath
	}
	defer func() { _ = obs.Close() }()

	if opts.NDJSON() || !env.Interactive {
		return stream(ctx, env, opts, root, obs)
	}

	registry := metrics.NewRegistry()
	metrics.DefaultBudgets(registry)

	model := app.New(app.Options{
		Root:     root,
		Observer: obs,
		Config:   opts.Config,
		Palette:  palette(env, opts.Config),
		Metrics:  registry,
		Now:      env.now,
	})

	p := tea.NewProgram(model, tea.WithContext(ctx), tea.WithOutput(env.Stdout))
	if _, err := p.Run(); err != nil {
		switch {
		case errors.Is(err, tea.ErrInterrupted), errors.Is(err, context.Canceled):
			return report.CodeInterrupted
		case errors.Is(err, tea.ErrProgramKilled):
			return report.CodeInterrupted
		}
		return writeErr(env, err, report.CodeOK)
	}
	return report.CodeOK
}

// palette resolves the colour setting against the terminal.
func palette(env Env, cfg config.Config) theme.Palette {
	p := theme.Plain()
	switch cfg.Color {
	case config.ColorNever:
		p = theme.Plain()
	case config.ColorAlways:
		p = theme.For(env.DarkBackground)
	default:
		if env.Interactive {
			p = theme.For(env.DarkBackground)
		}
	}
	if cfg.ASCII {
		p = p.WithGlyphs(theme.ASCIIGlyphs())
	}
	return p
}
