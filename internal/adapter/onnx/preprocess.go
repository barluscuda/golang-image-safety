package onnx

import (
	"bytes"
	"context"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"

	"github.com/barluscuda/golang-image-safety/internal/domain"
	"golang.org/x/image/draw"
)

const imageSize = 224

// The published ONNX graph includes normalization and softmax. Its input is
// float32 RGB in [0,255], laid out as [1,3,224,224]; do not normalize twice.
func preprocess(ctx context.Context, src io.Reader, maxBytes int64) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(src, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, domain.ErrImageTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 12000 || cfg.Height > 12000 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return nil, domain.ErrInvalidImage
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, domain.ErrInvalidImage
	}
	// Discard alpha before resizing, matching Pillow's convert("RGB").
	bounds := decoded.Bounds()
	opaque := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			opaque.SetRGBA(x, y, color.RGBA{R: c.R, G: c.G, B: c.B, A: 255})
		}
	}
	resized := image.NewRGBA(image.Rect(0, 0, imageSize, imageSize))
	draw.BiLinear.Scale(resized, resized.Bounds(), opaque, bounds, draw.Src, nil)
	plane := imageSize * imageSize
	pixels := make([]float32, 3*plane)
	for y := 0; y < imageSize; y++ {
		for x := 0; x < imageSize; x++ {
			c := resized.RGBAAt(x, y)
			i := y*imageSize + x
			pixels[i], pixels[plane+i], pixels[2*plane+i] = float32(c.R), float32(c.G), float32(c.B)
		}
	}
	return pixels, ctx.Err()
}
