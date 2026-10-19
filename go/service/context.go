package service

import (
	"math"
	"slices"
)

// maxConversationSlots bounds the catalogue's conversation_slots field so
// every memory product below stays far from overflow. It is a validation
// bound; each entry's own slots come from the catalogue.
const maxConversationSlots = 16

// slots is the most conversations the entry keeps resident, at least one.
func (e catalogEntry) slots() int {
	return max(e.ConversationSlots, 1)
}

// needFor is the memory the entry needs with slots conversations of context
// tokens each: the engine holds context × slots tokens of KV.
func (e catalogEntry) needFor(context int64, slots int) (int64, bool) {
	if context <= 0 || slots <= 0 || context > math.MaxInt64/int64(slots) {
		return 0, false
	}
	return e.need(context * int64(slots))
}

// ContextPlan is how one curated local model runs on this machine: the
// context each conversation gets, how many conversations stay resident at
// once, and the memory that takes (decision D, 2026-11-08). An engine
// adapter maps Context and Slots onto its own flags; an inventory reports
// Context, the window one conversation actually has.
type ContextPlan struct {
	Family string
	// Context is the tokens one conversation may hold.
	Context int64
	// Slots is the number of conversations kept resident, each with Context.
	Slots int
	// NeedBytes is the Fit memory rule at Context in every slot.
	NeedBytes int64
	// Sizes are the per-conversation sizes that fit this machine with one
	// slot, smallest first: the choices a person may make. Empty when the
	// machine's memory is unknown.
	Sizes []int64
	// Automatic is the context Automatic gives on this machine.
	Automatic int64
	// Chosen is the person's size when it applies, and zero for Automatic.
	Chosen int64
}

// PlanContext resolves family's context and conversation slots from the
// compiled-in catalogue for a machine whose memory pool holds capacity
// bytes. chosen is the person's per-conversation size, zero for Automatic.
//
// Automatic is the trained maximum when it fits with one slot, else the
// largest size that does. A second slot is kept only while memory still
// allows the full context in each, so raising the context lowers the slots to
// one before a size is refused; a chosen size that does not fit with one slot
// reads as Automatic. With capacity unknown (zero or less) the plan is the
// use's catalogue context in every slot, the measured default profile, and a
// chosen size runs in one slot. ok is false for a family the catalogue does
// not curate.
func PlanContext(family string, capacity, chosen int64) (ContextPlan, bool) {
	c, err := parseCatalog(catalogDocument)
	if err != nil {
		return ContextPlan{}, false
	}
	return c.planContext(family, capacity, chosen)
}

// ContextNeed is the Fit memory rule for family at context tokens in each of
// slots resident conversations: the weights, the KV cache of context × slots
// tokens, the startup residual and the margin. An engine leases this for the
// load (abstraction.resource RES-L9); ok is false for a family the catalogue
// does not curate or a size that overflows.
func ContextNeed(family string, context int64, slots int) (int64, bool) {
	c, err := parseCatalog(catalogDocument)
	if err != nil {
		return 0, false
	}
	entry, _, found := c.contextEntry(family)
	if !found {
		return 0, false
	}
	return entry.needFor(context, slots)
}

func (c catalog) planContext(family string, capacity, chosen int64) (ContextPlan, bool) {
	entry, base, found := c.contextEntry(family)
	if !found {
		return ContextPlan{}, false
	}
	plan := ContextPlan{Family: family}
	candidates := contextSizes(base, entry.TrainedContext)
	fits := func(context int64, slots int) bool {
		need, ok := entry.needFor(context, slots)
		return ok && need <= capacity
	}
	if capacity <= 0 {
		plan.Automatic, plan.Context, plan.Slots = base, base, entry.slots()
		if chosen != base && slices.Contains(candidates, chosen) {
			plan.Context, plan.Slots, plan.Chosen = chosen, 1, chosen
		}
		plan.NeedBytes, _ = entry.needFor(plan.Context, plan.Slots)
		return plan, true
	}
	for _, size := range candidates {
		if fits(size, 1) {
			plan.Sizes = append(plan.Sizes, size)
		}
	}
	plan.Automatic = base
	if len(plan.Sizes) > 0 {
		plan.Automatic = plan.Sizes[len(plan.Sizes)-1]
	}
	plan.Context = plan.Automatic
	if chosen > 0 && slices.Contains(plan.Sizes, chosen) {
		plan.Context, plan.Chosen = chosen, chosen
	}
	plan.Slots = 1
	for n := entry.slots(); n > 1; n-- {
		if fits(plan.Context, n) {
			plan.Slots = n
			break
		}
	}
	plan.NeedBytes, _ = entry.needFor(plan.Context, plan.Slots)
	return plan, true
}

// contextEntry is the ungated entry of family and the smallest context any use
// listing it asks for, which is where its sizes start.
func (c catalog) contextEntry(family string) (catalogEntry, int64, bool) {
	var found catalogEntry
	var base int64
	for use, ids := range c.Uses {
		for _, id := range ids {
			e := c.entries[id]
			if e.Gated || e.family != family {
				continue
			}
			if window := c.Context[use]; base == 0 || window < base {
				found, base = e, window
			}
		}
	}
	return found, base, base > 0
}

// contextSizes are base doubled up to trained, then trained itself: the
// per-conversation sizes a person chooses from.
func contextSizes(base, trained int64) []int64 {
	if trained < base {
		trained = base
	}
	sizes := []int64{}
	for size := base; size < trained && size <= math.MaxInt64/2; size *= 2 {
		sizes = append(sizes, size)
	}
	return append(sizes, trained)
}
