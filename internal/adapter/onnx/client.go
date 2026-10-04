package onnx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/barluscuda/golang-image-safety/internal/domain"
	"github.com/barluscuda/golang-image-safety/internal/port"
	ort "github.com/yalue/onnxruntime_go"
)

const ModelName = "OwenElliott/image-safety-classifier-xs"

type Config struct {
	ModelPath      string
	RuntimeLibrary string
	Timeout        time.Duration
	MaxImageBytes  int64
	Threads        int
}

// Client owns the process-wide ONNX environment and a reusable CPU session.
// Construct one at startup and close it after all workers have exited.
type Client struct {
	session *ort.AdvancedSession
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
	gate    chan struct{}
	closed  bool
	cfg     Config
}

var _ port.ImageModerator = (*Client)(nil)

func New(cfg Config) (_ *Client, err error) {
	if cfg.Timeout <= 0 || cfg.MaxImageBytes <= 0 || cfg.MaxImageBytes > 1<<30 || cfg.Threads <= 0 {
		return nil, fmt.Errorf("ONNX timeout, image limit, and threads must be positive; image limit must not exceed 1 GiB")
	}
	for _, path := range []string{cfg.ModelPath, cfg.RuntimeLibrary} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("missing ONNX model or runtime at %q; run bash scripts/setup.sh", path)
		}
	}
	if ort.IsInitialized() {
		return nil, fmt.Errorf("ONNX environment already initialized; use one classifier per process")
	}
	ort.SetSharedLibraryPath(cfg.RuntimeLibrary)
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("initialize ONNX Runtime 1.29.0: %w", err)
	}
	c := &Client{cfg: cfg, gate: make(chan struct{}, 1)}
	c.gate <- struct{}{}
	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()
	if err = options.SetIntraOpNumThreads(cfg.Threads); err != nil {
		return nil, err
	}
	if err = options.SetInterOpNumThreads(1); err != nil {
		return nil, err
	}
	inputs, outputs, err := ort.GetInputOutputInfoWithOptions(cfg.ModelPath, options)
	if err != nil {
		return nil, fmt.Errorf("inspect ONNX model: %w", err)
	}
	if len(inputs) != 1 || len(outputs) != 1 || inputs[0].DataType != ort.TensorElementDataTypeFloat || outputs[0].DataType != ort.TensorElementDataTypeFloat ||
		!matchesBatchShape(inputs[0].Dimensions, ort.NewShape(1, 3, imageSize, imageSize)) || !matchesBatchShape(outputs[0].Dimensions, ort.NewShape(1, 3)) {
		return nil, fmt.Errorf("model must have float32 input [1,3,224,224] and output [1,3]; got inputs %+v, outputs %+v", inputs, outputs)
	}
	c.input, err = ort.NewEmptyTensor[float32](ort.NewShape(1, 3, imageSize, imageSize))
	if err != nil {
		return nil, err
	}
	c.output, err = ort.NewEmptyTensor[float32](ort.NewShape(1, 3))
	if err != nil {
		return nil, err
	}
	c.session, err = ort.NewAdvancedSession(cfg.ModelPath, []string{inputs[0].Name}, []string{outputs[0].Name}, []ort.Value{c.input}, []ort.Value{c.output}, options)
	if err != nil {
		return nil, fmt.Errorf("load image safety classifier: %w", err)
	}
	return c, nil
}

// The published graph uses a symbolic batch dimension (-1). Run one image at
// a time while still checking all channel, spatial, and class dimensions.
func matchesBatchShape(actual, expected ort.Shape) bool {
	return len(actual) == len(expected) && len(actual) > 0 &&
		(actual[0] == 1 || actual[0] == -1) && slices.Equal(actual[1:], expected[1:])
}

func (c *Client) Moderate(ctx context.Context, src io.Reader, _ string, policyText string) (*domain.ModerationResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	policy, err := domain.ParsePolicy(policyText)
	if err != nil {
		return nil, err
	}
	pixels, err := preprocess(ctx, src, c.cfg.MaxImageBytes)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.gate:
	}
	defer func() { c.gate <- struct{}{} }()
	if c.closed {
		return nil, fmt.Errorf("classifier is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	copy(c.input.GetData(), pixels)
	options, err := ort.NewRunOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()
	finished, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = options.Terminate()
		case <-finished:
		}
	}()
	err = c.session.RunWithOptions(options)
	close(finished)
	<-watcherDone // Do not destroy native run options while cancellation uses them.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("run image safety classifier: %w", err)
	}
	return policy.Assess(c.output.GetData(), ModelName)
}

func (c *Client) Close() error {
	<-c.gate
	defer func() { c.gate <- struct{}{} }()
	if c.closed {
		return nil
	}
	c.closed = true
	var errs []error
	if c.session != nil {
		errs = append(errs, c.session.Destroy())
	}
	if c.output != nil {
		errs = append(errs, c.output.Destroy())
	}
	if c.input != nil {
		errs = append(errs, c.input.Destroy())
	}
	errs = append(errs, ort.DestroyEnvironment())
	return errors.Join(errs...)
}
