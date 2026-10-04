package domain

import "time"

type ModerationResult struct {
	Verdict        Verdict `json:"verdict"`
	ViolatesPolicy bool    `json:"violates_policy"`
	Model          string  `json:"model"`
	Class          string  `json:"class"`
	Confidence     float32 `json:"confidence"`
	Scores         Scores  `json:"scores"`
}

// Scores are probabilities in the export's class order: NSFL, NSFW, SFW.
type Scores struct {
	NSFL float32 `json:"nsfl"`
	NSFW float32 `json:"nsfw"`
	SFW  float32 `json:"sfw"`
}

type Image struct {
	ID               string
	OriginalFilename string
	ContentType      string
	SizeBytes        int64
	StorageKey       string
	Status           Status
	Result           *ModerationResult
	ProcessingError  string
	PolicyHash       string
	PolicyText       string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ProcessedAt      *time.Time
	DeletedAt        *time.Time
}
