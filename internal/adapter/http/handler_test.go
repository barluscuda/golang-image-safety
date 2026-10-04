package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	gormadapter "github.com/barluscuda/golang-image-safety/internal/adapter/gorm"
	"github.com/barluscuda/golang-image-safety/internal/adapter/storage"
	"github.com/barluscuda/golang-image-safety/internal/domain"
	"github.com/barluscuda/golang-image-safety/internal/repository"
	"github.com/barluscuda/golang-image-safety/internal/service"
	"go.uber.org/zap"
)

type testModerator struct{ fail bool }

func (m testModerator) Moderate(_ context.Context, _ io.Reader, _, policy string) (*domain.ModerationResult, error) {
	if m.fail {
		return nil, errors.New("inference failed")
	}
	p, err := domain.ParsePolicy(policy)
	if err != nil {
		return nil, err
	}
	return p.Assess([]float32{0.02, 0.96, 0.02}, "test-xs-model")
}

func TestUploadStatusAndModerationContract(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "blocked result"
		if fail {
			name = "processing error"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			db, err := gormadapter.Initialize(filepath.Join(root, "api.db"), &repository.ImageModel{})
			if err != nil {
				t.Fatal(err)
			}
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			pool.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = pool.Close() })
			repo := repository.NewImageRepository(db)
			files, err := storage.NewLocal(filepath.Join(root, "images"))
			if err != nil {
				t.Fatal(err)
			}
			policy, hash, err := (domain.Policy{NSFWThreshold: 0.5, NSFLThreshold: 0.5}).Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			images := service.NewImageService(repo, files, policy, hash, 1024)
			router := NewRouter(images, 1024, zap.NewNop())
			moderation := service.NewModerationService(repo, files, testModerator{fail}, zap.NewNop())
			var pngData bytes.Buffer
			if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
				t.Fatal(err)
			}
			response := multipartUpload(t, router, pngData.Bytes())
			if response.Code != http.StatusAccepted {
				t.Fatalf("upload: %d %s", response.Code, response.Body.String())
			}
			var uploaded uploadResponse
			if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil {
				t.Fatal(err)
			}
			if uploaded.Status != domain.StatusQueued || len(uploaded.ImageID) != 32 || response.Header().Get("Location") != "/v1/images/"+uploaded.ImageID {
				t.Fatalf("upload contract: %+v", uploaded)
			}
			get := func() imageResponse {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/images/"+uploaded.ImageID, nil))
				if response.Code != http.StatusOK {
					t.Fatalf("get: %d %s", response.Code, response.Body.String())
				}
				var value imageResponse
				if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			queued := get()
			if queued.Status != domain.StatusQueued || queued.Outcome != "" || queued.ModerationResult != nil {
				t.Fatalf("queued contract: %+v", queued)
			}
			worked, err := moderation.ProcessNext(context.Background())
			if err != nil || !worked {
				t.Fatalf("processing: %t %v", worked, err)
			}
			processed := get()
			if processed.Status != domain.StatusProcessed || processed.ProcessedAt == nil || !processed.ImageDeleted {
				t.Fatalf("completion contract: %+v", processed)
			}
			if fail {
				if processed.Outcome != "error" || processed.ModerationResult != nil || processed.ProcessingError != "moderation_failed" {
					t.Fatalf("failure contract: %+v", processed)
				}
			} else if processed.Outcome != "success" || processed.ModerationResult == nil || processed.ModerationResult.Class != "NSFW" || processed.ModerationResult.Verdict != domain.VerdictBlocked || !processed.ModerationResult.ViolatesPolicy || processed.ModerationResult.Scores.NSFW != 0.96 {
				t.Fatalf("result contract: %+v", processed)
			}
			if _, err := files.Open(context.Background(), uploaded.ImageID+".png"); err == nil {
				t.Fatal("source file was retained")
			}
			if response := multipartUpload(t, router, []byte("not an image")); response.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("invalid image: %d", response.Code)
			}
			if response := multipartUpload(t, router, make([]byte, 1025)); response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized image: %d", response.Code)
			}
			for path, want := range map[string]int{"/v1/images/unknown": 404, "/health/live": 200, "/health/ready": 200, "/": 200} {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != want {
					t.Fatalf("%s: %d, want %d", path, response.Code, want)
				}
			}
		})
	}
}

func multipartUpload(t *testing.T, handler http.Handler, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/images", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
