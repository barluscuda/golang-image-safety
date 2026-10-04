package domain

import "errors"

var (
	ErrNotFound      = errors.New("image not found")
	ErrInvalidImage  = errors.New("invalid or unsupported image")
	ErrImageTooLarge = errors.New("image exceeds size limit")
	ErrInvalidPolicy = errors.New("invalid classification policy")
	ErrInvalidScores = errors.New("invalid model probabilities")
)
