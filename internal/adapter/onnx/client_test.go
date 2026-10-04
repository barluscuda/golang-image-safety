package onnx

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"testing"
	"time"

	"github.com/barluscuda/golang-image-safety/internal/adapter/storage"
	"github.com/barluscuda/golang-image-safety/internal/domain"
)

func TestNativeClassifierAspectRatios(t *testing.T) {
	model, runtime := os.Getenv("ONNX_TEST_MODEL"), os.Getenv("ONNX_TEST_RUNTIME")
	if model == "" || runtime == "" {
		t.Skip("set ONNX_TEST_MODEL and ONNX_TEST_RUNTIME to test real inference")
	}
	c, err := New(Config{ModelPath: model, RuntimeLibrary: runtime, Timeout: 10 * time.Second, MaxImageBytes: 1 << 20, Threads: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	files, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy, _, err := (domain.Policy{NSFWThreshold: 0.5, NSFLThreshold: 0.5}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"png", "jpeg"} {
		for _, size := range []struct {
			name          string
			width, height int
		}{
			{"square", 448, 448},
			{"portrait", 448, 896},
			{"landscape", 896, 448},
		} {
			t.Run(format+"_"+size.name, func(t *testing.T) {
				ctx := context.Background()
				data := gridImage(t, size.width, size.height, format)
				stored, err := files.Store(ctx, format+"_"+size.name, bytes.NewReader(data), c.cfg.MaxImageBytes)
				if err != nil {
					t.Fatal(err)
				}
				file, err := files.Open(ctx, stored.StorageKey)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				original, err := io.ReadAll(file)
				if err != nil || !bytes.Equal(original, data) {
					t.Fatalf("storage changed original image: %v", err)
				}
				result, err := c.Moderate(ctx, bytes.NewReader(original), stored.ContentType, policy)
				if err != nil {
					t.Fatal(err)
				}
				// Inspect the actual reused tensor bound to the native session.
				tolerance := 0.0
				if format == "jpeg" {
					tolerance = 2
				}
				assertEntireImageTensor(t, c.input.GetData(), tolerance)
				if result.Model != ModelName || result.Class == "" || result.Confidence <= 0 {
					t.Fatalf("invalid result: %+v", result)
				}
			})
		}
	}
}

// Opt in so ordinary tests do not require native runtime or model downloads.
func TestNativeClassifier(t *testing.T) {
	model, runtime := os.Getenv("ONNX_TEST_MODEL"), os.Getenv("ONNX_TEST_RUNTIME")
	if model == "" || runtime == "" {
		t.Skip("set ONNX_TEST_MODEL and ONNX_TEST_RUNTIME to test real inference")
	}
	c, err := New(Config{ModelPath: model, RuntimeLibrary: runtime, Timeout: 10 * time.Second, MaxImageBytes: 1024, Threads: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	var data bytes.Buffer
	fixture := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			fixture.SetRGBA(x, y, color.RGBA{R: 40, G: 80, B: 120, A: 255})
		}
	}
	if err := png.Encode(&data, fixture); err != nil {
		t.Fatal(err)
	}
	policy, _, err := (domain.Policy{NSFWThreshold: 0.5, NSFLThreshold: 0.5}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := c.Moderate(context.Background(), bytes.NewReader(data.Bytes()), "image/png", policy)
		if err != nil {
			t.Fatal(err)
		}
		if result.Model != ModelName || result.Class == "" || result.Confidence <= 0 {
			t.Fatalf("invalid result: %+v", result)
		}
		t.Logf("native prediction: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Moderate(ctx, bytes.NewReader(data.Bytes()), "image/png", policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Moderate(context.Background(), bytes.NewReader(data.Bytes()), "image/png", policy); err == nil {
		t.Fatal("closed classifier accepted a request")
	}
}
