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

# check_incremental <fixture> <file to append to> <what must change> [builder]
# Evaluates the fixture from two copies that differ in one file and lists
# the nodes whose derivations differ: package import paths, then
# bin:<name>, mod:<key>, testpkg:<key>, testbin:<import path> and
# test:<import path>. The builder, a Nix function from the source to the
# application, defaults to buildGoApplication with only pname and src.
check_incremental() {
  local a b got
  local builder='src: goEnv.buildGoApplication { pname = "incremental"; inherit src; }'
  builder="${4:-$builder}"
  a="$(mktemp -d)"
  b="$(mktemp -d)"
  cp -R "$root/tests/fixtures/$1/." "$a/"
  cp -R "$root/tests/fixtures/$1/." "$b/"
  printf '\n// edited\n' >>"$b/$2"
  got="$(nix eval --impure --raw "${exec_opt[@]}" --expr "
    let
      inherit ((builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}) goEnv fixtures;
      build = $builder;
      a = build $a;
      b = build $b;
      changed = set: builtins.filter
        (n: a.\${set}.\${n}.drvPath != b.\${set}.\${n}.drvPath)
        (builtins.attrNames a.\${set});
    in builtins.concatStringsSep \" \" (
      changed \"packages\"
      ++ map (n: \"bin:\" + n) (changed \"bins\")
      ++ map (n: \"mod:\" + n) (changed \"modules\")
      ++ map (n: \"testpkg:\" + n) (changed \"testPackages\")
      ++ map (n: \"testbin:\" + n) (changed \"testBins\")
      ++ map (n: \"test:\" + n) (changed \"tests\"))
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
  if grep -E -- '-(gosrc|gomod|golocal|gopkg|gobin|gotest|gotestpkg|gotestbin)-' <<<"$refs"; then
    fail "$1: the result refers to the build inputs listed above"
  fi
  echo "ok: $1: the result refers to no source tree or intermediate derivation"
}

# check_stdenv <attribute under legacyPackages.<system>> <true|false>
# Only cgo packages and the binaries that contain them build with stdenv
# and its C compiler; everything else keeps the tool as its builder.
# The attribute is read through an expression: a test package's name has a
# space in it, which an installable cannot hold.
check_stdenv() {
  local got
  got="$(nix eval --impure "${exec_opt[@]}" --expr "
    (builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}.$1 ? stdenv")" ||
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

# An entry that no cgo package takes changes nothing, so it is most likely
# a mistake. It is a warning, since the key may match on another platform.
check_override_unmatched() {
  local msg
  msg="$(cgo_fixture_with '{
      "example.com/cgofix/internal/latee".buildInputs = [ ];
      "example.com/cgofix/internal/pure".buildInputs = [ ];
      "example.com/cgofix/internal/lz4".buildInputs = [ pkgs.lz4 ];
    }' 'app.drvPath' 2>&1)" || fail "packageOverrides: a key that matches nothing stops the evaluation: $msg"
  case "$msg" in
    *'packageOverrides."example.com/cgofix/internal/latee", packageOverrides."example.com/cgofix/internal/pure" match no cgo package'*) ;;
    *) fail "packageOverrides: no warning for the keys that match no cgo package: $msg" ;;
  esac
  case "$msg" in
    *'packageOverrides."example.com/cgofix/internal/lz4"'*)
      fail "packageOverrides: a warning names a key that a cgo package takes: $msg" ;;
  esac
  echo "ok: packageOverrides: keys that match no cgo package are warned about"
}

# goEnv.builders is exported; a caller that predates packageOverrides
# passes none.
check_builders_without_overrides() {
  local got
  got="$(nix eval --impure "${exec_opt[@]}" --expr "
    let goEnv = (builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}.goEnv;
    in (goEnv.builders { srcStr = \"/nonexistent\"; cgoEnabled = false; ldflags = [ ]; }) ? compile
  ")" || fail "goEnv.builders does not evaluate without packageOverrides"
  [ "$got" = "true" ] || fail "goEnv.builders without packageOverrides gave $got"
  echo "ok: goEnv.builders works without packageOverrides"
}

# tests_with <function from the default arguments to the changes> <expression over app>
# Evaluates the expression with app bound to the tests fixture built with
# the changes, and flake to this flake.
tests_with() {
  nix eval --impure --raw "${exec_opt[@]}" --expr "
    let
      flake = builtins.getFlake \"$flake\";
      app = flake.legacyPackages.\${builtins.currentSystem}.fixtures.testsWith ($1);
    in $2"
}

# check_tested_set <fixture> <tested import paths, space separated>
check_tested_set() {
  local got set
  for set in tests testBins; do
    got="$(nix eval "${exec_opt[@]}" --raw "$flake#fixtures.$1.$set" --apply 'set: builtins.concatStringsSep " " (builtins.attrNames set)')"
    [ "$got" = "$2" ] || fail "$1: $set are [$got], want [$2]"
  done
  echo "ok: $1: the tested packages are [$2]"
}

# check_test_log <fixture> <import path> <text the log must have> <text it must not>
check_test_log() {
  local log
  log="$(build "fixtures.$1.tests.\"$2\"")"
  grep -qF -- "$3" "$log" || fail "$1: the test log of $2 lacks '$3'"
  if grep -qF -- "$4" "$log"; then fail "$1: the test log of $2 has '$4'"; fi
  tail -n 1 "$log" | grep -q "^ok  "$'\t'"$2"$'\t' || fail "$1: the test log of $2 does not end with ok"
  echo "ok: $1: the test log of $2 has '$3' and not '$4'"
}

# check_test_modinfo <fixture> <import path> <package, relative to the fixture> [shell]
# A test binary's module info must match what `go test -c -trimpath`
# embeds.
check_test_modinfo() {
  local out go tmp bin
  local -a in_shell=()
  out="$(build "fixtures.$1.testBins.\"$2\"")"
  go="$(build "fixtures.$1.go")/bin/go"
  if [ -n "${4:-}" ]; then
    in_shell=(nix develop "$flake#$4" --command)
  fi
  bin="$(ls "$out/bin")"
  tmp="$(mktemp -d)"
  (cd "$root/tests/fixtures/$1" &&
    GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local \
      ${in_shell[@]+"${in_shell[@]}"} "$go" test -c -trimpath -o "$tmp/ref" "$3")
  if ! diff <("$go" version -m "$out/bin/$bin" | tail -n +2) <("$go" version -m "$tmp/ref" | tail -n +2); then
    rm -rf "$tmp"
    fail "$1: the module info of $2's test binary differs from go test -c -trimpath (left: gonixgo, right: go test)"
  fi
  rm -rf "$tmp"
  echo "ok: $1: the module info of $2's test binary matches go test -c"
}

# A failing test fails the build and names itself; its test binary still
# builds, to be run by hand.
check_test_failure() {
  local msg failing='(a: { checkEnv = a.checkEnv // { FIXTURE_FAIL = "1"; }; })'
  if msg="$(nix build -L --impure --no-link "${exec_opt[@]}" --expr "
    (builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}.fixtures.testsWith $failing" 2>&1)"; then
    fail "tests: the build succeeded with a failing test"
  fi
  case "$msg" in
    *'--- FAIL: TestFailSwitch'*) ;;
    *) fail "tests: the failure does not name the failing test: $msg" ;;
  esac
  # The log Nix prints has its tabs expanded.
  grep -qE 'FAIL[[:space:]]+example\.com/tests/p[[:space:]]' <<<"$msg" ||
    fail "tests: the failure does not name the package: $msg"
  nix build --impure --no-link "${exec_opt[@]}" --expr "
    ((builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}.fixtures.testsWith $failing).testBins.\"example.com/tests/p\"" ||
    fail "tests: the test binary of a failing test does not build"
  echo "ok: tests: a failing test fails the build and its binary still builds"
}

