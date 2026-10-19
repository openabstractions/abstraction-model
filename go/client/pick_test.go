package client

import (
	"testing"

	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
)

// One pick rule (architect review 25): a paired runtime serving the
// strongest entry makes it neither the pick nor the smallest local need;
// the pick is the first entry this computer serves or fits.
func TestLocalPickSkipsAPairedEntryRankedFirst(t *testing.T) {
	r := wire.FitResult{Outcome: wire.FitOutcomeFitted, PoolBytes: 30e9, Entries: []wire.FitEntry{
		{Title: "Paired", Family: "big", Fit: wire.FitNo, Availability: wire.AvailabilityServed, Machine: "studio", NeedBytes: 1},
		{Title: "Too big", Family: "large", Fit: wire.FitNo, Availability: wire.AvailabilityDownload, NeedBytes: 60e9},
		{Title: "Middle", Family: "middle", Fit: wire.FitFits, Availability: wire.AvailabilityDownload, NeedBytes: 25e9},
		{Title: "Small", Family: "small", Fit: wire.FitFits, Availability: wire.AvailabilityDownload, NeedBytes: 6e9},
	}}
	if pick, smallest := LocalPick(r); pick != 2 || smallest != 3 {
		t.Fatalf("pick %d smallest %d", pick, smallest)
	}
	r.Entries[1].Availability = wire.AvailabilityServed
	if pick, _ := LocalPick(r); pick != 1 {
		t.Fatalf("served here = %d", pick)
	}
	r.Entries = r.Entries[:1]
	if pick, smallest := LocalPick(r); pick != -1 || smallest != -1 {
		t.Fatalf("paired only = %d %d", pick, smallest)
	}
	if pick, smallest := LocalPick(wire.FitResult{Outcome: wire.FitOutcomeForbidden}); pick != -1 || smallest != -1 {
		t.Fatalf("refused = %d %d", pick, smallest)
	}
}
