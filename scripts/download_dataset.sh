#!/usr/bin/env bash
set -euo pipefail

DATA_DIR="${1:-./data}"
mkdir -p "$DATA_DIR"

DATASETS=(
  "sample_data_200:https://github.com/RUCKBReasoning/SpreadsheetBench/raw/main/data/sample_data_200.tar.gz"
  "spreadsheetbench_verified_400:https://huggingface.co/datasets/KAKA22/SpreadsheetBench/resolve/main/spreadsheetbench_verified_400.tar.gz"
  "all_data_912:https://github.com/RUCKBReasoning/SpreadsheetBench/raw/main/data/all_data_912.tar.gz"
)

for entry in "${DATASETS[@]}"; do
  name="${entry%%:*}"
  url="${entry#*:}"
  if [ -d "$DATA_DIR/$name" ]; then
    echo "[skip] $name already exists"
    continue
  fi
  echo "[download] $name ..."
  curl -fSL "$url" -o "$DATA_DIR/$name.tar.gz"
  tar xzf "$DATA_DIR/$name.tar.gz" -C "$DATA_DIR"
  rm "$DATA_DIR/$name.tar.gz"
  echo "[done] $name"
done
