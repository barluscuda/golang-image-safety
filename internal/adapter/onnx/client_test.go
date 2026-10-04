package onnx

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/barluscuda/golang-image-safety/internal/domain"
)

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
