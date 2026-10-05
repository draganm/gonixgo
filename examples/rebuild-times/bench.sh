#!/usr/bin/env bash
# Times both builders on the same change: one string in main.go.
# Usage: ./bench.sh [runs]
set -euo pipefail
cd "$(dirname "$0")"

runs="${1:-5}"
exec_opt=(--option allow-unsafe-native-code-during-evaluation true)
TIMEFORMAT=%R

# build <attribute>: nix prints nothing unless the build fails.
build() {
  local opts=() log
  if [ "$1" = gonixgo ]; then
    opts=("${exec_opt[@]}")
  fi
  if ! log="$(nix build "${opts[@]}" --no-link ".#$1" 2>&1)"; then
    echo "$log" >&2
    exit 1
  fi
}

# edit <text>: change what main.go prints, so every run sees a new source.
edit() {
  sed -i.bak "s/\"grpc[^\"]*\", grpc.Version/\"grpc $1\", grpc.Version/" main.go
  rm main.go.bak
}

original="$(cat main.go)"
trap 'printf "%s\n" "$original" > main.go' EXIT

for builder in buildGoModule gonixgo; do
  # Build once first, so the timed runs are rebuilds and not the first build.
  build "$builder"
  for i in $(seq "$runs"); do
    edit "$builder $i"
    printf '%-14s rebuild %s: ' "$builder" "$i"
    time build "$builder"
  done
  printf '%-14s no change: ' "$builder"
  time build "$builder"
done
