package domain

import (
	"errors"
	"math"
	"testing"
)

func TestClassificationPolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		values    []float32
		threshold float32
		class     string
		verdict   Verdict
	}{
		{"safe", []float32{0.02, 0.03, 0.95}, 0.5, "SFW", VerdictAllowed},
		{"nsfw", []float32{0.01, 0.98, 0.01}, 0.5, "NSFW", VerdictBlocked},
		{"nsfl", []float32{0.98, 0.01, 0.01}, 0.5, "NSFL", VerdictBlocked},
		{"split unsafe", []float32{0.45, 0.45, 0.1}, 0.5, "NSFL", VerdictBlocked},
		{"strict threshold", []float32{0.01, 0.25, 0.74}, 0.2, "SFW", VerdictBlocked},
		{"threshold boundary", []float32{0.01, 0.2, 0.79}, 0.2, "SFW", VerdictBlocked},
		{"tie", []float32{0.5, 0, 0.5}, 1, "NSFL", VerdictBlocked},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := Policy{NSFWThreshold: test.threshold, NSFLThreshold: test.threshold}
			result, err := p.Assess(test.values, "test-model")
			if err != nil {
				t.Fatal(err)
			}
			if result.Class != test.class || result.Verdict != test.verdict || result.ViolatesPolicy != (test.verdict == VerdictBlocked) {
				t.Fatalf("unexpected result: %+v", result)
			}
			if result.Scores.NSFL != test.values[0] || result.Scores.NSFW != test.values[1] || result.Scores.SFW != test.values[2] {
				t.Fatalf("incorrect label order: %+v", result.Scores)
			}
		})
	}
}

func TestInvalidOutputCannotApprove(t *testing.T) {
	p := Policy{NSFWThreshold: 0.5, NSFLThreshold: 0.5}
	for _, values := range [][]float32{
		nil, {0.1, 0.9}, {0, 0, 0}, {0, 0, 2}, {-0.1, 0.1, 1},
		{0, 0, float32(math.NaN())}, {0, 0, float32(math.Inf(1))},
	} {
		result, err := p.Assess(values, "test-model")
		if result != nil || !errors.Is(err, ErrInvalidScores) {
			t.Fatalf("invalid output %v produced %+v, %v", values, result, err)
		}
	}
}

func TestPolicySnapshot(t *testing.T) {
	p := Policy{NSFWThreshold: 0.25, NSFLThreshold: 0.35}
	text, hash, err := p.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePolicy(text)
	if err != nil || parsed != p || len(hash) != 64 {
		t.Fatalf("snapshot failed: %+v %q %v", parsed, hash, err)
	}
	for _, text := range []string{"test policy", "{}", `{"nsfw_threshold":2,"nsfl_threshold":0.5}`} {
		if _, err := ParsePolicy(text); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("invalid policy accepted: %s", text)
		}
	}
}
