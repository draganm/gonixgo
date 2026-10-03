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

# check_distinct_build_ids <fixture> <binary> <binary>
# The linker derives LC_UUID and the ELF build ID from the Go build ID, so
# two binaries must not share one.
check_distinct_build_ids() {
  local out a b
  out="$(build "fixtures.$1")"
  a="$(grep -a -o 'Go build ID: "[^"]*"' "$out/bin/$2" | head -1)" || true
  b="$(grep -a -o 'Go build ID: "[^"]*"' "$out/bin/$3" | head -1)" || true
  [ -n "$a" ] || fail "$1: $2 has no Go build ID"
  [ -n "$b" ] || fail "$1: $3 has no Go build ID"
  [ "$a" != "$b" ] || fail "$1: $2 and $3 share the build ID $a"
  echo "ok: $1: $2 and $3 have distinct build IDs"
}

# check_incremental <fixture> <file to append to> <what must change>
# Evaluates the fixture from two copies that differ in one file and lists
# the nodes whose derivations differ: package import paths, then bin:<name>,
# then mod:<key>.
check_incremental() {
  local a b got
  a="$(mktemp -d)"
  b="$(mktemp -d)"
  cp -R "$root/tests/fixtures/$1/." "$a/"
  cp -R "$root/tests/fixtures/$1/." "$b/"
  printf '\n// edited\n' >>"$b/$2"
  got="$(nix eval --impure --raw "${exec_opt[@]}" --expr "
    let
      goEnv = (builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}.goEnv;
      build = src: goEnv.buildGoApplication { pname = \"incremental\"; inherit src; };
      a = build $a;
      b = build $b;
      changed = set: builtins.filter
        (n: a.\${set}.\${n}.drvPath != b.\${set}.\${n}.drvPath)
        (builtins.attrNames a.\${set});
    in builtins.concatStringsSep \" \" (
      changed \"packages\"
      ++ map (n: \"bin:\" + n) (changed \"bins\")
      ++ map (n: \"mod:\" + n) (changed \"modules\"))
  ")"
  rm -rf "$a" "$b"
  [ "$got" = "$3" ] || fail "$1: editing $2 changed [$got], want [$3]"
  echo "ok: $1: editing $2 changes [$3]"
}

# Without the option, evaluation must say what to set. The option is
# turned off explicitly, in case nix.conf turns it on.
check_exec_error() {
  local msg
  if msg="$(nix build --option allow-unsafe-native-code-during-evaluation false \
    --no-link "$flake#fixtures.hello-deps" 2>&1)"; then
    fail "building without builtins.exec succeeded"
  fi
  case "$msg" in
    *allow-unsafe-native-code-during-evaluation*) echo "ok: missing builtins.exec is explained" ;;
    *) fail "unhelpful error without builtins.exec: $msg" ;;
  esac
}

# With one binary, nix run finds it through meta.mainProgram.
check_main_program() {
  local got
  got="$(nix eval "${exec_opt[@]}" --raw "$flake#fixtures.$1.meta.mainProgram")" ||
    fail "$1: meta.mainProgram does not evaluate"
  [ "$got" = "$2" ] || fail "$1: meta.mainProgram is '$got', want '$2'"
  echo "ok: $1: meta.mainProgram is $2"
}

# Choosing a Go version is mkGoEnv's most common customisation; passing
# only go must work.
check_go_override() {
  local got
  got="$(nix eval --impure --raw --expr "
    let
      flake = builtins.getFlake \"$flake\";
      pkgs = flake.inputs.nixpkgs.legacyPackages.\${builtins.currentSystem};
    in (flake.lib.mkGoEnv { inherit pkgs; go = pkgs.go_1_25; }).go.version
  ")" || fail "mkGoEnv with go = pkgs.go_1_25 does not evaluate"
  case "$got" in
    1.25*) echo "ok: mkGoEnv accepts go = pkgs.go_1_25" ;;
    *) fail "mkGoEnv with go = pkgs.go_1_25 uses Go $got" ;;
  esac
}

# The fetch derivation normally never runs, because resolve pre-seeds its
# output. Force it to run and let nix compare the result with the store.
check_fetch_fallback() {
  nix build "${exec_opt[@]}" --rebuild --no-link "$flake#fixtures.$1.modules.\"$2\"" ||
    fail "$1: the fetch derivation for $2 does not reproduce the pre-seeded module"
  echo "ok: $1: fetching $2 reproduces the pre-seeded module"
}

check_run hello-deps hello "hello, gonixgo"
check_modinfo hello-deps hello .
check_main_program hello-deps hello

check_run asm-embed asmembed "3 hi [extra.txt index.html] 1.2.3"
check_run asm-embed second "second"
check_modinfo asm-embed asmembed .
check_modinfo asm-embed second ./cmd/second
check_distinct_build_ids asm-embed asmembed second

check_run nethttp nethttp "pong true"
check_modinfo nethttp nethttp .

# A package edit rebuilds it, its importers and the link. Nothing else.
check_incremental hello-deps internal/greet/greet.go \
  "example.com/hello example.com/hello/internal/greet bin:hello"
check_incremental hello-deps main.go "example.com/hello bin:hello"
# A file no package uses changes nothing.
check_incremental hello-deps NOTES.md ""

check_exec_error
check_go_override
check_fetch_fallback hello-deps "github.com/mattn/go-isatty@v0.0.20"

echo "all integration checks passed"