# Without doCheck nothing of the tests is resolved, and the program's own
# derivations are the ones it has with tests.
check_docheck_off() {
  local got
  got="$(tests_with '_: { doCheck = false; }' '
    let
      withTests = flake.legacyPackages.${builtins.currentSystem}.fixtures.tests;
      same = set: builtins.all (n: withTests.${set}.${n}.drvPath == app.${set}.${n}.drvPath) (builtins.attrNames app.${set});
    in builtins.toJSON {
      cmp = builtins.any (k: builtins.match "github.com/google/go-cmp@.*" k != null) (builtins.attrNames app.modules);
      tests = builtins.attrNames app.tests;
      testBins = builtins.attrNames app.testBins;
      packages = same "packages";
      bins = same "bins";
    }')" || fail "tests: doCheck = false does not evaluate"
  [ "$got" = '{"bins":true,"cmp":false,"packages":true,"testBins":[],"tests":[]}' ] ||
    fail "tests: with doCheck = false got $got"
  echo "ok: tests: doCheck = false resolves nothing for tests and keeps the program's derivations"
}

# A misspelt test attribute is an error naming it.
check_test_override_typo() {
  local msg
  if msg="$(tests_with '(a: { packageOverrides = a.packageOverrides // { "example.com/tests/xonly".checkFlag = [ ]; }; })' 'app.drvPath' 2>&1)"; then
    fail "tests: an unknown attribute was accepted"
  fi
  case "$msg" in
    *'packageOverrides."example.com/tests/xonly".checkFlag'*) echo "ok: tests: an unknown attribute is rejected by name" ;;
    *) fail "tests: unhelpful error for an unknown attribute: $msg" ;;
  esac
}

