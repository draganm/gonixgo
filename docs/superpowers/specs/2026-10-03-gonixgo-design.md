# gonixgo design

Date: 2026-10-03

## Goal

Build Go programs with Nix one package per derivation, in the style of
[go2nix](https://github.com/numtide/go2nix), with two differences:

1. **No Nix plugin.** A Nix-built binary runs at evaluation time through
   `builtins.exec` and prints Nix code describing the build of the
   application and all of its dependencies.
2. **No checked-in hashes.** A Go project never has to commit Nix code, a
   lockfile, or any other file whose content is driven by `go.mod` or
   `go.sum`. Every module hash is derived at evaluation time.

One nixpkgs instance is passed in. It builds the tool and performs the Go
build.

### Success criteria

- A flake with `src = ./.` and no lockfile builds a multi-package Go program
  with third-party dependencies.
- Editing one package rebuilds that package, the packages that import it, and
  the link. Nothing else.
- `go version -m` on the result matches a `go build -trimpath` of the same
  source.
- Changing `go.mod`/`go.sum` requires no regeneration step.

## Facts verified on Nix 2.26.1

These were probed directly and the design relies on them.

| Fact | Evidence |
|---|---|
| `builtins.exec` exists only when `allow-unsafe-native-code-during-evaluation` is true. | Without it: `error: attribute 'exec' missing`. |
| The option can be set with `--option` or `NIX_CONFIG`. | `builtins ? exec` is `true` under each. `nix.conf` reads the same setting but was not probed separately. |
| A flake's `nixConfig` **cannot** enable it. | With `nixConfig.allow-unsafe-native-code-during-evaluation = true` and `--accept-flake-config`, `builtins ? exec` is still `false`. |
| `builtins.exec` works in pure flake evaluation. | A flake output calling it evaluates without `--impure`. |
| Its stdout is parsed and evaluated as a Nix expression. | A printed function was applied by the caller. |
| Derivations in the argument list's string context are built before the program runs. | A derivation-built script was built, then executed. |
| The program inherits the caller's environment. | It saw `HOME`, `GOMODCACHE` and `nix` on `PATH`. |
| `nix store add --name N --mode nar --hash-algo sha256 DIR` yields the same store path as a fixed-output derivation named `N` with that NAR hash. | Paths were identical; a derivation whose builder is `exit 1` then "built" without running. |
| A generated function can take a filtered per-directory copy of `src` in pure evaluation. | `builtins.path { path = src + "/internal/web"; filter = …; }` returned a store path. |

## User-facing API

```nix
{
  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
  inputs.gonixgo.url = "github:draganm/gonixgo";

  outputs = { nixpkgs, gonixgo, ... }:
    let
      pkgs = nixpkgs.legacyPackages.aarch64-darwin;
      goEnv = gonixgo.lib.mkGoEnv { inherit pkgs; };
    in {
      packages.aarch64-darwin.default = goEnv.buildGoApplication {
        pname = "app";
        src = ./.;
      };
    };
}
```

Build with:

```bash
nix build --option allow-unsafe-native-code-during-evaluation true
```

Non-flake use: `import gonixgo { inherit pkgs; }` returns the same set as
`mkGoEnv`.

### `mkGoEnv`

| Argument | Default | Meaning |
|---|---|---|
| `pkgs` | required | The nixpkgs instance. Target platform is `pkgs.stdenv.hostPlatform`. |
| `go` | `pkgs.buildPackages.go` | Toolchain used inside derivations. |
| `evalPkgs` | `null` | Package set whose tool runs on the evaluating machine; `null` means `pkgs.buildPackages`. Override only when the evaluating machine differs from the build platform. |
| `evalGo` | `null` | Go that runs `go list` on the evaluating machine; `null` means `evalPkgs.go` when `evalPkgs` is given, otherwise `go`. |

Returns `{ buildGoApplication, tool, go, stdlib, builders }`. `stdlib` is a
function from the cgo setting to the standard-library derivation.

The evaluation-time Go's version must equal `go.version`; `mkGoEnv` asserts
this so the file lists resolved at evaluation match what is compiled. Passing
only `go`, for example `go = pkgs.go_1_25`, therefore works: the same Go
resolves and builds.

### `buildGoApplication`

| Argument | Default | Meaning |
|---|---|---|
| `pname` | required | Derivation name. |
| `version` | `null` | Appended to the derivation name when set. |
| `src` | required | A path or evaluation-time fetch. |
| `modRoot` | `"."` | Directory of `go.mod`, relative to `src`. |
| `subPackages` | `[ "." ]` | Main packages to link, relative to `modRoot`. |
| `tags` | `[ ]` | Build tags. |
| `ldflags` | `[ ]` | Passed to `go tool link`. |
| `CGO_ENABLED` | `null` | `null` means Go's own default for the target. |
| `doCheck` | `true` | Build and run tests of local packages. |
| `checkFlags` | `[ ]` | Passed to every test binary. |
| `packageOverrides` | `{ }` | See [cgo](#cgo) and [Tests](#tests). |
| `meta` | `{ }` | Passed through. |

The result puts each binary in `$out/bin`, named as `go build` names it: the
last element of the main package's import path, skipping a major-version
suffix such as `/v2`. `pname` does not affect binary names.
`passthru` exposes `graph`, `modules`, `packages`, `bins` and `tests`.

## Components

### 1. The `gonixgo` binary

Written in Go using only the standard library, so it builds with
`pkgs.buildGoModule { vendorHash = null; }` and gonixgo's own flake carries no
dependency hash.

| Subcommand | Runs | Purpose |
|---|---|---|
| `resolve <json>` | at evaluation, via `builtins.exec` | Runs `go list`, hashes and pre-seeds modules, prints the graph as Nix. |
| `compile` | in a derivation | Compiles one package from a JSON manifest. |
| `link` | in a derivation | Writes module info and links one binary. |
| `test` | in a derivation | Compiles, links and runs one package's tests. |
| `fetch` | in a fixed-output derivation | Fallback module download. |

Build-time subcommands read a manifest written by the builder functions.
`compile`, `link` and `test` derivations use `__structuredAttrs`, so the
manifest arrives in the attrs file and its size is not bounded by the
environment; `fetch` reads it from the `manifest` environment variable.

### 2. The static Nix library

`nix/` holds `mkGoEnv` and the builder functions the generated code calls:
`fetchModule`, `localDir`, `compile`, `link`, `test`. Each defines how one kind
of node is built. All graph wiring is in the generated code.

### 3. The generated graph

`resolve` prints one Nix function. It is produced on every evaluation and
never written to disk.

```nix
b: rec {
  goVersion = "1.26.8";
  cgoEnabled = true;

  modules."github.com/fatih/color@v1.18.0" = b.fetchModule {
    name = "gomod-github.com-fatih-color-v1.18.0";
    path = "github.com/fatih/color";
    version = "v1.18.0";
    hash = "sha256-pP5y…";
  };

  packages."github.com/fatih/color" = b.compile {
    name = "gopkg-github.com-fatih-color-v1.18.0";
    importPath = "github.com/fatih/color";
    src = modules."github.com/fatih/color@v1.18.0";
    subdir = "";
    module = "github.com/fatih/color";
    trimTo = "github.com/fatih/color@v1.18.0";
    lang = "go1.17";
    isMain = false;
    goFiles = [ "color.go" "doc.go" ];
    deps = [ packages."github.com/mattn/go-isatty" ];
  };

  packages."example.com/app" = b.compile {
    name = "golocal-example.com-app";
    importPath = "example.com/app";
    src = b.localDir { name = "gosrc-example.com-app"; files = [ "main.go" ]; };
    subdir = "";
    module = "example.com/app";
    trimTo = "example.com/app";
    lang = "go1.24";
    isMain = true;
    goFiles = [ "main.go" ];
    deps = [ packages."github.com/fatih/color" ];
  };

  bins.app = b.link {
    name = "gobin-app";
    binName = "app";
    main = packages."example.com/app";
    deps = [ /* transitive closure, standard library excluded */ ];
    modinfo = "…";
    godebug = "…";
  };

  tests."example.com/app" = b.test {
    importPath = "example.com/app";
    src = b.localDir {
      name = "gosrc-test-example.com-app";
      files = [ "main.go" "main_test.go" "cli_test.go" ];
      trees = [ "testdata" ];
    };
    module = "example.com/app";
    lang = "go1.24";
    goFiles = [ "main.go" ];
    testGoFiles = [ "main_test.go" ];   # package main
    xTestGoFiles = [ "cli_test.go" ];   # package main_test
    deps = [ packages."github.com/fatih/color" ];
    testDeps = [ packages."github.com/stretchr/testify/assert" ];
    recompile = [ ];
  };
}
```

Derivation names are computed by the tool, so name sanitising lives in one
place. `module` is the owning module's path, used to look up
`packageOverrides`; `trimTo` is what `-trimpath` rewrites the source directory
to. File lists in `localDir` are relative to the source root.

Fields a `compile` node may carry beyond those shown: `sFiles`, `cgoFiles`,
`cFiles`, `cxxFiles`, `hFiles`, `sysoFiles`, `embed` (pattern to file list),
`cgo` (`cflags`, `ldflags`, `pkgConfig`). A `test` node carries the same
optional fields, plus `testEmbed` and `xTestEmbed`.

In a `test` node, `deps` are the package's own direct imports, `testDeps` are
the direct imports its test files add, and `recompile` lists the local
packages that must be rebuilt against the test archive, each as
`{ importPath; src; goFiles; deps; … }`.

`deps` lists direct imports only. The standard library is not a node; every
builder takes it from `b.stdlib cgoEnabled`. `cgoEnabled` is the resolved
setting, so `CGO_ENABLED = null` is settled by the resolver, not in Nix.

The tool and the Nix library ship from one source tree and the tool is built
from the same revision as the library, so the contract between generated code
and builders cannot drift and carries no version number.

## Evaluation flow

`buildGoApplication` does the following when Nix evaluates it.

1. It checks `builtins ? exec` and throws an error naming the option if it is
   missing.
2. It calls

   ```nix
   builtins.exec [
     "${evalTool}/bin/gonixgo" "resolve"
     (builtins.toJSON {
       go = "${evalGo}/bin/go";
       src = "${src}";
       storeDir = builtins.storeDir;
       inherit modRoot subPackages tags goos goarch cgoEnabled doCheck;
     })
   ]
   ```

   Nix builds the tool and Go first if they are not in the store.
3. The tool runs `go list -e -deps -json` for `subPackages` in
   `src/modRoot`. Under `doCheck` a second `go list -e -deps -test -json`
   pass covers the local packages found by the first.
4. It classifies each package as standard library, third-party (owned by a
   fetched module) or local (main module, or a module replaced with a
   directory).
5. It hashes and pre-seeds every module that owns a package in the graph.
6. It prints the graph. `buildGoApplication` applies it to the builders and
   returns the application derivation.

### Environment for `go list`

Set by the tool: `GOENV=off`, `GOFLAGS=-mod=readonly`, `GOWORK=off`,
`GOTOOLCHAIN=local`, `GOOS`, `GOARCH`, `CGO_ENABLED`, and the build tags. The
caller's build configuration (`GOOS`, `GOARCH`, `CGO_ENABLED`, the
architecture level keys, `GOEXPERIMENT`, `GOFIPS140`, `GO111MODULE`, `GOROOT`)
is dropped, so the same source resolves to the same graph in every shell, as
the builders see it.

Carried over from the caller: where modules come from. The tool first runs
`go env` with the caller's environment and Go env file for `GOPROXY`,
`GOPRIVATE`, `GONOPROXY`, `GONOSUMDB`, `GOSUMDB`, `GOINSECURE`, `GOVCS`,
`GOMODCACHE`, `GOPATH` and `GOAUTH`, and sets those values explicitly.
`HOME`, `PATH`, `NETRC` and the proxy variables are inherited. Go downloads
missing modules and verifies them against `go.sum`.

### What evaluation requires

- `allow-unsafe-native-code-during-evaluation = true`, via `--option`,
  `nix.conf` or `NIX_CONFIG`. A flake's `nixConfig` does not work.
- Network access, or a module cache that already holds the project's modules.
- Import-from-derivation allowed (the default), since the tool and Go are
  built during evaluation.
- The evaluating machine must be able to run the evaluation-time tool and Go.

## Module hashing and pre-seeding

For each module that owns a package in the graph:

1. **Locate.** `go list` reports the extracted module directory in
   `GOMODCACHE`.
2. **Hash.** The tool serialises the directory as a NAR and takes its
   SHA-256. Results are cached under the user cache directory, keyed by
   module path, version and the module's `h1:` line from `go.sum`.
3. **Compute the store path.** Name `gomod-<sanitised path>-<version>`,
   fixed-output, recursive SHA-256, under `storeDir`. Characters outside
   `[A-Za-z0-9+._?=-]` become `-`.
4. **Pre-seed.** If that path does not exist, the tool runs
   `nix store add --name <name> --mode nar --hash-algo sha256 <dir>` and checks
   the printed path equals the computed one. A mismatch is an error: the module
   cache changed after it was hashed.
5. **Emit.** `b.fetchModule { path; version; hash; }`.

Hashing and pre-seeding run concurrently across modules.

If `nix` is not on `PATH` or the add fails, the tool warns on stderr and
continues; the fetch derivation then downloads at build time.

### `fetchModule`

A fixed-output derivation (`outputHashMode = "recursive"`) whose builder is
`gonixgo fetch`: `go mod download <path>@<version>` into a temporary module
cache, then copy the extracted tree to `$out`. `GOPROXY`, `NETRC` and the proxy
variables are `impureEnvVars`.

Because of pre-seeding the builder normally never runs. It is the fallback for
a derivation built on a machine that did not evaluate it.

Consequences: each module is downloaded once, by `go`; private modules work
with the caller's own credentials and no token enters the store.

For `replace a => b v1.2.3`, `path` and `version` are the replacement's; the
packages keep import paths under `a`.

## Derivations

| Kind | Name | One per |
|---|---|---|
| `stdlib` | `go-stdlib-<version>-<goos>-<goarch>[-cgo]` | Go version, target, cgo setting |
| `fetchModule` | `gomod-<path>-<version>` | module version |
| `compile`, third-party | `gopkg-<import path>-<version>` | package |
| `compile`, local | `golocal-<import path>` | package |
| `link` | `gobin-<name>` | main package |
| `test` | `gotest-<import path>` | local package with tests |
| application | `<pname>[-<version>]` | `buildGoApplication` call |

### `stdlib`

Copies `GOROOT`'s `src`, `pkg` and `lib` to a writable directory and runs
`GODEBUG=installgoroot=all go install -trimpath std` with the target's `GOOS`,
`GOARCH` and `CGO_ENABLED`. Outputs every archive and an `importcfg` listing
them. It uses the C toolchain when cgo is enabled. Every build in a `goEnv`
shares it.

### `compile`

Outputs `$out/pkg.a` and `$out/importcfg`, one `packagefile` line for this
package.

- The compile importcfg is the standard library's plus each direct
  dependency's line.
- `go tool compile` runs with `-p <import path>` (`main` for main packages),
  `-lang` from the owning module's `go` directive, `-trimpath`, and an empty
  build ID.
- Assembly: `go tool asm -gensymabis` first, compile with `-symabis` and
  `-asmhdr`, assemble each file, add the objects with `go tool pack`.
- Embeds: an embedcfg built from the patterns and files `go list` reported.
- `-trimpath` rewrites the source directory to the import path for
  main-module packages and to `<module>@<version>/<subdir>` otherwise, and the
  build directory to nothing.

Pure-Go packages are a bare `derivation` whose builder is the tool. They do not
use stdenv.

### `link`

Writes the link importcfg (standard library, the main package, its transitive
dependencies, and a `modinfo` line), then runs `go tool link` with the caller's
`ldflags`. The build ID is derived from a hash of the link manifest, which
names every input's store path: the linker turns it into the Mach-O `LC_UUID`
and the ELF build ID, so each binary gets its own, reproducibly. The C toolchain is available when cgo is enabled, and the linker
chooses internal or external linking as it does under `go build`.

The module info matches `go build -trimpath`: `path`, `mod`, one `dep` line per
module that provides a package to this binary with its `h1:` sum, `=>` lines
for replacements, and the `build` settings. `DefaultGODEBUG` comes from the
field `go list` reports for main packages. The main module's version is
`(devel)`.

### Application

Copies each `bins.*` output into `$out/bin`. With `doCheck` it also depends on
every `tests.*` derivation, so a failing test fails the build.

## Local sources

`b.localDir { name; files; trees ? [ ]; }` is a `builtins.path` copy of `src`
filtered to exactly the listed files, the directories leading to them, and
everything under each listed tree. Paths are relative to `src`.

A compile node lists the files `go list` reported for the package, including
embed targets in subdirectories. Editing a neighbouring package, or a file in
the same directory that the package does not use, changes nothing.

## cgo

A package with cgo, C, C++ or assembly-for-gcc files compiles through
`stdenv.mkDerivation` with `stdenv.cc`. The tool runs `go tool cgo`, compiles
the generated and hand-written C sources with the C compiler, and packs the
objects into the archive. `cgo` directives and `pkg-config` names from
`go list` are honoured.

`packageOverrides` supplies libraries:

```nix
packageOverrides."github.com/mattn/go-sqlite3" = {
  buildInputs = [ pkgs.sqlite ];
  nativeBuildInputs = [ pkgs.pkg-config ];
  env.CGO_CFLAGS = "-DSQLITE_ENABLE_FTS5";
};
```

Keys are import paths or module paths; an import path match wins. The
`buildInputs` of every overridden package in a binary's closure are also added
to its link derivation.

With `CGO_ENABLED = null` the resolver uses `go env CGO_ENABLED` for the
target and reports the result as the graph's `cgoEnabled`. When cgo is off,
`go list` excludes cgo files and no derivation uses a C toolchain.

## Tests

One test derivation per local package that has test files and is reachable
from `subPackages`, including packages of sibling modules behind a directory
`replace`. Third-party packages are not tested. Local helper packages that only
tests import are compiled but their own tests are not run.

For package `P` the `test` subcommand:

1. Compiles the internal test archive: `P`'s files plus its `_test.go` files
   in package `P`.
2. Recompiles, inside the same derivation, any local package that the external
   test imports and that itself imports `P`, against the archive from step 1.
   `go list -test` reports these as `Q [P.test]`.
3. Compiles the external test package `P_test`.
4. Generates the test main, registering `Test*`, `Benchmark*`, `Fuzz*` and
   `Example*` functions.
5. Links and runs the test binary with `checkFlags`, in a directory laid out
   as `P`'s path relative to `src`.

The output is the test log.

Test-only third-party packages and local helper packages imported only by
tests are ordinary `packages` nodes that only test nodes reference.

**File visibility.** A test sees its package's files and everything under its
`testdata/`. This differs from go2nix, which gives tests a filtered copy of
the whole source and so reruns them on any change. Tests that read files
elsewhere declare them:

```nix
packageOverrides."example.com/app/internal/web".testExtraSrc = [ "fixtures" ];
```

Paths are relative to `src` and are included as trees.

Tests are skipped when the build platform cannot execute the target.

## Cross-compilation

`GOOS` and `GOARCH` come from `go.GOOS` and `go.GOARCH`, which nixpkgs derives
from the target platform. They go to the resolver, so file lists and build
constraints match the target, and to every derivation.

Derivations run on the build platform with `pkgs.buildPackages.go`. cgo
packages use the cross C compiler from `pkgs.stdenv.cc`.

The resolver runs on the evaluating machine. When that is not the build
platform, for example building Linux packages from a Mac through a remote
builder, pass `evalPkgs` for the evaluating machine's system; its tool and its
Go then run the resolver (pass `evalGo` to pick a different Go of the same
version).

## Monorepo layout

- **`modRoot`** names the directory of `go.mod` inside `src`. `subPackages`
  are relative to it.
- **`replace` with a directory inside `src`.** The target module's packages
  are local. They compile with that module's own `go` directive and appear in
  module info under its path and the version from the `require` line.
- **`replace` with another module version.** Changes what is fetched; import
  paths are unchanged.
- **`replace` with a directory outside `src`.** An error naming the directive.

## Error handling

The tool writes diagnostics to stderr, which Nix passes through, and exits
non-zero. Nix then reports that the program failed.

| Situation | Behaviour |
|---|---|
| `builtins.exec` unavailable | `buildGoApplication` throws, naming `allow-unsafe-native-code-during-evaluation` and the three ways to set it. `mkGoEnv` itself does not need `exec`. |
| `go.sum` missing entries | Reports Go's message and suggests `go mod tidy`. |
| Packages that fail to load | `go list -e` errors are collected and all reported. |
| Module download fails | Reports Go's message with the module and version. |
| `replace` target outside `src` | Names the directive and the resolved path. |
| Pre-seeded path differs from the computed one | Names the module and says the module cache changed. |
| `nix` missing or `nix store add` fails | Warning only; the fetch derivation covers it. |
| The evaluation-time Go (`evalGo`, else `evalPkgs.go`, else `go`) differs in version from `go` | `mkGoEnv` assertion naming `evalGo` and both versions. |

## Not in this version

`go.work` (forced off), `vendor/` directories (ignored; modules come from the
module cache), PGO, `gcflags`, the race detector, coverage, content-addressed
derivations, and `GOEXPERIMENT`/`GOFIPS140` configuration.

## Testing gonixgo

### Unit tests (Go)

- NAR serialisation and hashing against values from `nix hash path`.
- Store-path computation against `nix store add`.
- Package classification and graph construction from recorded `go list`
  output.
- The Nix emitter, with golden files.
- Module info generation against `go version -m` output.
- Test-main generation.

### Integration fixtures

Small Go projects under `tests/fixtures/`, built by a script with real
`nix build` and the `exec` option. They run outside the Nix sandbox because
they need `exec` and the network.

| Fixture | Covers |
|---|---|
| `hello-deps` | Pure Go, several local packages, third-party dependencies. |
| `asm-embed` | Assembly and `go:embed`. |
| `cgo` | A cgo package with a library from `packageOverrides`. |
| `tests` | Internal and external tests, a test-only dependency, `testdata`. |
| `monorepo` | `modRoot`, a sibling module behind a directory `replace`. |
| `cross` | A Linux arm64 build. |

Each fixture asserts:

- The binary runs and prints the expected output (where the build platform
  can execute it).
- `go version -m` matches a `go build -trimpath -buildvcs=false` of the same
  source.
- Editing one package changes only the expected derivation paths.

## Repository layout

```
flake.nix            lib.mkGoEnv, packages.gonixgo, dev shell
default.nix          { pkgs }: non-flake entry point
go.mod               module with no requirements
cmd/gonixgo/         main: subcommand dispatch, manifest loading
internal/golist/     running and decoding go list
internal/graph/      classification, closures, the graph model
internal/modinfo/    the module info go build embeds
internal/nar/        NAR serialisation and hashing
internal/storepath/  fixed-output store paths, name sanitising
internal/modcache/   go.sum, hash cache, pre-seeding
internal/emit/       graph to Nix
internal/resolve/    the evaluation-time pipeline
internal/gotool/     locating and running compile, asm, link
internal/compile/    compile, assembly, embeds, cgo
internal/link/       importcfg, link
internal/fetch/      the fallback module download
internal/gotest/     test variants, test main, runner
nix/                 mk-go-env.nix, tool.nix, stdlib.nix, builders.nix, build-go-application.nix
tests/fixtures/      integration fixtures
tests/run.sh         integration driver
```

## Build order

1. **Pure Go, end to end.** Resolve, hashing, pre-seeding, emitter, `stdlib`,
   `fetchModule`, `compile` (Go, assembly, embeds), `link` with module info,
   application derivation. Fixtures `hello-deps` and `asm-embed`.
2. **Cross-compilation and monorepo.** `evalPkgs`, target threading,
   `modRoot`, both kinds of `replace`. Fixtures `cross` and `monorepo`.
3. **cgo.** The stdenv compile path, `packageOverrides`, external linking.
   Fixture `cgo`.
4. **Tests.** The `-test` pass, test nodes, test main, `testExtraSrc`,
   `doCheck`. Fixture `tests`.

Each stage ends with its fixtures passing, and each gets its own
implementation plan, written when the previous stage is done. The first plan
covers stage 1 only.
