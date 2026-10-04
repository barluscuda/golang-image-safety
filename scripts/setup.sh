#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

version=1.29.0
revision=54f4560bd9c5ee92d45dc30418a8f8680e80de6d
model_sha=8c28c49d9075f3ad15ebdc2961f02d5b3f99be944815b848b49c9f0e6f3fb689
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)
    platform=linux-x64
    runtime_sha=c3fddc4f139a045b0c4902c57410f0694f1c2fdf9b6939fbe38b1aeae7cd14ba
    ;;
  Linux/aarch64)
    platform=linux-aarch64
    runtime_sha=e1799098ebc054b370f6176a450f158720f297818c613e5dc99b92e2ec82346f
    ;;
  *)
    echo 'Automatic setup supports Linux x64 and arm64. See README for manual runtime setup.' >&2
    exit 1
    ;;
esac

mkdir -p models runtime
task_tmp=$(mktemp -d)
trap 'rm -rf "$task_tmp"' EXIT

model=models/image-safety-classifier-xs.onnx
if [ ! -f "$model" ] || ! echo "$model_sha  $model" | sha256sum --check --status 2>/dev/null; then
  curl --silent --show-error --fail --location --retry 3 --connect-timeout 15 --max-time 300 \
    "https://huggingface.co/OwenElliott/image-safety-classifier-xs/resolve/$revision/onnx/image-safety-classifier-xs.onnx" \
    --output "$task_tmp/model.onnx"
  echo "$model_sha  $task_tmp/model.onnx" | sha256sum --check
  mv "$task_tmp/model.onnx" "$model"
fi

archive="onnxruntime-$platform-$version"
curl --silent --show-error --fail --location --retry 3 --connect-timeout 15 --max-time 300 \
  "https://github.com/microsoft/onnxruntime/releases/download/v$version/$archive.tgz" \
  --output "$task_tmp/runtime.tgz"
echo "$runtime_sha  $task_tmp/runtime.tgz" | sha256sum --check
tar -xzf "$task_tmp/runtime.tgz" -C "$task_tmp"
cp -R "$task_tmp/$archive/lib/." runtime/
cp "$task_tmp/$archive/LICENSE" runtime/LICENSE
echo "Ready: $model and runtime/libonnxruntime.so.$version"
