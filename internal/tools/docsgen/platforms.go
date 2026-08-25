package main

import (
	"strings"
)

// platform is one target and what the suite asserts on it.
//
// The rows state what CI checks rather than what the code is expected to do.
// A property compiled but never exercised is reported as compiled, because a
// support matrix that reads as a guarantee is the same over-claim as a
// filesystem list beside the word "watch".
type platform struct {
	// name is the target.
	name string
	// runner is the workflow label whose job asserts this row's test columns,
	// empty for a row that claims no test run. A row naming a runner the
	// workflow does not carry stops the generator, so the matrix cannot claim
	// a platform CI stopped covering.
	runner string
	// builds reports whether CI compiles for it.
	builds bool
	// tests reports whether CI runs the suite on it.
	tests bool
	// race reports whether that run is under the race detector.
	race bool
	// confinement reports whether the symlink-confinement property is
	// asserted there.
	confinement bool
	// note states anything the columns cannot.
	note string
}

var platforms = []platform{
	{
		name: "linux/amd64", runner: "ubuntu-latest",
		builds: true, tests: true, race: true, confinement: true,
		note: "The reference target. `os.Root` refuses a path that resolves outside the workspace, and " +
			"`TestSymlinkEscapeIsRefused` asserts it against a real filesystem.",
	},
	{
		name: "linux/arm64", runner: "ubuntu-24.04-arm",
		builds: true, tests: true, race: true, confinement: true,
		note: "The same kernel interfaces as linux/amd64; the suite runs here so the claim is asserted " +
			"per architecture rather than per operating system.",
	},
	{
		name: "darwin/arm64", runner: "macos-latest",
		builds: true, tests: true, race: true, confinement: true,
		note: "Kernel notification is kqueue rather than inotify; the watch budget is a per-process " +
			"descriptor limit rather than a per-user watch limit.",
	},
	{
		name: "darwin/amd64", runner: "macos-13",
		builds: true, tests: true, race: true, confinement: true,
		note: "The same kernel interfaces as darwin/arm64.",
	},
	{
		name: "windows/amd64, windows/arm64", runner: "",
		builds: true, tests: false, race: false, confinement: false,
		note: "Compiled, not exercised. `os.Root` resolves reparse points under rules this suite does " +
			"not check, so the confinement property is not asserted here. `agentfs doctor` says so on " +
			"the running binary.",
	},
}

// renderPlatforms writes the support matrix.
func renderPlatforms(b *strings.Builder, m module) error {
	title(b, "Platforms",
		"What continuous integration actually does on each target. A column says yes only where a job "+
			"checks it, so a property that is merely expected to hold reads as no.")

	para(b, "Building agentfs needs Go "+m.goVersion+" or later.")

	t := newTable("Target", "Builds", "Suite runs", "Race detector", "Confinement asserted")
	for _, p := range platforms {
		t.row(p.name, yesNo(p.builds), yesNo(p.tests), yesNo(p.race), yesNo(p.confinement))
	}
	if err := t.write(b); err != nil {
		return err
	}

	section(b, "What each target carries")
	for _, p := range platforms {
		subsection(b, p.name)
		para(b, p.note)
	}

	section(b, "Change detection by platform")
	para(b, "The mechanism is chosen from the filesystem rather than from the operating system. "+
		"`agentfs doctor` reports what a root was probed as and which mode it resolved to, and "+
		"[change-detection.md](../explanation/change-detection.md) explains what each observes.")

	t = newTable("Platform", "Kernel notification", "Filesystem probe")
	t.row("Linux", "inotify, bounded by the per-user watch limit", "statfs superblock magic")
	t.row("macOS", "kqueue, bounded by the per-process descriptor limit", "statfs filesystem name")
	t.row("Other", "whatever the notification library provides", "unprobed, so the conservative mode is chosen")
	return t.write(b)
}
