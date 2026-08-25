package workspace

// SettleMemory reports how many documents the scanner holds a reading for. The
// settle rule needs one reading per document the workspace declares, so this
// count is a property of the workspace rather than of how long the session has
// been running.
func (s *Scanner) SettleMemory() int { return len(s.seen) }