# A testExtraSrc path that is missing or leaves src is an error naming it.
check_test_extra_src_errors() {
  local msg path
  for path in nope ../outside /etc; do
    if msg="$(tests_with "(a: { packageOverrides = a.packageOverrides // { \"example.com/tests/xonly\".testExtraSrc = [ \"$path\" ]; }; })" \
      'app.tests."example.com/tests/xonly".drvPath' 2>&1)"; then
      fail "tests: testExtraSrc \"$path\" was accepted"
    fi
    case "$msg" in
      *"packageOverrides.\"example.com/tests/xonly\".testExtraSrc: \"$path\""*) ;;
      *) fail "tests: unhelpful error for testExtraSrc \"$path\": $msg" ;;
    esac
  done
  echo "ok: tests: testExtraSrc paths that are missing or leave src are rejected by name"
}

# ./shared/ means shared: the test that reads it passes.
check_test_extra_src_spelling() {
  nix build --impure --no-link "${exec_opt[@]}" --expr "
    ((builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}.fixtures.testsWith (a: {
      packageOverrides = a.packageOverrides // {
        \"example.com/tests/p\" = a.packageOverrides.\"example.com/tests/p\" // { testExtraSrc = [ \"./shared/\" ]; };
      };
    })).tests.\"example.com/tests/p\"" || fail "tests: testExtraSrc ./shared/ does not bring shared"
  echo "ok: tests: testExtraSrc ./shared/ is shared"
}

