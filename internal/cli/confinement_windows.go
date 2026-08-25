//go:build windows

package cli

// confinementNote states what the confined root guarantees on this platform.
//
// Windows is a compile target rather than an asserted one: reparse points
// resolve differently and the confinement test does not run there, so the note
// says so rather than implying a guarantee the suite has not checked.
func confinementNote() string {
	return "os.Root — windows semantics differ and confinement is not asserted on this platform"
}
