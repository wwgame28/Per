#!/bin/sh
set -eu
umask 077
mkdir -p /data/assets
MODEL_PATH=${MODEL_PATH:-/data/model.gguf}
MODEL_URL=${MODEL_URL:-https://huggingface.co/unsloth/Qwen3-0.6B-GGUF/resolve/main/Qwen3-0.6B-Q4_K_M.gguf}
MODEL_SHA256=${MODEL_SHA256:-ac2d97712095a558e31573f62f466a3f9d93990898b0ec79d7c974c1780d524a}

/app/shtab &
bot_pid=$!
(
  model_pid=''
  trap '[ -z "$model_pid" ] || kill "$model_pid" 2>/dev/null; exit 0' TERM INT
  if [ ! -f "$MODEL_PATH" ]; then
    echo 'Downloading local model; VK control bot is already running.'
    if ! curl -fsSL --retry 2 --connect-timeout 20 --max-time 900 --max-filesize 800000000 "$MODEL_URL" -o "$MODEL_PATH.part"; then
      rm -f "$MODEL_PATH.part"
      echo 'Model download failed. VK control bot remains available; restart after fixing network/model settings.'
      exit 0
    fi
    if ! printf '%s  %s\n' "$MODEL_SHA256" "$MODEL_PATH.part" | sha256sum -c - >/dev/null; then
      rm -f "$MODEL_PATH.part"
      echo 'Model checksum mismatch. Inference remains disabled.'
      exit 0
    fi
    mv "$MODEL_PATH.part" "$MODEL_PATH"
  fi
  if ! printf '%s  %s\n' "$MODEL_SHA256" "$MODEL_PATH" | sha256sum -c - >/dev/null; then
    echo 'Stored model checksum mismatch. Inference remains disabled.'
    exit 0
  fi
  while :; do
    # No VK secrets are inherited by the inference process. One model, one slot, one CPU thread.
    env -i PATH=/usr/bin:/bin LD_LIBRARY_PATH=/opt/llama /opt/llama/llama-server \
      --model "$MODEL_PATH" --host 127.0.0.1 --port 8081 \
      --ctx-size 2048 --parallel 1 --threads 1 --threads-batch 1 \
      --batch-size 128 --ubatch-size 64 --threads-http 2 --n-gpu-layers 0 \
      --jinja --chat-template-kwargs '{"enable_thinking":false}' --log-disable &
    model_pid=$!
    wait "$model_pid" || true
    model_pid=''
    echo 'llama-server stopped; retry in 60 seconds. VK control remains available.'
    sleep 60
  done
) &
supervisor_pid=$!
cleanup() {
  trap - TERM INT
  kill "$supervisor_pid" "$bot_pid" 2>/dev/null || true
  wait "$bot_pid" 2>/dev/null || true
}
trap cleanup TERM INT
wait "$bot_pid" || bot_exit=$?
kill "$supervisor_pid" 2>/dev/null || true
exit "${bot_exit:-0}"
