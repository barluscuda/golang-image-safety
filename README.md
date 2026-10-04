# Image Safety for Go

A local image moderation service using [OwenElliott/image-safety-classifier-xs](https://huggingface.co/OwenElliott/image-safety-classifier-xs) with ONNX Runtime on CPU. No separate inference server is required.

This project follows the architecture and asynchronous HTTP API of [golang-image-guard-shieldgemma-2](https://github.com/barluscuda/golang-image-guard-shieldgemma-2): Gin handlers, application services, ports, GORM/SQLite repositories, local file storage, and one background worker. ShieldGemma chat inference is replaced by a reusable native ONNX session.

## Run locally

Requirements: Go 1.25 or newer, a C compiler with CGO enabled, and Linux x64 or arm64. Setup also uses Bash, curl, tar, and sha256sum.

```sh
bash scripts/setup.sh
go run ./cmd/server
```

Open <http://localhost:8080/> for the inherited moderation workspace. The workspace shows the predicted class and model score alongside each verdict. Its styling uses the Tailwind CDN; the API and inference work offline after setup.

Setup downloads the 13.1 MB FP32 model and ONNX Runtime 1.29.0, verifies their SHA-256 checksums, and stores them in ignored `models/` and `runtime/` directories. The model is pinned to revision `54f4560bd9c5ee92d45dc30418a8f8680e80de6d`. The runtime matches the C API used by the pinned [`onnxruntime_go` v1.36.0](https://github.com/yalue/onnxruntime_go/tree/v1.36.0) dependency.

For other platforms, download the ONNX file from that model revision and the appropriate [ONNX Runtime 1.29.0 release](https://github.com/microsoft/onnxruntime/releases/tag/v1.29.0), then set `moderation.model_path` and `moderation.runtime_library` in `config.yaml`. Use the platform's actual `.so`, `.dylib`, or `.dll` library.

## API

Upload one JPEG or PNG using multipart field `image`:

```sh
curl -i -F 'image=@sample.png' http://localhost:8080/v1/images
```

The API returns `202 Accepted`, a `Location: /v1/images/IMAGE_ID` header, and:

```json
{"image_id":"IMAGE_ID","status":"queued"}
```

Poll the returned image ID:

```sh
curl http://localhost:8080/v1/images/IMAGE_ID
```

Example processed response (probabilities are illustrative):

```json
{
  "image_id": "IMAGE_ID",
  "original_filename": "sample.png",
  "content_type": "image/png",
  "size_bytes": 12345,
  "status": "processed",
  "outcome": "success",
  "moderation_result": {
    "verdict": "allowed",
    "violates_policy": false,
    "model": "OwenElliott/image-safety-classifier-xs",
    "class": "SFW",
    "confidence": 0.95,
    "scores": {"nsfl": 0.02, "nsfw": 0.03, "sfw": 0.95}
  },
  "processing_error": "",
  "created_at": "2026-10-04T13:00:00Z",
  "processed_at": "2026-10-04T13:00:01Z",
  "image_deleted": true
}
```

Status progresses through `queued`, `processing`, and `processed`. Before completion, `outcome` is empty and `moderation_result` is null. Processing failures return `outcome: "error"`, a null result, and `image_read_failed` or `moderation_failed`; they never imply an allowed verdict. Source files are deleted after successful or failed processing, and failed deletions are retried. A result may briefly have `image_deleted: false` while cleanup finishes.

| Route | Behavior |
| --- | --- |
| `GET /` | Browser workspace |
| `POST /v1/images` | Queue image; 202 on success |
| `GET /v1/images/:id` | Status/result; 404 for unknown IDs |
| `GET /health/live` | Process liveness |
| `GET /health/ready` | Ready after the model and database load |

Invalid multipart requests return 400, oversized uploads return 413, unsupported or invalid images return 415, and persistence failures return 500. Uploads default to 10 MiB, at most 12,000 pixels per dimension and 40 million pixels total. Full decoding happens during moderation; a file with a valid header but corrupt body can be accepted and then fail processing.

## Classification policy

The model predicts three classes: `NSFL` (gore), `NSFW` (pornographic or highly suggestive content), and `SFW`. It is a fixed classifier: natural-language `policy.txt` prompts from the ShieldGemma service are replaced by numeric thresholds.

```yaml
policy:
  nsfw_threshold: 0.5
  nsfl_threshold: 0.5
```

Block an image when its highest-scoring class is unsafe, or when either unsafe probability reaches its configured threshold. Ties with an unsafe class are blocked. A dominant SFW class can still be blocked by a stricter threshold. Each threshold must be greater than zero and at most one. The policy is snapshotted into each queued record so pending uploads keep their policy across restarts.

`class` and `confidence` describe the prediction; `verdict` describes the policy decision. Confidence is the selected softmax probability, not a calibrated safety guarantee. This classifier does not implement arbitrary content rules, and the model card notes limited NSFL training coverage. Evaluate it against your own images before setting production thresholds.

## Configuration

Run from a directory containing `config.yaml`. Every key can be overridden using `IMAGE_SAFETY_` environment variables, replacing dots with underscores:

```sh
IMAGE_SAFETY_SERVER_ADDRESS=:9090 \
IMAGE_SAFETY_POLICY_NSFW_THRESHOLD=0.3 \
go run ./cmd/server
```

Key settings include `database.dsn`, `storage.directory`, `storage.max_image_bytes`, `moderation.model_path`, `moderation.runtime_library`, `moderation.threads`, `moderation.timeout`, and `worker.poll_interval`. By default, SQLite uses `imagesafety.db`, uploaded files use `data/images`, and inference uses two CPU threads.

The service fails startup if the model/runtime is missing or its tensor contract is incompatible. Native inference supports cancellation and a timeout. Shutdown cancels the worker and waits for it before freeing the ONNX session and library.

## Architecture

```text
cmd/server                 configuration and dependency wiring
internal/domain            images, statuses, classification scores and policy
internal/port              application/repository/storage/moderator interfaces
internal/service           upload, queue processing, persistence, cleanup
internal/repository        GORM image records and queue claims
internal/adapter/http      Gin handlers with the reference API contract
internal/adapter/gorm      SQLite connection and schema migration
internal/adapter/storage   bounded uploads and local file storage
internal/adapter/onnx      image preprocessing and native CPU inference
internal/worker            polling and cleanup retries
tools                      embedded browser workspace
```

The ONNX adapter decodes RGB, drops alpha, resizes to 224×224 using bilinear interpolation, and fills a float32 NCHW tensor with pixel values in 0–255. Normalization and softmax are already inside the published graph. The verified graph has input `image: [batch,3,224,224]` and output `probabilities: [batch,3]`, ordered NSFL, NSFW, SFW. The service processes one image per run and reuses its tensors and session.

Run one service process per SQLite database/storage directory. Interrupted `processing` rows are requeued on restart. The database retains results; files are removed after processing. Queue length and result retention are not capped.

## Verification

```sh
go test ./...
go test -race ./...
go vet ./...
node --test tools/web.test.cjs
```

After setup, opt in to the real native inference test:

```sh
ONNX_TEST_MODEL="$PWD/models/image-safety-classifier-xs.onnx" \
ONNX_TEST_RUNTIME="$PWD/runtime/libonnxruntime.so.1.29.0" \
go test -v ./internal/adapter/onnx -run TestNativeClassifier
```

Ordinary tests do not need model downloads. They cover policy decisions, invalid outputs, RGB layout, cancellation, upload/status compatibility, persistence, recovery, and deletion. Browser tests verify saved results against the API before showing decisions.

## Skill discovery

The installed `golang-design-patterns` skill fits this project's architecture. Searches of the skills registry for ONNX and image safety did not identify a close match for local Go NSFW inference; no additional skill was installed. Integration follows the model author's ONNX instructions and the Go runtime wrapper's documentation.
