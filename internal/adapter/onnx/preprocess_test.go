package onnx

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/barluscuda/golang-image-safety/internal/domain"
)

func TestPreprocessRGBOrderRangeAndAlpha(t *testing.T) {
	fixture := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			fixture.SetNRGBA(x, y, color.NRGBA{R: 40, G: 80, B: 120, A: 0})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, fixture); err != nil {
		t.Fatal(err)
	}
	pixels, err := preprocess(context.Background(), bytes.NewReader(data.Bytes()), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(pixels) != 3*224*224 {
		t.Fatalf("wrong tensor size: %d", len(pixels))
	}
	for channel, want := range []float32{40, 80, 120} {
		for _, value := range pixels[channel*224*224 : (channel+1)*224*224] {
			if value != want {
				t.Fatalf("channel %d = %v, want raw RGB %v", channel, value, want)
			}
		}
	}
}

func TestPreprocessRejectsInvalidImageAndCancellation(t *testing.T) {
	if _, err := preprocess(context.Background(), bytes.NewBufferString("not an image"), 1024); !errors.Is(err, domain.ErrInvalidImage) {
		t.Fatal(err)
	}
	if _, err := preprocess(context.Background(), bytes.NewBufferString("12345"), 4); !errors.Is(err, domain.ErrImageTooLarge) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := preprocess(ctx, bytes.NewReader(nil), 1024); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
