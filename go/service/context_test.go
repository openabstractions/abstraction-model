package service

import (
	"slices"
	"testing"
)

const qwen = "qwen3.6-35b-a3b"

// qwenNeed is the Fit rule for Qwen3.6 35B with tokens of KV in total:
// weights, the startup residual, the 1 GiB margin and 22528 bytes per token.
func qwenNeed(tokens int64) int64 {
	return rungOneBytes + rungOneStartupResidual + 1<<30 + 22528*tokens
}

func TestPlanContextAutomaticOnLargeMachine(t *testing.T) {
	// A 128 GB gfx1151 machine: the card's pool is carved from memory.
	plan, ok := PlanContext(qwen, 128<<30, 0)
	if !ok {
		t.Fatal("curated family has no plan")
	}
	if plan.Context != 262144 || plan.Slots != 2 || plan.Automatic != 262144 || plan.Chosen != 0 {
		t.Fatalf("automatic plan %+v, want the trained 262144 in two slots", plan)
	}
	if plan.NeedBytes != qwenNeed(2*262144) {
		t.Fatalf("need %d, want %d", plan.NeedBytes, qwenNeed(2*262144))
	}
	if want := []int64{32768, 65536, 131072, 262144}; !slices.Equal(plan.Sizes, want) {
		t.Fatalf("sizes %v, want %v", plan.Sizes, want)
	}
}

func TestPlanContextOffersOnlySizesThatFit(t *testing.T) {
	capacity := qwenNeed(65536) // 65536 fits with one slot; 131072 does not.
	plan, _ := PlanContext(qwen, capacity, 0)
	if want := []int64{32768, 65536}; !slices.Equal(plan.Sizes, want) {
		t.Fatalf("sizes %v, want %v", plan.Sizes, want)
	}
	if plan.Automatic != 65536 || plan.Context != 65536 || plan.Slots != 1 {
		t.Fatalf("automatic plan %+v, want 65536 in one slot", plan)
	}
	// A size that does not fit reads as Automatic.
	refused, _ := PlanContext(qwen, capacity, 262144)
	if refused.Context != 65536 || refused.Chosen != 0 {
		t.Fatalf("unfitting choice applied: %+v", refused)
	}
	// The smaller size keeps the second slot it still has room for.
	small, _ := PlanContext(qwen, capacity, 32768)
	if small.Context != 32768 || small.Slots != 2 || small.Chosen != 32768 {
		t.Fatalf("32768 plan %+v, want two slots", small)
	}
}

func TestPlanContextRaisingContextLowersSlotsFirst(t *testing.T) {
	// 131072 fits twice and 262144 fits once.
	capacity := qwenNeed(2*131072) + 1<<30
	low, _ := PlanContext(qwen, capacity, 131072)
	if low.Context != 131072 || low.Slots != 2 {
		t.Fatalf("131072 plan %+v, want two slots", low)
	}
	high, _ := PlanContext(qwen, capacity, 262144)
	if high.Context != 262144 || high.Slots != 1 || high.Chosen != 262144 {
		t.Fatalf("262144 plan %+v, want one slot before refusal", high)
	}
	if !slices.Contains(high.Sizes, 262144) {
		t.Fatalf("sizes %v leave out the size that fits in one slot", high.Sizes)
	}
}

func TestPlanContextUnknownMemoryKeepsMeasuredProfile(t *testing.T) {
	plan, _ := PlanContext(qwen, 0, 0)
	if plan.Context != 32768 || plan.Slots != 2 || len(plan.Sizes) != 0 || plan.NeedBytes != rungOneNeed {
		t.Fatalf("unknown-memory plan %+v", plan)
	}
	chosen, _ := PlanContext(qwen, 0, 131072)
	if chosen.Context != 131072 || chosen.Slots != 1 {
		t.Fatalf("unknown-memory choice %+v, want one slot", chosen)
	}
	if _, ok := PlanContext("no-such-family", 128<<30, 0); ok {
		t.Fatal("uncurated family planned")
	}
}

func TestContextSizes(t *testing.T) {
	if got := contextSizes(32768, 100000); !slices.Equal(got, []int64{32768, 65536, 100000}) {
		t.Fatalf("sizes %v", got)
	}
	if got := contextSizes(32768, 0); !slices.Equal(got, []int64{32768}) {
		t.Fatalf("untrained sizes %v", got)
	}
}
