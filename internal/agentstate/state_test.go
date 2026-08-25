package agentstate_test

import (
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// The profile decides whether the alias table and the compatibility member
// names apply, so output that names the profile tells a reader which rules
// their document was read under.
func TestProfileNamesTheRulesItReadsUnder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		profile agentstate.Profile
		want    string
	}{
		{agentstate.ProfileV1, agentstate.SchemaVersion},
		{agentstate.ProfileCompat, "compat"},
		{agentstate.Profile(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.profile.String(); got != tc.want {
			t.Errorf("Profile(%d) renders as %q, want %q", tc.profile, got, tc.want)
		}
	}
}

// The wire form declares a heartbeat in seconds and the decoded form holds a
// duration, so a document that declares a fractional heartbeat keeps it.
func TestHeartbeatRoundTripsThroughTheWireForm(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{"schema":"agentfs/v1","status":"running","heartbeat_seconds":2.5}`)
	for _, d := range ds {
		t.Errorf("unexpected diagnostic: %s", d)
	}
	if st.Heartbeat != 2500*time.Millisecond {
		t.Errorf("heartbeat = %v, want 2.5s", st.Heartbeat)
	}
	if got := st.HeartbeatSeconds(); got != 2.5 {
		t.Errorf("HeartbeatSeconds = %v, want 2.5", got)
	}
}