# Test attributes on a package that is not tested change nothing: a
# warning, and none when there are no tests.
check_test_override_unmatched() {
  local msg extra='"example.com/tests/q".checkFlags = [ "-v" ];'
  msg="$(tests_with "(a: { packageOverrides = a.packageOverrides // { $extra }; })" 'app.drvPath' 2>&1)" ||
    fail "tests: an entry that matches no tested package stops the evaluation: $msg"
  case "$msg" in
    *'packageOverrides."example.com/tests/q" matches no tested package'*) ;;
    *) fail "tests: no warning for an entry that matches no tested package: $msg" ;;
  esac
  msg="$(tests_with "(a: { doCheck = false; packageOverrides = a.packageOverrides // { $extra }; })" 'app.drvPath' 2>&1)" ||
    fail "tests: doCheck = false with test attributes does not evaluate: $msg"
  case "$msg" in
    *'no tested package'*) fail "tests: a warning about test attributes without tests: $msg" ;;
  esac
  echo "ok: tests: test attributes no tested package takes are warned about, and only with tests"
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
# The 10 is a macro that the env of a packageOverrides entry defines. The
# last word is what __FILE__ is in a cgo file: as under go build -trimpath,
# the module's name stands for wherever the source is.
check_run cgo cgofix "3 8 7 10 4 4 zstd true saved pure /_/example.com/cgofix/internal/cadd/where.go"
check_modinfo cgo cgofix . cgoShell
check_no_source_refs cgo
check_stdenv 'fixtures.cgo.packages."example.com/cgofix/internal/cadd"' true
check_stdenv 'fixtures.cgo.packages."example.com/cgofix/internal/pure"' false
check_stdenv 'fixtures.cgo.bins.cgofix' true
check_stdenv 'fixtures.nethttp.bins.nethttp' false
check_override_lookup
check_override_typo
check_override_unmatched
check_builders_without_overrides

# Tests: internal and external tests, testdata, test embeds, a test-only
# module and helper, a main package's and a cgo package's tests, and the
# check settings, for the program and per package.
check_run tests app "4 xonly 42"
check_modinfo tests app ./cmd/app cgoShell
check_main_program tests app
check_tested_set tests "example.com/tests/cmd/app example.com/tests/cnum example.com/tests/p example.com/tests/xonly"
check_test_log tests example.com/tests/p "=== RUN   TestCaller" "TestSkipped"
check_test_modinfo tests example.com/tests/p ./p
check_test_modinfo tests example.com/tests/cmd/app ./cmd/app cgoShell
check_no_source_refs tests
check_stdenv 'fixtures.tests.testPackages."example.com/tests/cnum [example.com/tests/cnum.test]"' true
check_stdenv 'fixtures.tests.testPackages."example.com/tests/p [example.com/tests/p.test]"' false
check_stdenv 'fixtures.tests.testPackages."example.com/tests/p.test"' false
check_test_failure
check_docheck_off
check_test_override_typo
check_test_extra_src_errors
check_test_extra_src_spelling
check_test_override_unmatched

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

# A test file, testdata and testExtraSrc change only that package's test
# nodes; a source file also changes what is built on it.
tests_builder='src: fixtures.testsWith (_: { inherit src; })'
p_test_nodes="testpkg:example.com/tests/p [example.com/tests/p.test] testpkg:example.com/tests/p.test testpkg:example.com/tests/p_test [example.com/tests/p.test] testpkg:example.com/tests/q [example.com/tests/p.test] testbin:example.com/tests/p test:example.com/tests/p"
check_incremental tests p/p_internal_test.go "$p_test_nodes" "$tests_builder"
check_incremental tests p/testdata/golden.txt "$p_test_nodes" "$tests_builder"
check_incremental tests shared/fixture.txt "$p_test_nodes" "$tests_builder"
check_incremental tests p/p.go \
  "example.com/tests/cmd/app example.com/tests/p example.com/tests/q bin:app testpkg:example.com/tests/cmd/app [example.com/tests/cmd/app.test] testpkg:example.com/tests/cmd/app.test testpkg:example.com/tests/p [example.com/tests/p.test] testpkg:example.com/tests/p.test testpkg:example.com/tests/p_test [example.com/tests/p.test] testpkg:example.com/tests/q [example.com/tests/p.test] testbin:example.com/tests/cmd/app testbin:example.com/tests/p test:example.com/tests/cmd/app test:example.com/tests/p" \
  "$tests_builder"

check_exec_error
check_go_override
check_fetch_fallback hello-deps "github.com/mattn/go-isatty@v0.0.20"

echo "all integration checks passed"
