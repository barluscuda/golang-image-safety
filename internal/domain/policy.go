package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
)

type Policy struct {
	NSFWThreshold float32 `json:"nsfw_threshold"`
	NSFLThreshold float32 `json:"nsfl_threshold"`
}

func (p Policy) Validate() error {
	for _, threshold := range []float32{p.NSFWThreshold, p.NSFLThreshold} {
		if math.IsNaN(float64(threshold)) || threshold <= 0 || threshold > 1 {
			return ErrInvalidPolicy
		}
	}
	return nil
}

// Snapshot persists the policy with each upload, including across restarts.
func (p Policy) Snapshot() (text, hash string, err error) {
	if err = p.Validate(); err != nil {
		return "", "", err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(data)
	return string(data), hex.EncodeToString(sum[:]), nil
}

func ParsePolicy(text string) (Policy, error) {
	var p Policy
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		return p, ErrInvalidPolicy
	}
	return p, p.Validate()
}

func (p Policy) Assess(values []float32, model string) (*ModerationResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if len(values) != 3 {
		return nil, ErrInvalidScores
	}
	var sum float64
	top := 0
	for i, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || value < 0 || value > 1 {
			return nil, ErrInvalidScores
		}
		sum += float64(value)
		if value > values[top] {
			top = i
		}
	}
	if math.Abs(sum-1) > 0.001 {
		return nil, ErrInvalidScores
	}
	// Ties with an unsafe class are blocked. Thresholds can make the policy stricter.
	blocked := top != 2 || values[0] >= p.NSFLThreshold || values[1] >= p.NSFWThreshold
	verdict := VerdictAllowed
	if blocked {
		verdict = VerdictBlocked
	}
	return &ModerationResult{
		Verdict: verdict, ViolatesPolicy: blocked, Model: model,
		Class: []string{"NSFL", "NSFW", "SFW"}[top], Confidence: values[top],
		Scores: Scores{NSFL: values[0], NSFW: values[1], SFW: values[2]},
	}, nil
}
