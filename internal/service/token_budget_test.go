package service

import "testing"

func TestTokenEstimate_EmptyContent(t *testing.T) {
	if got := EstimateTokenCost(""); got != 0 {
		t.Fatalf("EstimateTokenCost(\"\") = %d, want 0", got)
	}
}

func TestTokenEstimate_LongerContentCostsMore(t *testing.T) {
	short := EstimateTokenCost("short note")
	long := EstimateTokenCost("User prefers concise technical answers with contract-first planning and explicit verification loops.")
	if long <= short {
		t.Fatalf("long = %d, short = %d, want long > short", long, short)
	}
}

func TestTokenEstimate_IsDeterministic(t *testing.T) {
	content := "stable estimate"
	first := EstimateTokenCost(content)
	second := EstimateTokenCost(content)
	if first != second {
		t.Fatalf("first = %d, second = %d, want equal", first, second)
	}
}
