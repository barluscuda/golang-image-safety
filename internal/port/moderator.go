package port

import (
	"context"
	"io"

	"github.com/barluscuda/golang-image-safety/internal/domain"
)

type ImageModerator interface {
	Moderate(context.Context, io.Reader, string, string) (*domain.ModerationResult, error)
}
