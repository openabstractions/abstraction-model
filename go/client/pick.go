package client

import wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"

// LocalPick is the one pick rule for a fitted answer on this computer
// (first-run DECISION section 4): pick is the index of the first entry in
// rank order that this computer serves or that fits here, and smallest the
// index of the entry this computer could run with the least need. An entry
// a paired runtime serves is neither: a remote candidate enters a selection
// only by the person's choice. Each is -1 when there is none. The setup
// step, the runtime's RequestSetup check and the gateway's oa_setup_fit all
// read Fit through it.
func LocalPick(r FitResult) (pick, smallest int) {
	pick, smallest = -1, -1
	if r.Outcome != wire.FitOutcomeFitted {
		return pick, smallest
	}
	for i, e := range r.Entries {
		if e.Machine != "" {
			continue
		}
		if smallest < 0 || e.NeedBytes < r.Entries[smallest].NeedBytes {
			smallest = i
		}
		if pick < 0 && (e.Availability == wire.AvailabilityServed || e.Fit == wire.FitFits) {
			pick = i
		}
	}
	return pick, smallest
}
