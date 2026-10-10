# gonixgo

Build Go programs with Nix one package per derivation, with nothing to check
in when `go.mod` changes.

gonixgo follows [go2nix](https://github.com/numtide/go2nix): the standard
library, every module and every package get their own derivation, so an edit
rebuilds the package, the packages that import it, and the link. It differs
in two ways:

- **No Nix plugin.** A Nix-built binary runs during evaluation through
  `builtins.exec` and prints the Nix code for the build.
- **No lockfile.** Module hashes are computed during evaluation. Go
  verifies each module against `go.sum` when it downloads it into the
  module cache; gonixgo hashes the extracted directory it finds there. A Go
  project commits no Nix code that depends on `go.mod` or `go.sum`.

## Use

```nix
{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
  inputs.gonixgo.url = "github:draganm/gonixgo";

  outputs = { nixpkgs, gonixgo, ... }:
    let
      system = "aarch64-darwin";
      pkgs = nixpkgs.legacyPackages.${system};
      goEnv = gonixgo.lib.mkGoEnv { inherit pkgs; };
    in {
      packages.${system}.default = goEnv.buildGoApplication {
        pname = "app";
        src = ./.;
      };
    };
}
```

```bash
nix build --option allow-unsafe-native-code-during-evaluation true
```

With `allow-unsafe-native-code-during-evaluation` on, any Nix expression
you evaluate can run programs as you. Prefer passing `--option` per command
for projects you trust over enabling it in `nix.conf`. `nix flake check` and
`nix flake show` on a flake that exposes a gonixgo package under `packages`
need the option too.

The `pkgs` you pass builds the gonixgo tool and the standard library, and
performs the Go build. Without flakes, `import gonixgo { inherit pkgs; }`
returns the same set as `mkGoEnv`.

### `mkGoEnv`

| Argument | Default | Meaning |
|---|---|---|
| `pkgs` | required | The nixpkgs that builds the tool, the standard library and the packages. |
| `go` | `pkgs.pkgsBuildBuild.go` | The Go that builds. It runs on the build platform; the target comes from `pkgs`. |
| `evalPkgs` | `null` | Package set for the evaluating machine, when it differs from the build platform; `null` means `pkgs.pkgsBuildBuild`. |
| `evalGo` | `null` | The Go that resolves the graph during evaluation; `null` means `evalPkgs.go` when `evalPkgs` is given, otherwise `go`. It must be the same version as `go`. |

To choose a Go version: `gonixgo.lib.mkGoEnv { inherit pkgs; go = pkgs.go_1_25; }`.
In a cross build, take it from `pkgs.pkgsBuildBuild`, as in
`go = pkgs.pkgsBuildBuild.go_1_25`.

### `buildGoApplication`

| Argument | Default | Meaning |
|---|---|---|
| `pname` | required | Derivation name. |
| `version` | `null` | Appended to the derivation name. |
| `src` | required | The source tree. |
| `modRoot` | `"."` | Directory of `go.mod` inside `src`. |
| `subPackages` | `[ "." ]` | Main packages to build, relative to `modRoot`. |
| `tags` | `[ ]` | Build tags. |
| `ldflags` | `[ ]` | Linker flags, split as `go build -ldflags` splits them. |
| `CGO_ENABLED` | `null` | `null` uses Go's default for the target, which with nixpkgs' Go is on, except in a cross build, where it is off; see [Cross-compilation](#cross-compilation). |
| `doCheck` | `true` | Build and run the tests of the program's packages; see [Tests](#tests). |
| `checkFlags` | `[ ]` | Flags for every test, spelt as for `go test`: `-run`, `-skip`, `-short`, `-v`, `-count`, `-timeout` and the other flags `go test` hands to the test binary. |
| `nativeCheckInputs` | `[ ]` | Tools on every test's `PATH`. |
| `checkEnv` | `{ }` | Environment variables for every test. |
| `packageOverrides` | `{ }` | Libraries and tools for cgo packages, and what tests need; see [cgo](#cgo) and [Tests](#tests). |

Binaries land in `$out/bin`, named as `go build` names them. The result's
`passthru` has `packages`, `modules`, `bins`, `tests`, `testPackages` and
`testBins`, each a set of derivations, so one package can be built alone:

```bash
nix build --option allow-unsafe-native-code-during-evaluation true \
  '.#default.packages."example.com/app/internal/web"'
```

### cgo

A package that imports `"C"` is built like any other, in its own
derivation, with the C compiler of the `pkgs` you pass. Its C, C++,
Objective-C and assembly files are compiled as `go build` compiles them,
and its `#cgo` directives are honoured, `pkg-config` and `${SRCDIR}`
included. Pure-Go packages, and binaries with no cgo package in them, are
built without a C compiler as before.

A package that needs only what the platform provides, such as libc or the
macOS frameworks, needs nothing from you. One that needs a library from
nixpkgs gets a `packageOverrides` entry:

```nix
goEnv.buildGoApplication {
  pname = "app";
  src = ./.;
  packageOverrides = {
    # One package: its #cgo pkg-config directive names libzstd.
    "example.com/app/internal/zstd" = {
      buildInputs = [ pkgs.zstd ];
      nativeBuildInputs = [ pkgs.pkg-config ];
    };
    # Every cgo package of a module.
    "github.com/mattn/go-sqlite3".env.CGO_CFLAGS = "-O2 -g -DSQLITE_ENABLE_FTS5";
  };
}
```

| Attribute | Default | Meaning |
|---|---|---|
| `buildInputs` | `[ ]` | Libraries. The package's compile gets them, and so does the link of every binary that contains the package. |
| `nativeBuildInputs` | `[ ]` | Tools the compile runs, such as `pkg-config`. |
| `env` | `{ }` | Environment of the compile. `CGO_CPPFLAGS`, `CGO_CFLAGS`, `CGO_CXXFLAGS` and `CGO_LDFLAGS` mean what they mean to `go build`: setting `CGO_CFLAGS`, `CGO_CXXFLAGS` or `CGO_LDFLAGS` replaces its `-O2 -g` default. |

A key is an import path or a module path. A package takes the entry for its
import path if there is one, otherwise its module's. Any other attribute in
an entry is an error, and a key that no cgo package of the build takes gets
a warning, because such an entry changes nothing.

Two things differ from `go build`:

- A local package is built from a copy of the source that holds only the
  files `go list` reports for it, plus every file and directory its `#cgo`
  directives name through `${SRCDIR}`. A file that only an `#include`
  reaches, such as a header in a subdirectory, is not in that copy until a
  directive names its directory: `#cgo CFLAGS: -I${SRCDIR}/sub`.
- `#cgo` flags are not checked against the list of flags `go build`
  considers safe. A dependency's flags can therefore make the C compiler
  run code during the build, which the Nix sandbox contains where it is
  on; it is off by default on macOS.

### Tests

With `doCheck` on, the build runs the tests of every package of your module
that a binary contains and that has `_test.go` files; a failing test fails
the build. Each piece of a package's test binary is its own derivation, so
editing a test reruns only that package's tests, and editing a source file
reruns the tests of the packages built on it.

A test runs in a writable copy of its package's files, test files and
`testdata/`, laid out as in your source, with the package's directory as
its working directory. `HOME` is an empty writable directory, and Go's
`bin` comes first on `PATH`. The package under test is compiled without
`-trimpath`, so a path from `runtime.Caller` names a real file; that file
is read-only.

A test that reads other files of the repository, or needs tools,
environment or flags of its own, gets a `packageOverrides` entry:

```nix
goEnv.buildGoApplication {
  pname = "app";
  src = ./.;
  checkFlags = [ "-short" ];
  nativeCheckInputs = [ pkgs.git ];
  checkEnv.TZ = "UTC";
  packageOverrides."example.com/app/internal/web" = {
    testExtraSrc = [ "fixtures" ];
    checkFlags = [ "-skip" "TestNeedsNetwork" ];
    checkEnv.WEB_FIXTURES = "../../fixtures";
  };
}
```

| Attribute | Default | Meaning |
|---|---|---|
| `testExtraSrc` | `[ ]` | Files and directories, relative to `src`, that the tests read outside the package's `testdata/`. |
| `nativeCheckInputs` | `[ ]` | Tools, after the program's. |
| `checkFlags` | `[ ]` | Flags, after the program's; where both give a flag, the package's value wins. |
| `checkEnv` | `{ }` | Environment, merged over the program's. |

`passthru.tests."<import path>"` is a package's test run, whose output is
the log, and `passthru.testBins."<import path>"` its test binary, which
builds even when the test fails:

```bash
nix build --option allow-unsafe-native-code-during-evaluation true \
  '.#default.testBins."example.com/app/internal/web"'
cd internal/web && ../../result/bin/web.test -test.run TestFoo -test.v
```

Not tested: third-party packages, packages that only tests import, and
packages with only test files. Tests do not run when cross-compiling.
`go vet`, coverage, the race detector, fuzzing (seed corpora run as
ordinary tests) and `-json` output are not supported; a build flag in
`checkFlags`, such as `-race`, is rejected by the test binary.

### Cross-compilation

The target is the platform of the `pkgs` you pass to `mkGoEnv`, so a
`pkgsCross` set builds for its platform:

```nix
goEnv = gonixgo.lib.mkGoEnv { pkgs = pkgs.pkgsCross.aarch64-multiplatform; };
```

The Go that builds runs on your machine and is the one the binary cache
holds. `GOOS`, `GOARCH` and, for 32-bit ARM, `GOARM` come from the target. A
Go from the cross set itself, such as `pkgs.pkgsCross.aarch64-multiplatform.go_1_25`,
runs on the target, and `mkGoEnv` refuses it.

cgo is off in a cross build unless you set `CGO_ENABLED = 1`, so a pure-Go
program needs no C toolchain for the target. With it, cgo packages compile
with the cross C compiler, and `packageOverrides` entries take the target's
libraries from the cross set:

```nix
let cross = pkgs.pkgsCross.aarch64-multiplatform; in
(gonixgo.lib.mkGoEnv { pkgs = cross; }).buildGoApplication {
  pname = "app";
  src = ./.;
  CGO_ENABLED = 1;
  packageOverrides."example.com/app/internal/zstd" = {
    buildInputs = [ cross.zstd ];
    nativeBuildInputs = [ cross.pkg-config ];
  };
}
```

Nix builds the cross C toolchain when the binary cache lacks it, which it
does for a Mac building for Linux. When cgo is off, a package that cannot
load without it fails evaluation, and one that loads without its cgo files
is named in a warning; both say to set `CGO_ENABLED = 1`. Heed the warning:
such a package may not compile, and one with a pure-Go fallback for builds
without cgo, such as `github.com/mattn/go-sqlite3`, builds but fails only
when it runs.

Tests run only when your machine can run the target's binaries.

`evalPkgs` is for something else: building on another machine, such as a
Linux remote builder from a Mac. There `pkgs` is the builder's nixpkgs and
`evalPkgs` your machine's, whose tool and Go resolve the graph:

```nix
gonixgo.lib.mkGoEnv {
  pkgs = nixpkgs.legacyPackages.x86_64-linux;
  evalPkgs = nixpkgs.legacyPackages.aarch64-darwin;
}
```

### Monorepos and `replace`

`modRoot` names the directory of `go.mod` inside `src`, and `subPackages` are
relative to it. A sibling module behind a directory `replace` must be inside
`src`:

```
repo/
  services/api/go.mod   replace example.com/repo/lib => ../../lib
  lib/go.mod
```

```nix
goEnv.buildGoApplication {
  pname = "api";
  src = ./.;              # repo/
  modRoot = "services/api";
}
```

The sibling module's packages build from `src` like the program's own, and
compile with that module's `go` directive. Their tests do not run; only the
main module's packages are tested. A `replace` with another version, or with
a fork under another path, changes what is fetched. Either way the packages
keep their import paths: a `packageOverrides` key is the import path or the
module path the code imports, not the fork's. The binary's module info lists
the replacements as `go build` does.

`go.work` is ignored, so each module's `go.mod` needs its own `replace`
lines. A directory `replace` that leaves `src` fails during evaluation: make
`src` the directory that holds both modules, and `modRoot` the one with the
program.

## What evaluation needs

- `allow-unsafe-native-code-during-evaluation = true`, from `--option`,
  `NIX_CONFIG` or `nix.conf`. A flake's `nixConfig` cannot set it.
- The project's modules: `go list` runs during evaluation with your
  `GOMODCACHE`, `GOPROXY`, `GOPRIVATE` and `NETRC`, and downloads what is
  missing.
- Import-from-derivation (on by default): the tool and Go are built during
  evaluation the first time.
- A recent Nix: gonixgo is developed against Nix 2.26. Pre-seeding uses
  `nix store add --mode nar`; with an older `nix` that add fails and modules
  are fetched at build time instead.

Module sources are added to the Nix store during evaluation, so nothing is
downloaded twice and private modules need no credentials in the store. A
module is fetched by a derivation only when the build happens on a machine
that did not evaluate it; that fetch uses `GOPROXY` and cannot reach
repositories that need `git`. It reads `GOPROXY` and `NETRC` from the
environment of whatever builds it: the Nix daemon's, on multi-user installs.

## Not yet supported

`go.work`, `vendor/` directories, and packages with SWIG, Fortran or `.syso`
files; `.syso` support may come later. Such packages are rejected during
evaluation with a message naming them.

The integration tests have been run on aarch64-darwin only; Linux is
untested.

## Example

[`examples/rebuild-times`](examples/rebuild-times) builds one small service
with gonixgo and with `buildGoModule`, and times a rebuild of each after a
one-line change.

## Development

```bash
nix develop --command go test ./...   # unit tests
tests/run.sh                          # integration tests: real nix builds
```

The design is in `docs/superpowers/specs/2026-10-03-gonixgo-design.md`.

## License

MIT, see [LICENSE](LICENSE).
