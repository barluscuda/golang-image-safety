package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	gormadapter "github.com/barluscuda/golang-image-safety/internal/adapter/gorm"
	"github.com/barluscuda/golang-image-safety/internal/adapter/storage"
	"github.com/barluscuda/golang-image-safety/internal/domain"
	"github.com/barluscuda/golang-image-safety/internal/port"
	"github.com/barluscuda/golang-image-safety/internal/repository"
	"go.uber.org/zap"
)

type allowModerator struct{}

func (allowModerator) Moderate(_ context.Context, _ io.Reader, _, policy string) (*domain.ModerationResult, error) {
	if policy != "test policy" {
		return nil, errors.New("wrong policy passed to moderator")
	}
	return &domain.ModerationResult{Verdict: domain.VerdictAllowed, Model: "test-model", Class: "SFW", Confidence: 0.95,
		Scores: domain.Scores{NSFL: 0.02, NSFW: 0.03, SFW: 0.95}}, nil
}

type failedModerator struct{ calls int }

func (m *failedModerator) Moderate(context.Context, io.Reader, string, string) (*domain.ModerationResult, error) {
	m.calls++
	return nil, errors.New("model unavailable")
}

func TestUploadModeratePersistAndDelete(t *testing.T) {
	testUploadModeratePersistAndDelete(t, allowModerator{}, "")
}

func TestModelFailureDoesNotApprove(t *testing.T) {
	moderator := &failedModerator{}
	testUploadModeratePersistAndDelete(t, moderator, "moderation_failed")
	if moderator.calls != 1 {
		t.Fatalf("model calls = %d, want 1", moderator.calls)
	}
}

func testUploadModeratePersistAndDelete(t *testing.T, moderator port.ImageModerator, wantError string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := gormadapter.Initialize(filepath.Join(root, "test.db")+"?_busy_timeout=5000", &repository.ImageModel{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := repository.NewImageRepository(db)
	files, err := storage.NewLocal(filepath.Join(root, "images"))
	if err != nil {
		t.Fatal(err)
	}
	var pngData bytes.Buffer
	fixture := image.NewRGBA(image.Rect(0, 0, 1, 1))
	fixture.Set(0, 0, color.RGBA{R: 40, G: 80, B: 120, A: 255})
	if err := png.Encode(&pngData, fixture); err != nil {
		t.Fatal(err)
	}
	images := NewImageService(repo, files, "test policy", "test-policy-hash", 1024)
	queued, err := images.Upload(ctx, "fixture.png", bytes.NewReader(pngData.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != domain.StatusQueued {
		t.Fatalf("upload status = %q, want queued", queued.Status)
	}
	// Simulate a process dying after claiming an image, before inference.
	claimed, err := repo.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.Status != domain.StatusProcessing {
		t.Fatalf("claim image for recovery: %+v, %v", claimed, err)
	}
	if err := repo.ResetProcessing(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := images.Get(ctx, queued.ID)
	if err != nil || recovered.Status != domain.StatusQueued || recovered.PolicyText != "test policy" || recovered.PolicyHash != "test-policy-hash" {
		t.Fatalf("recovery did not preserve upload policy: %+v, %v", recovered, err)
	}
	storedPath := filepath.Join(root, "images", queued.StorageKey)
	if _, err := os.Stat(storedPath); err != nil {
		t.Fatalf("uploaded file was not stored: %v", err)
	}

	moderation := NewModerationService(repo, files, moderator, zap.NewNop())
	worked, err := moderation.ProcessNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !worked {
		t.Fatal("worker did not claim queued image")
	}
	processed, err := images.Get(ctx, queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if processed.Status != domain.StatusProcessed {
		t.Fatalf("status = %q, want processed", processed.Status)
	}
	if wantError != "" {
		if processed.Result != nil || processed.ProcessingError != wantError {
			t.Fatalf("failed model call produced result %#v, error %q", processed.Result, processed.ProcessingError)
		}
	} else if processed.Result == nil || processed.Result.Verdict != domain.VerdictAllowed || processed.ProcessingError != "" {
		t.Fatalf("unexpected moderation result: %#v, error %q", processed.Result, processed.ProcessingError)
	}
	if wantError == "" && (processed.Result.Class != "SFW" || processed.Result.Confidence != 0.95 || processed.Result.Scores.SFW != 0.95 || processed.Result.Scores.NSFW != 0.03 || processed.Result.Scores.NSFL != 0.02) {
		t.Fatalf("classification probabilities were not persisted: %+v", processed.Result)
	}
	if processed.DeletedAt == nil {
		t.Fatal("database does not record image deletion")
	}
	if _, err := os.Stat(storedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("image file still exists or stat failed: %v", err)
	}
}
