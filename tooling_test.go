package main_test

import (
	"regexp"
	"testing"
)

// A tool's version is pinned in two files: the workflow installs it and the
// Taskfile checks for it, so a local run and a pipeline run ask the same
// question of the same release. Nothing keeps the two numbers together — an
// update lands in whichever file its manager knows about — and a pair that has
// drifted still passes the pipeline, because the pipeline only ever reads its
// own half. The mismatch surfaces on the contributor's machine instead, as a
// gate that refuses to run or a finding the pipeline never reported.
func TestEveryPinnedToolCarriesOneVersion(t *testing.T) {
	t.Parallel()

	workflow := read(t, ".github/workflows/ci.yml")
	taskfile := read(t, "Taskfile.yml")

	tools := []struct {
		tool     string
		workflow *regexp.Regexp
		taskfile *regexp.Regexp
	}{
		{
			tool:     "golangci-lint",
			workflow: regexp.MustCompile(`golangci-lint-action@[0-9a-f]+ # [^\n]*\n\s*with:\n\s*version:\s*(\S+)`),
			taskfile: regexp.MustCompile(`LINT_VERSION:\s*(\S+)`),
		},
		{
			tool:     "govulncheck",
			workflow: regexp.MustCompile(`golang\.org/x/vuln/cmd/govulncheck@(\S+)`),
			taskfile: regexp.MustCompile(`golang\.org/x/vuln/cmd/govulncheck@([^"\s]+)`),
		},
	}

	for _, tc := range tools {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			inWorkflow := sole(t, tc.workflow, workflow, ".github/workflows/ci.yml")
			inTaskfile := sole(t, tc.taskfile, taskfile, "Taskfile.yml")
			if inWorkflow != inTaskfile {
				t.Errorf("the workflow pins %s %s and the Taskfile pins %s",
					tc.tool, inWorkflow, inTaskfile)
			}
			if inWorkflow == "latest" {
				t.Errorf("%s is pinned to latest, so the gate reports what an unreviewed "+
					"build decided rather than what this tree was checked against", tc.tool)
			}
		})
	}
}

// sole returns the single capture the pattern makes, failing when a file holds
// none — a pattern that matches nothing would let this gate pass by asking
// nothing.
func sole(t *testing.T, pattern *regexp.Regexp, text, name string) string {
	t.Helper()
	all := pattern.FindAllStringSubmatch(text, -1)
	if len(all) == 0 {
		t.Fatalf("%s holds no version matching %s", name, pattern)
	}
	first := all[0][1]
	for _, m := range all[1:] {
		if m[1] != first {
			t.Fatalf("%s pins both %s and %s", name, first, m[1])
		}
	}
	return first
}
