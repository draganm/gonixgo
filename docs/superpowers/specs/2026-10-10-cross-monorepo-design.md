# gonixgo cross-compilation and monorepo design

Date: 2026-10-10

This is stage 2 of the build order in the
[gonixgo design](2026-10-03-gonixgo-design.md). It fills in that document's
Cross-compilation and Monorepo layout sections and replaces them where the
two differ; the differences are listed under
[Departures from the 2026-10-03 design](#departures-from-the-2026-10-03-design).

## Goal

Build a program for a platform other than the one that builds it, and build
programs whose `go.mod` replaces dependencies, as a monorepo does for its
sibling modules.

Today `mkGoEnv` takes the target from its `go`, which defaults to
`pkgs.buildPackages.go`. In a cross package set that Go is built to compile
for the target and needs a cross GCC, which no binary cache holds for macOS,
so the first cross build compiles GCC. `GOARM` is not passed on. A cross
build gets Go's cgo default, which nixpkgs' Go turns on, so even a pure-Go
program needs a C toolchain for the target. The resolver rejects every
`replace` directive.

### Success criteria

- With `pkgs = pkgs.pkgsCross.aarch64-multiplatform`, a pure-Go program
  builds on an aarch64-darwin Mac without building a C toolchain, and its
  module info matches `GOOS=linux GOARCH=arm64 go build -trimpath`.
- A Raspberry Pi build (`pkgsCross.raspberryPi`) records `GOARM=6` in its
  module info, as `go build` does with `GOARM=6`.
- In a cross build, `CGO_ENABLED = 1` builds cgo packages with the cross C
  toolchain and the target's libraries from `packageOverrides`. Left at
  `null`, cgo is off, and a package that needs it fails evaluation with a
  line saying how to turn it on.
- A native build uses the same Go, standard library and C toolchain
  derivations as before.
- A program whose module replaces a dependency with another version, or with
  a directory inside `src`, builds. Its module info matches
  `go build -trimpath`, and the source paths recorded in the binary are the
  ones `go build -trimpath` records.
- A directory `replace` that points outside `src`, and a `modRoot` without a
  `go.mod`, fail during evaluation with a message that says what to change.

## Scope

In this version:

- Cross builds, chosen by the `pkgs` given to `mkGoEnv`: any package set
  whose build and host platforms differ. Only `pkgsCross` sets are tested.
- `GOOS`, `GOARCH` and `GOARM` from the target platform.
- cgo in cross builds, when asked for.
- `replace` with a module version, under the same path or another, and with
  a directory inside `src`.
- Checks on `modRoot`.

Not in this version; see [Not in this version](#not-in-this-version): tests
of cross builds, architecture levels other than `GOARM`, a directory
`replace` outside `src`, `go.work`, and tests of directory-replaced modules.

Verified on aarch64-darwin only, like the rest of gonixgo.

## Facts verified

Probed with Go 1.26.7, Nix 2.26.1 and nixpkgs `nixos-26.05` on
aarch64-darwin. The probe modules have a directory `replace`, a version
`replace` under the same path, and one under another path.

| Fact | Evidence |
|---|---|
| In a `pkgsCross` set, `pkgs.buildPackages.go` runs on the build platform but is built for the target: its `GOOS` and `GOARCH` are the target's, and its `CC_FOR_TARGET` is the cross GCC, which it needs in order to build. | `nix eval` of `pkgsCross.aarch64-multiplatform.buildPackages.go`. |
| The cache holds no cross C toolchain for an aarch64-darwin build machine: aarch64-linux's GCC and glibc are 12 derivations to build, x86_64-darwin's clang 17. | `nix build --dry-run`. |
| `pkgs.pkgsBuildBuild.go` is the native Go. In a cross set it is the derivation that `pkgs.go` is natively, which the cache holds. The same goes for the tool built from `pkgsBuildBuild`. | `drvPath` comparisons. |
| `pkgs.stdenv.hostPlatform.go` is `{ GOOS; GOARCH; GOARM; }`: `linux`, `arm64`, `""` for `aarch64-multiplatform`; `linux`, `arm`, `"6"` for `raspberryPi`; `darwin`, `amd64`, `""` for `x86_64-darwin`. | `nix eval`. |
| The native Go cross-compiles a pure-Go program given only `GOOS` and `GOARCH`: `hello-deps` built for linux/arm64 in 15 seconds, and its module info matched `GOOS=linux GOARCH=arm64 go build -trimpath`. | A build with `go` and the target overridden in `mkGoEnv`. |
| nixpkgs' Go reports `CGO_ENABLED=1` for cross targets too. | `go env CGO_ENABLED` with another platform's `GOOS` and `GOARCH`. |
| In a cross stdenv, `$CC` and `$CXX` name the cross compilers, and pkg-config's wrapper sets `$PKG_CONFIG` to the target's. gonixgo already takes the C compiler from `$CC` and pkg-config from `$PKG_CONFIG`, and links with `-extld=$CC`. | nixpkgs' cc-wrapper and pkg-config wrapper; `internal/gotool`, `internal/cc/pkgconfig.go`, `internal/link`. |
| Natively, `pkgs.runCommandCC` and `pkgs.buildPackages.runCommandCC` make the same derivation; in a cross set they differ. | `drvPath` comparison. |
| `go.stdenv.hostPlatform` says where a nixpkgs Go runs: on the build platform for `pkgsBuildBuild.go` and `buildPackages.go`, on the target for a cross set's own `go`. | `nix eval`. |
| nixpkgs' `canExecute` says aarch64-darwin cannot run x86_64-darwin code, so stdenv skips such a build's tests. macOS runs that code under Rosetta. | `nix eval`; `arch -x86_64`. |
| For a package of a replaced module, `go list -json` reports `Module.Path` and `Module.Version` from the `require` line, `Module.Dir` and `Module.GoVersion` from the replacement, and `Module.Replace` with the replacement's `Path`, `Version`, `Dir`, `GoVersion` and, for a version, `Sum`. A directory replacement's `Path` is as written in `go.mod` (`../lib`), with no version or sum. | `go list -deps -json` on the probe. |
| `go.sum` holds lines for the replacement only. | The probe's `go.sum` after `go mod tidy`. |
| cmd/go compiles a replaced package with `-lang` from the replacement's `go` directive. Under `-trimpath` it rewrites the package directory to `<path>@<version>` followed by the import path below `<path>`, with the path and version from the `require` line, for both kinds: the probe recorded `example.com/monorepo/lib@v0.0.0-00010101000000-000000000000/lib.go` and `github.com/Sirupsen/logrus@v1.0.0/logger.go`. A main-module package gets its import path, because the main module has no version. | The probe's output: a closure over a loop variable, `runtime.Caller` and `runtime.FuncForPC`; `Action.trimpath` in `cmd/go/internal/work/gc.go`. |
| cmd/go's C compiler prefix map sends the module directory, the replacement's for a replaced module, to `/_/<path>@<version>`, or `/_/<path>` for the main module. | `ccompile` in `cmd/go/internal/work/exec.go`. |
| Module info for a replaced module is the `dep` line with the path and version from the `require` line and no sum, then `=>` with the replacement's path, version and sum, then an empty line. A directory replacement shows its path as written, the version `(devel)` and an empty sum. | `go version -m` on the probe; `BuildInfo.String` in `runtime/debug`. |
| When a directory replacement does not exist, `go list -e` reports an error on each of its packages: `example.com/lib@v1.2.3: replacement directory ../lib does not exist`. | The probe without `../lib`. |
| A replacement under another path whose packages import their own path, such as `github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.9.3`, builds. `go mod tidy` then also requires the new path, at its latest version, and its test dependencies. | `go mod tidy` on the probe. |

## Cross-compilation

### The Go that builds, and the target

- `mkGoEnv`'s `go` defaults to `pkgs.pkgsBuildBuild.go`, the cached native
  Go. In a native build it is the derivation the old default was.
- `mkGoEnv` asserts that `go` runs on the build platform, when `go` has a
  `stdenv` as nixpkgs' Go does:
  `pkgs.stdenv.buildPlatform.canExecute go.stdenv.hostPlatform`. Otherwise a
  cross set's own `go_1_25`, which runs on the target, would cost a cross
  toolchain build and then fail during evaluation with "exec format error".
  The message suggests `pkgs.pkgsBuildBuild.go_1_25`.
- The target is `pkgs.stdenv.hostPlatform.go`. Its `GOOS`, `GOARCH` and
  `GOARM` (`""` except for 32-bit ARM) go to:
  - the resolver, which sets them for `go env` and `go list`, so file lists,
    build constraints and the module info's settings are the target's;
  - the standard library's derivation;
  - every compile and link manifest. The tool sets them for each `go tool`
    run.
- Without `GOARM` an ARMv6 board would get code for ARMv7, Go's default, and
  module info would record `GOARM=7`.
- The tool is built from `pkgs.pkgsBuildBuild`, and so is the evaluation tool
  unless `evalPkgs` is given. Cross and native builds share it.

### cgo

- A build is cross when `pkgs.stdenv.buildPlatform != pkgs.stdenv.hostPlatform`,
  nixpkgs' own test. `buildGoApplication` passes the result to the resolver
  as `cross`.
- In a cross build, `CGO_ENABLED = null` means off: the resolver sets
  `CGO_ENABLED=0`. Native builds keep Go's default, which nixpkgs' Go turns
  on.
- With `CGO_ENABLED = 1`, these build with `pkgs.stdenv`, whose `$CC`, `$CXX`
  and `$PKG_CONFIG` are the cross tools:
  - the standard library's cgo variant, which moves from
    `pkgs.buildPackages.runCommandCC` to `pkgs.runCommandCC`, natively the
    same derivation;
  - cgo compiles, and the links that contain cgo, which already use
    `pkgs.stdenv`.
- `packageOverrides` entries work as written. Taken from a cross `pkgs`,
  their `buildInputs` are the target's libraries and their
  `nativeBuildInputs` run on the build platform, through nixpkgs' splicing.
- The README warns that a package with a `!cgo` fallback, such as
  `github.com/mattn/go-sqlite3`, builds with cgo off and fails only when it
  runs.

### Tests

Unchanged: tests run only when
`pkgs.stdenv.buildPlatform.canExecute pkgs.stdenv.hostPlatform`. A Linux
build on a Mac and an x86_64-darwin build on Apple silicon skip them.

### `evalPkgs`

Unchanged. It serves a build whose platform the evaluating machine cannot
build for, such as a Linux build from a Mac through a remote builder:
`mkGoEnv { pkgs = <x86_64-linux nixpkgs>; evalPkgs = <the Mac's nixpkgs>; }`.
The resolver then runs `evalPkgs`' tool and Go. A cross build needs no
`evalPkgs`: its build platform is the evaluating machine.

## `replace` and `modRoot`

### `modRoot`

The resolver already runs in `src/modRoot` and reads `go.sum` there, and
local paths are relative to `src`. It now checks `modRoot` before running
Go:

- An absolute `modRoot`, or one that leaves `src` through `..`:
  `modRoot "../x" must be a directory inside src`.
- A `modRoot` without `go.mod`:
  `modRoot "x": no go.mod in /nix/store/…-source/x`.

`""` means `"."`.

### Package kinds

The resolver classifies each package outside the standard library by the
module `go list` reports for it. Every kind keeps its import paths.

| Module | Kind | Source | Compile name | Fetched module |
|---|---|---|---|---|
| The main module | local | a filtered copy of `src` | `golocal-<import path>` | none |
| `a v1`, not replaced | third-party | `gomod-<a>-<v1>` | `gopkg-<import path>-<v1>` | `a@v1` |
| `a v1` replaced by `b v2` | third-party | `gomod-<b>-<v2>` | `gopkg-<import path>-<v2>` | `b@v2`, its sum from `go.sum` |
| `a v1` replaced by a directory inside `src` | local | a filtered copy of `src` | `golocal-<import path>` | none |
| `a v1` replaced by a directory outside `src` | an error | | | |

A third-party package's subdirectory is relative to `Module.Dir`, which is
the replacement's directory. One fetch serves every module replaced by the
same `b@v2`.

### What a replaced package gets

- `-lang` from the replacement's `go` directive: the `Module.GoVersion` the
  code already reads.
- The trim path cmd/go uses: `<a>@<v1>` followed by the import path below
  `a`. One function gives the trim path of a package and of its test copies:
  the import path for the main module, this form for every other module.
  Today a test copy's trim path is recomputed, from the import path for a
  local package and from the fetched module otherwise; both are wrong for a
  replaced package.
- The C compiler's prefix map, which the existing `trimRoot` derives from the
  trim path: the replacement's directory maps to `/_/<a>@<v1>`.
- `packageOverrides` keys as before: the import path, or the module path
  `a`. A fork is overridden under the path the code imports, not the fork's.

### Tested set

The tested set becomes the main-module packages with test files. Today it is
every local package with test files, which would take in a directory-replaced
module's packages. This matches `go test ./...` in `modRoot` and the
[tests design](2026-10-10-tests-design.md).

### Module info

`modinfo.Module` gains `Replace *Module`. `Info.String` writes a replaced
module as cmd/go does, `dep\t<a>\t<v1>\n=>\t<b>\t<v2>\t<sum>\n\n`; a
directory replacement has its path as written in `go.mod`, `(devel)` and an
empty sum.

The graph keeps the module-info entry of every module other than the main
one that provides a package, keyed by the module path `a`. A binary's `dep` lines come from these
entries. Today they come from the fetched modules, which a directory
replacement does not have and a version replacement keys by `b@v2`.

### Errors

- A directory replacement that does not exist, the usual case because only
  `src` is in the store: Go's message, with the other load problems.
- One that exists outside `src`, as when `src` is a directory inside another
  store path:
  `replace example.com/lib => ../lib: /nix/store/…/lib is outside src /nix/store/…/app`,
  once per module.
- Either way the load error ends with
  `a directory replace must point inside src; set src to a directory that holds both modules and modRoot to the one with go.mod`.

## Interfaces

### `gonixgo resolve`

Two new arguments:

| Argument | Type | Meaning |
|---|---|---|
| `goarm` | string | `GOARM` for `go env` and `go list`; `""` for none. |
| `cross` | bool | The build is cross. With `cgoEnabled = null` it means `CGO_ENABLED=0`, and a load error of the first pass ends with `cgo is off in a cross build; set CGO_ENABLED = 1 to build cgo packages`. |

### Compile and link manifests

A `goarm` field, present only when `GOARM` is set, so the manifests of other
targets do not change. `gotool.New` takes it and sets `GOARM` in every tool
run's environment, including its own `go env`, whose value picks the
assembler's `GOARM_*` defines.

### Nix

- `nix/mk-go-env.nix`: the default `go`; the target from
  `pkgs.stdenv.hostPlatform.go`; the tools from `pkgs.pkgsBuildBuild`; the
  assertion on `go`; `goarm` to the standard library, the builders and
  `buildGoApplication`; `pkgs.runCommandCC` to the standard library.
- `nix/stdlib.nix`: `GOARM` in the environment when set; the name gets
  `v<GOARM>` after the architecture, as in `go-stdlib-1.26.7-linux-armv6`;
  the cgo variant uses the `runCommandCC` it is given.
- `nix/builders.nix`: `goarm` in the manifests.
- `nix/build-go-application.nix`: `goarm` and `cross` to the resolver.

## Error handling

| Situation | Behaviour |
|---|---|
| `go` does not run on the build platform | `mkGoEnv` assertion naming both platforms and suggesting `pkgs.pkgsBuildBuild.go` or one of its versions. |
| A cross build with `CGO_ENABLED = null` reaches a package that needs cgo | The load problems, typically "build constraints exclude all Go files", then the cgo line. |
| A cross build with `CGO_ENABLED = 1` | Nix builds the cross C toolchain when the cache lacks it. Not an error. |
| `modRoot` absolute, outside `src`, or without `go.mod` | Error naming `modRoot`. |
| A directory `replace` whose directory does not exist | Go's message with the other load problems, then the replace hint. |
| A directory `replace` outside `src` | Error naming the directive and both paths, then the replace hint. |
| `go.sum` lacks a replacement's line | Go's message, then the `go mod tidy` hint, as for any module. |

## Not in this version

- **Tests of cross builds.** Skipped when the build platform cannot run the
  target, as stdenv skips `doCheck`.
- **Architecture levels other than `GOARM`:** `GOAMD64`, `GOARM64` and the
  rest. nixpkgs' platforms do not carry them; Go's defaults apply, and module
  info records them.
- **A directory `replace` outside `src`.** Make `src` the directory that
  holds both modules.
- **`go.work`.** Still forced off; each module's `go.mod` needs its own
  `replace` lines.
- **Tests of directory-replaced modules.** As in the tests design.

## Departures from the 2026-10-03 design

| 2026-10-03 design | This design | Why |
|---|---|---|
| `go` defaults to `pkgs.buildPackages.go`, and `GOOS` and `GOARCH` come from it. | `go` defaults to `pkgs.pkgsBuildBuild.go`, and `GOOS`, `GOARCH` and `GOARM` come from `pkgs.stdenv.hostPlatform`. | `pkgs.buildPackages.go` needs a cross GCC that no cache holds for macOS. The native Go is cached and does not depend on the target. |
| `CGO_ENABLED = null` is Go's default for the target. | In a cross build it is off. | nixpkgs' Go defaults to on, which would make every cross build need a C toolchain for the target. |
| `evalPkgs = null` means `pkgs.buildPackages`. | It means `pkgs.pkgsBuildBuild`. | The tool then depends on nothing built for the target. |
| The standard library is `go-stdlib-<version>-<goos>-<goarch>[-cgo]`. | `v<GOARM>` follows the architecture when set. | Builds for two ARM versions would otherwise have standard libraries of the same name. |
| The `cross` fixture is a Linux arm64 build. | Linux arm64, Linux ARMv6 and x86_64-darwin with cgo, plus an `evalPkgs` build that is only evaluated. | `GOARM`, cgo and `evalPkgs` need covering. |
| The `monorepo` fixture covers `modRoot` and a directory `replace`. | It also covers a version `replace`. | Both kinds are in this stage. |

The 2026-10-03 document is edited to agree.

## Testing

### Unit tests (Go)

- **`graph`**, from recorded `go list` output:
  - a version replacement under the same path: the fetched module, its name
    and sum, and the package's subdirectory, trim path and module-info
    entry;
  - a fork under another path: the fork's path is fetched, and the original
    path is in the trim path and the `dep` line;
  - a directory replacement inside `src`: local, `-lang` from its own `go`
    directive, its trim path, and not in the tested set;
  - a directory replacement outside `src`: the error, once per module, and
    the hint;
  - Go's missing-directory error gets the hint;
  - a test copy of a package of each replaced kind keeps the package's trim
    path.
- **`modinfo`:** `String` with both kinds of replacement equals
  `runtime/debug`'s `BuildInfo.String` for the same modules.
- **`resolve`**, with real Go:
  - `modRoot` absolute, outside `src`, and without `go.mod`;
  - `goarm` reaches `go env`, so module info records `GOARM=6`;
  - with `cross` and no `cgoEnabled`, a cgo-only package fails with the cgo
    line; with `cgoEnabled` given, the line is absent.
- **`gotool`:** `GOARM` is in the tool environment, and `GOARM=6` gives the
  assembler `GOARM_6` and `GOARM_5`.

### Integration fixture `monorepo`

```
tests/fixtures/monorepo/
  app/  example.com/monorepo/app, go 1.22, the main module. Requires lib
        through replace => ../lib, and go-cmp v0.6.0 replaced by v0.7.0.
        The program prints the source paths recorded for its own file,
        for lib's and for go-cmp's Equal, then lib.Captured(). Its test
        passes.
  lib/  example.com/monorepo/lib, go 1.21. Where() returns runtime.Caller's
        file; Captured() collects a loop variable through closures. Its
        test always fails.
```

`tests/fixtures.nix` builds it with `modRoot = "app"`; `monorepoWith` lays
other arguments over those.

`tests/run.sh` asserts:

- **Output:** `example.com/monorepo/app/main.go
  example.com/monorepo/lib@v0.0.0-00010101000000-000000000000/lib.go
  github.com/google/go-cmp@v0.6.0/cmp/compare.go [3 3 3]`, on one line.
  `[3 3 3]` shows that `lib` compiled with its own `go` directive; with Go
  1.22's semantics it would be `[0 1 2]`.
- **Module info** matches `go build -trimpath` run in `app`.
- **Tested set:** `example.com/monorepo/app` alone. That the build passes
  shows `lib`'s test did not run.
- **Fetch:** the `github.com/google/go-cmp@v0.7.0` fetch derivation
  reproduces the pre-seeded module.
- **References:** the binary refers to no source.
- **Errors:**
  - `src = ./fixtures/monorepo/app` with `modRoot = "."`: Go's
    missing-directory message and the hint.
  - `src = "${./fixtures/monorepo}/app"` with `modRoot = "."`: the
    outside-`src` error naming `replace example.com/monorepo/lib => ../lib`,
    and the hint.
  - `modRoot = "nope"`: the `modRoot` error.

A replacement under another path is covered by the `graph` unit tests only.
The well-known one, the logrus rename, drags unrelated modules into
`go.mod` (see [Facts verified](#facts-verified)), and a fixture should not
depend on a third-party fork.

### Integration fixtures for cross builds

| Attribute | Builds | Checks |
|---|---|---|
| `hello-deps-aarch64-linux` | `hello-deps` from `pkgsCross.aarch64-multiplatform` | `file` reports an ARM aarch64 ELF; module info matches `GOOS=linux GOARCH=arm64 go build -trimpath`; no source references. |
| `hello-deps-armv6l-linux` | `hello-deps` from `pkgsCross.raspberryPi` | `file` reports a 32-bit ARM ELF; module info matches `GOOS=linux GOARCH=arm GOARM=6 go build -trimpath`. |
| `cgo-x86_64-darwin`, on aarch64-darwin only | the `cgo` fixture from `pkgsCross.x86_64-darwin` with `CGO_ENABLED = 1` and that set's zstd, lz4 and pkg-config | It runs under Rosetta and prints what the native fixture prints; `file` reports x86_64; module info matches `GOARCH=amd64 CGO_ENABLED=1 go build -trimpath` in `cgoShellX86_64Darwin`; `cadd` and the link build with stdenv. |
| `cgo-aarch64-linux` | the `cgo` fixture from `pkgsCross.aarch64-multiplatform`, `CGO_ENABLED` left `null` | Evaluation fails with the cgo line. Nothing is built. |
| `hello-deps-x86_64-linux` | `hello-deps` for x86_64-linux, with `evalPkgs` this machine's package set | The `drvPath` evaluates, and the derivations are for x86_64-linux. Nothing is built. |

The first run builds the x86_64-darwin C toolchain, zstd and lz4. Without
Rosetta the run check is skipped with a note.

`check_modinfo` learns the reference build's directory and environment. The
fixtures take `mkGoEnv` and the x86_64-linux package set from the flake.

## Docs

- **README:**
  - `mkGoEnv`: `go` defaults to `pkgs.pkgsBuildBuild.go` and must run on the
    build platform; `evalPkgs = null` means `pkgs.pkgsBuildBuild`.
  - `CGO_ENABLED`: `null` is Go's default natively, which nixpkgs' Go turns
    on, and off in a cross build.
  - A cross-compilation section: the target comes from `pkgs`; a Go version
    is chosen with `pkgs.pkgsBuildBuild.go_1_25`; cgo and the `!cgo`
    warning; skipped tests; `evalPkgs` for remote builders.
  - A monorepo section: `modRoot`, both kinds of `replace`, overriding a
    fork's packages, `go.work`, and the fix for a directory outside `src`.
  - Not yet supported: `replace` directives and cross-compilation come off
    the list.
- **2026-10-03 design:** the `mkGoEnv` and `CGO_ENABLED` rows, the
  Cross-compilation section, the derivations table and the fixture table.

## Changes by file

| File | Change |
|---|---|
| `nix/mk-go-env.nix` | The default `go`, the target, the tools from `pkgsBuildBuild`, the assertion on `go`; `goarm` and `pkgs.runCommandCC` passed on. |
| `nix/stdlib.nix` | `GOARM`, the name, `runCommandCC` from `pkgs`. |
| `nix/builders.nix` | `goarm` in the manifests. |
| `nix/build-go-application.nix` | `goarm` and `cross` for the resolver. |
| `internal/resolve/resolve.go` | `goarm`, `cross`, the cgo default and its line, the `modRoot` checks. |
| `internal/golist/golist.go` | `GOARM` in `Options`. |
| `internal/graph/pkg.go` | The package kinds, the trim path function, the outside-`src` error. |
| `internal/graph/graph.go` | Module-info entries by module path, the tested set from the main module, the replace hint. |
| `internal/graph/tests.go` | Test copies use the trim path function. |
| `internal/modinfo/modinfo.go` | `Replace` and its format. |
| `internal/gotool/gotool.go` | `GOARM`. |
| `internal/compile/compile.go`, `internal/link/link.go` | `goarm` from the manifest to `gotool.New`. |
| `flake.nix` | `mkGoEnv` and the x86_64-linux package set for the fixtures; `cgoShellX86_64Darwin`. |
| `tests/fixtures.nix`, `tests/fixtures/monorepo/`, `tests/run.sh` | The fixtures and their checks. |
| `README.md`, `docs/superpowers/specs/2026-10-03-gonixgo-design.md` | As under [Docs](#docs). |
