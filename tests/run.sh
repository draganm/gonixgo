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

# check_modinfo <fixture> <binary> <main package, relative to the fixture> [shell]
# The module info must match what `go build -trimpath` embeds. A fixture
# whose reference build needs libraries names the flake's shell that has
# them.
check_modinfo() {
  local out go tmp
  local -a in_shell=()
  out="$(build "fixtures.$1")"
  go="$(build "fixtures.$1.go")/bin/go"
  if [ -n "${4:-}" ]; then
    in_shell=(nix develop "$flake#$4" --command)
  fi
  tmp="$(mktemp -d)"
  (cd "$root/tests/fixtures/$1" &&
    GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local \
      ${in_shell[@]+"${in_shell[@]}"} "$go" build -trimpath -buildvcs=false -o "$tmp/ref" "$3")
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

# check_no_source_refs <fixture>
# The C compiler records where its sources are. None of that may make the
# result depend on a source tree or on an intermediate derivation.
check_no_source_refs() {
  local out refs
  out="$(build "fixtures.$1")"
  refs="$(nix-store --query --references "$out")"
  if grep -E -- '-(gosrc|gomod|golocal|gopkg|gobin)-' <<<"$refs"; then
    fail "$1: the result refers to the build inputs listed above"
  fi
  echo "ok: $1: the result refers to no source tree or intermediate derivation"
}

# check_stdenv <attribute under legacyPackages.<system>> <true|false>
# Only cgo packages and the binaries that contain them build with stdenv
# and its C compiler; everything else keeps the tool as its builder.
check_stdenv() {
  local got
  got="$(nix eval "${exec_opt[@]}" "$flake#$1" --apply 'drv: drv ? stdenv')" ||
    fail "$1 does not evaluate"
  [ "$got" = "$2" ] || fail "$1: built with stdenv is $got, want $2"
  echo "ok: $1: built with stdenv: $2"
}

# cgo_fixture_with <packageOverrides> <expression over app>
# Evaluates the expression with app bound to the cgo fixture built with the
# given overrides; pkgs is the flake's nixpkgs.
cgo_fixture_with() {
  nix eval --impure --raw "${exec_opt[@]}" --expr "
    let
      flake = builtins.getFlake \"$flake\";
      pkgs = flake.inputs.nixpkgs.legacyPackages.\${builtins.currentSystem};
      app = flake.legacyPackages.\${builtins.currentSystem}.goEnv.buildGoApplication {
        pname = \"overrides\";
        src = $root/tests/fixtures/cgo;
        packageOverrides = $1;
      };
    in $2"
}

# An entry for a package's import path wins over one for its module, which
# is what every other cgo package of the module gets.
check_override_lookup() {
  local got
  got="$(cgo_fixture_with '{
      "example.com/cgofix".buildInputs = [ pkgs.zstd ];
      "example.com/cgofix/internal/lz4".buildInputs = [ pkgs.lz4 ];
    }' '
      let inputs = pkg: toString (map (i: i.pname) app.packages.${pkg}.buildInputs);
      in "lz4: ${inputs "example.com/cgofix/internal/lz4"}; zstd: ${inputs "example.com/cgofix/internal/zstd"}"
    ')" || fail "packageOverrides: the lookup does not evaluate"
  [ "$got" = "lz4: lz4; zstd: zstd" ] ||
    fail "packageOverrides: packages got the inputs [$got], want [lz4: lz4; zstd: zstd]"
  echo "ok: packageOverrides: an import path entry wins over the module's"
}

# A misspelt attribute must not be ignored.
check_override_typo() {
  local msg
  if msg="$(cgo_fixture_with '{ "example.com/cgofix/internal/lz4".buildInput = [ ]; }' 'app.drvPath' 2>&1)"; then
    fail "packageOverrides: an unknown attribute was accepted"
  fi
  case "$msg" in
    *'packageOverrides."example.com/cgofix/internal/lz4".buildInput'*)
      echo "ok: packageOverrides: an unknown attribute is rejected by name" ;;
    *) fail "packageOverrides: unhelpful error for an unknown attribute: $msg" ;;
  esac
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

# cgo: C, C++, Objective-C and assembly, a third-party cgo module, and two
# libraries from packageOverrides.
# The last word is what __FILE__ is in a cgo file: as under go build
# -trimpath, the module's name stands for wherever the source is.
check_run cgo cgofix "3 8 7 4 4 zstd true saved pure /_/example.com/cgofix/internal/cadd/where.go"
check_modinfo cgo cgofix . cgoShell
check_no_source_refs cgo
check_stdenv 'fixtures.cgo.packages."example.com/cgofix/internal/cadd"' true
check_stdenv 'fixtures.cgo.packages."example.com/cgofix/internal/pure"' false
check_stdenv 'fixtures.cgo.bins.cgofix' true
check_stdenv 'fixtures.nethttp.bins.nethttp' false
check_override_lookup
check_override_typo

# A package edit rebuilds it, its importers and the link. Nothing else.
check_incremental hello-deps internal/greet/greet.go \
  "example.com/hello example.com/hello/internal/greet bin:hello"
check_incremental hello-deps main.go "example.com/hello bin:hello"
# A file no package uses changes nothing.
check_incremental hello-deps NOTES.md ""
# The same holds for a cgo package's C file, and for a header in a
# directory that only a #cgo directive names.
check_incremental cgo internal/cadd/add.c \
  "example.com/cgofix example.com/cgofix/internal/cadd bin:cgofix"
check_incremental cgo internal/cadd/include/add.h \
  "example.com/cgofix example.com/cgofix/internal/cadd bin:cgofix"
check_incremental cgo NOTES.md ""

check_exec_error
check_go_override
check_fetch_fallback hello-deps "github.com/mattn/go-isatty@v0.0.20"

echo "all integration checks passed"
