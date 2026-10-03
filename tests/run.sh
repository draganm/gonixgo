#!/usr/bin/env bash
# Integration tests: build the fixtures with real nix and check the results.
# They need builtins.exec and, on the first run, the network, so they run
# outside the Nix sandbox.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
flake="path:$root"
exec_opt=(--option allow-unsafe-native-code-during-evaluation true)

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# build <attribute under legacyPackages.<system>>: prints the output path.
build() {
  nix build "${exec_opt[@]}" --no-link --print-out-paths "$flake#$1"
}

# check_run <fixture> <binary> <expected stdout>
check_run() {
  local out got
  out="$(build "fixtures.$1")"
  got="$("$out/bin/$2")"
  [ "$got" = "$3" ] || fail "$1: $2 printed '$got', want '$3'"
  echo "ok: $1: $2 runs"
}

# check_modinfo <fixture> <binary> <main package, relative to the fixture>
# The module info must match what `go build -trimpath` embeds.
check_modinfo() {
  local out go tmp
  out="$(build "fixtures.$1")"
  go="$(build "fixtures.$1.go")/bin/go"
  tmp="$(mktemp -d)"
  (cd "$root/tests/fixtures/$1" &&
    GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local \
      "$go" build -trimpath -buildvcs=false -o "$tmp/ref" "$3")
  if ! diff <("$go" version -m "$out/bin/$2" | tail -n +2) <("$go" version -m "$tmp/ref" | tail -n +2); then
    rm -rf "$tmp"
    fail "$1: $2 module info differs from go build -trimpath (left: gonixgo, right: go build)"
  fi
  rm -rf "$tmp"
  echo "ok: $1: $2 module info matches go build"
}

check_run hello-deps hello "hello, gonixgo"
check_modinfo hello-deps hello .

echo "all integration checks passed"
