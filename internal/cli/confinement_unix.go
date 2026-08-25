//go:build !windows

package cli

// confinementNote states what the confined root guarantees on this platform.
// The property is asserted by a test on linux and darwin, so the note is a
// claim the suite backs rather than an assurance.
func confinementNote() string {
	return "os.Root — a path that resolves outside the workspace is refused"
}
