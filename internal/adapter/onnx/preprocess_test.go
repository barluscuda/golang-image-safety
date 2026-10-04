package onnx

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"testing"

	"github.com/barluscuda/golang-image-safety/internal/domain"
)

func TestPreprocessPreservesEntireImage(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{"square", 448, 448},
		{"portrait", 448, 896},
		{"landscape", 896, 448},
		{"portrait_mixed_scaling", 112, 448},
		{"landscape_mixed_scaling", 448, 112},
		{"portrait_upscale", 8, 16},
		{"landscape_upscale", 16, 8},
		{"square_upscale", 8, 8},
		{"very_tall", 4, 4096},
		{"very_wide", 4096, 4},
	} {
		t.Run(size.name, func(t *testing.T) {
			pixels, err := preprocess(context.Background(), bytes.NewReader(gridImage(t, size.width, size.height, "png")), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			assertEntireImageTensor(t, pixels, 0)
		})
	}
	for _, size := range []image.Point{{448, 448}, {448, 896}, {896, 448}} {
		t.Run("jpeg_"+size.String(), func(t *testing.T) {
			pixels, err := preprocess(context.Background(), bytes.NewReader(gridImage(t, size.X, size.Y, "jpeg")), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			assertEntireImageTensor(t, pixels, 2)
		})
	}
}

// Sixteen distinct tiles let the assertions detect cropping at either edge,
// padding, transposition, and incorrect channel or row strides.
func gridImage(t *testing.T, width, height int, format string) []byte {
	t.Helper()
	fixture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			col, row := 4*x/width, 4*y/height
			fixture.SetNRGBA(x, y, color.NRGBA{R: uint8(32 + 48*col), G: uint8(40 + 48*row), B: uint8(16 + 8*col + 32*row), A: 255})
		}
	}
	var data bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&data, fixture, &jpeg.Options{Quality: 100})
	} else {
		err = png.Encode(&data, fixture)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func assertEntireImageTensor(t *testing.T, pixels []float32, tolerance float64) {
	t.Helper()
	const plane = imageSize * imageSize
	if len(pixels) != 3*plane {
		t.Fatalf("tensor length = %d, want %d", len(pixels), 3*plane)
	}
	for i, value := range pixels {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || value < 0 || value > 255 {
			t.Fatalf("tensor[%d] = %v, want finite RGB in [0,255]", i, value)
		}
	}
	// Include all corners and the center of every tile. Expectations are based
	// on the original image's pattern, independently of the resize code.
	for _, y := range []int{0, 28, 84, 140, 196, imageSize - 1} {
		for _, x := range []int{0, 28, 84, 140, 196, imageSize - 1} {
			col, row := 4*x/imageSize, 4*y/imageSize
			want := []float32{float32(32 + 48*col), float32(40 + 48*row), float32(16 + 8*col + 32*row)}
			for channel, expected := range want {
				value := pixels[channel*plane+y*imageSize+x]
				if math.Abs(float64(value-expected)) > tolerance {
					t.Fatalf("channel %d at (%d,%d) = %v, want %v (tolerance %v)", channel, x, y, value, expected, tolerance)
				}
			}
		}
	}
}

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
