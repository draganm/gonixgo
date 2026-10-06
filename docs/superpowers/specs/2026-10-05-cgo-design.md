# gonixgo cgo design

Date: 2026-10-05

This is stage 3 of the build order in the
[gonixgo design](2026-10-03-gonixgo-design.md), taken before stage 2. It
fills in that document's cgo section and replaces it where the two differ;
the differences are listed under
[Departures from the 2026-10-03 design](#departures-from-the-2026-10-03-design).

## Goal

Build programs whose package graph contains cgo packages.

cgo is already on by default, and a pure-Go program builds with it: the
standard library derivation is built with a C compiler. What fails today is
any graph that holds a package with cgo or C files. `resolve` rejects it, so
a project that pulls in, say, the Prometheus client on macOS has to set
`CGO_ENABLED = 0`.

### Success criteria

- A program with local and third-party cgo packages builds with the default
  `CGO_ENABLED`. The only Nix code it needs is a `packageOverrides` entry for
  each library that is not part of the platform.
- Editing a C file or a header of a local cgo package rebuilds that package,
  the packages that import it, and the link. Nothing else.
- A program with no cgo packages builds through the same kinds of
  derivations as before: no stdenv and no C compiler in any compile or link.
- `go version -m` on the result matches a `go build -trimpath` of the same
  source.
- No store path of a source tree appears in the result's references.

## Scope

In this version:

- Go files that `import "C"`.
- C, C++ and Objective-C sources, and `.s`, `.S` and `.sx` files of a cgo
  package, which go to the C compiler.
- `#cgo` `CPPFLAGS`, `CFLAGS`, `CXXFLAGS`, `LDFLAGS` and `pkg-config`
  directives, including `${SRCDIR}`.
- `//export`.
- `packageOverrides`, to supply libraries and tools.
- Linking through the C linker.

Not in this version; see [Not in this version](#not-in-this-version):
SWIG, Fortran, `.syso` files, cross-compiling cgo, tests.

Verified on aarch64-darwin only, like the rest of gonixgo.

## Facts verified

Probed with Go 1.26.7, Nix 2.26.1 and nixpkgs `nixos-26.05` on
aarch64-darwin. The design relies on them.

| Fact | Evidence |
|---|---|
| `go list -json` lists `"C"` in a cgo package's `Imports`, and `-deps` prints no package for it. | A cgo package's `Imports` was `["C"]`; no `C` entry followed. |
| `go list` expands `${SRCDIR}` in `CgoCFLAGS` and the other flag lists to the package's absolute directory, as plain text. | `-I${SRCDIR}/include` came back as `-I<package dir>/include`. |
| A header in a subdirectory is in none of `go list`'s file lists. | `include/add.h` was not in `HFiles`. |
| `go list` reports only the flags from `#cgo` directives. The `-O2 -g` defaults are added when building. | `CgoCFLAGS` held the directive's flags only; `go build -x` showed `-O2 -g` before them. |
| nixpkgs' Go reports `CGO_ENABLED=1` whether or not a C compiler is on `PATH`. | `go env CGO_ENABLED` printed `1` with `PATH=/nonexistent` and no `CC`. The graph does not depend on the evaluating shell. |
| `go build` passes `-extld=<CC>` to the link of a program with a cgo package, after the build ID. | `go build -x`. |
| A linked binary's debug sections are compressed, and with `-trimpath` no source path is readable in it. | Only `__zdebug_*` sections; `strings` found two store paths, a library and nixpkgs' tzdata. |
| `stdenv.mkDerivation` with `__structuredAttrs = true` and `buildCommand` exports `NIX_ATTRS_JSON_FILE`, and that file holds a `manifest` attribute and `outputs.out`. | A probe derivation printed both. |
| In that derivation `CC`, `CXX` and `PKG_CONFIG` are set, and an `env.CGO_CFLAGS` attribute reaches the build command. | `CC=clang CXX=clang++ PKG_CONFIG=pkg-config CGO_CFLAGS=-DFROM_OVERRIDE`. |
| `buildInputs` alone make `-l<name>` link, and frameworks link from the SDK. | `$CC t.c -lzstd -llz4 -framework CoreFoundation` linked with `buildInputs = [ zstd lz4 ]`. |
| `pkg-config` in `nativeBuildInputs` finds the `.pc` files of `buildInputs`. | `pkg-config --cflags --libs -- libzstd liblz4` printed store paths. |
| `$CC --print-prog-name dsymutil` and `… strip` resolve. The Go linker looks them up this way on macOS. | Both printed store paths. |

### The build steps of a cgo package

Captured from `go build -x -trimpath` and checked against cmd/go's source
(`cmd/go/internal/work/action.go` and `exec.go`). `<obj>` is the package's
scratch directory, `<src>` its source directory.

1. **cgo**, run in `<src>` with `CGO_LDFLAGS=` in the environment:

   ```
   cgo -objdir <obj>/ -importpath <import path> "-ldflags=<quoted ldflags>" -- <cppflags> <cflags> ./a.go …
   ```

   It writes `_cgo_gotypes.go`, `_cgo_export.c`, `_cgo_export.h`,
   `_cgo_main.c`, and for each input `a.go` the pair `a.cgo1.go` and
   `a.cgo2.c`. The `-ldflags` value is every link flag, each quoted as a Go
   string, joined by spaces. It is left out when there are no link flags.

2. **C compiles**, one per file:

   ```
   $CC -I <inc> -fPIC [-arch arm64] -pthread -fno-caret-diagnostics -Qunused-arguments \
     -fmessage-length=0 -ffile-prefix-map=<obj>=/tmp/go-build -gno-record-gcc-switches [-fno-common] \
     <cppflags> <cflags> -ffile-prefix-map=<module dir>=/_/<module>[@<version>] \
     -frandom-seed=<id> -o <obj>/_xNNN.o -c <file>
   ```

   Generated files compile in `<obj>`, the package's own files in `<src>`.
   `<inc>` is `<src>` for both; `go build -x` prints it as `.` for a
   command that runs there. Objects are numbered in
   this order: `_cgo_export.c`, each `a.cgo2.c`, the assembly files, the C
   files, the Objective-C files, then the C++ files, which use `$CXX` and
   `<cxxflags>`. `-fno-common` is passed on darwin, and the `-arch` flags
   depend on the target.

3. **Dynamic imports.** `_cgo_main.c` compiles to `_cgo_main.o`, then a
   trial link, then cgo again:

   ```
   $CC -I <src> <the same compiler flags> -o <obj>/_cgo_.o <obj>/_cgo_main.o <obj>/_x001.o … <ldflags>
   cgo -dynpackage <package name> -dynimport <obj>/_cgo_.o -dynout <obj>/_cgo_import.go
   ```

   The trial link uses `$CXX` when the package has C++ files, and adds
   `-pie` on linux/arm and android.

4. **Go compile**, without `-complete`, of the package's plain Go files
   followed by `<obj>/_cgo_gotypes.go`, each `<obj>/a.cgo1.go` and
   `<obj>/_cgo_import.go`. The `-trimpath` value is the one a pure package
   gets: `<src>=><trimTo>;<obj>=>`.

5. **Pack.** The `_xNNN.o` objects are appended to the archive.
   `_cgo_main.o` and `_cgo_.o` are not.

The flag lists are:

| List | Contents, in order |
|---|---|
| `<cppflags>` | `$CGO_CPPFLAGS`, the package's `CPPFLAGS`, `pkg-config --cflags`, then `-I <obj>/` |
| `<cflags>` | `$CGO_CFLAGS` (default `-O2 -g`), the package's `CFLAGS` |
| `<cxxflags>` | `$CGO_CXXFLAGS` (default `-O2 -g`), the package's `CXXFLAGS` |
| `<ldflags>` | `$CGO_LDFLAGS` (default `-O2 -g`), the package's `LDFLAGS`, `pkg-config --libs`, and `-lobjc` when there are Objective-C files |

## User-facing API

`buildGoApplication` already accepts `packageOverrides` and ignores it. It
now takes effect.

```nix
goEnv.buildGoApplication {
  pname = "app";
  src = ./.;
  packageOverrides = {
    # One package.
    "example.com/app/internal/zstd" = {
      buildInputs = [ pkgs.zstd ];
      nativeBuildInputs = [ pkgs.pkg-config ];
    };
    # Every cgo package of a module.
    "github.com/mattn/go-sqlite3" = {
      buildInputs = [ pkgs.sqlite ];
      env.CGO_CFLAGS = "-O2 -g -DSQLITE_ENABLE_FTS5";
    };
  };
}
```

| Attribute | Default | Meaning |
|---|---|---|
| `buildInputs` | `[ ]` | Libraries. They are available to the package's compile and to the link of every binary that contains the package. |
| `nativeBuildInputs` | `[ ]` | Tools the compile runs, such as `pkg-config`. |
| `env` | `{ }` | Environment of the compile. `CGO_CPPFLAGS`, `CGO_CFLAGS`, `CGO_CXXFLAGS` and `CGO_LDFLAGS` are read as `go build` reads them: `CGO_CFLAGS`, `CGO_CXXFLAGS` and `CGO_LDFLAGS` replace the `-O2 -g` default. |

A key is an import path or a module path. A package takes the entry for its
import path if there is one, otherwise the entry for its module. An entry
affects cgo packages only. Any other attribute in an entry is an error that
names the key and the attribute. A key that no cgo package of the build
takes is reported with a warning: it changes nothing, so it is most likely
misspelt, but it may match on another platform.

A package whose `#cgo` directives need only what the platform provides, such
as libc or the macOS frameworks, needs no entry.

## The generated graph

A cgo package's `compile` node carries one more attribute, `cgo`. A pure
package's node is unchanged.

```nix
packages."example.com/app/internal/cadd" = b.compile {
  name = "golocal-example.com-app-internal-cadd";
  importPath = "example.com/app/internal/cadd";
  src = b.localDir {
    name = "gosrc-example.com-app-internal-cadd";
    files = [ "internal/cadd/add.c" "internal/cadd/cadd.go" "internal/cadd/plain.go" ];
    trees = [ "internal/cadd/include" ];
  };
  subdir = "internal/cadd";
  module = "example.com/app";
  trimTo = "example.com/app/internal/cadd";
  lang = "go1.24";
  isMain = false;
  goFiles = [ "plain.go" ];
  sFiles = [ ];
  embed = { };
  deps = [ ];
  cgo = {
    pkgName = "cadd";
    cgoFiles = [ "cadd.go" ];
    cFiles = [ "add.c" ];
    cxxFiles = [ ];
    mFiles = [ ];
    cppflags = [ ];
    cflags = [ "-DBONUS=0" "-I\${SRCDIR}/include" ];
    cxxflags = [ ];
    ldflags = [ "-lm" ];
    pkgConfig = [ ];
  };
};
```

`goFiles` are the Go files that do not import `"C"`. `pkgName` is the Go
package name, which `cgo -dynpackage` needs. The flag lists are the
package's `#cgo` directives with `${SRCDIR}` kept as written.

`b.localDir` takes `trees`: directories copied whole, relative to the source
root like `files`. It is printed only when it is not empty.

A `link` node whose binary contains a cgo package carries `cgo = true;`, and
`cxx = true;` when one of its packages has C++ files. Both are left out when
false.

## Resolver

`golist.Package` decodes `CgoCPPFLAGS`, `CgoCFLAGS`, `CgoCXXFLAGS`,
`CgoLDFLAGS` and `CgoPkgConfig`.

**Classification.** A package with `CgoFiles` is a cgo package. A package
with SWIG, Fortran or `.syso` files is a load problem, reported with the
others, naming the package and the kind of file. With cgo off, `go list`
already leaves out cgo, C, C++ and Objective-C files.

**Imports.** `"C"` in `Imports` is skipped. `runtime/cgo` and `syscall`,
which cgo's generated code imports, come from the standard library's
importcfg, which every compile already receives whole.

**`${SRCDIR}`.** In each flag the package's directory is replaced by the
literal `${SRCDIR}`. This undoes what `go list` did: the directory it
expanded to is the module cache's or the evaluation-time source's, and
neither exists in the build. The compile step expands `${SRCDIR}` to the
package's directory in the store.

**Local sources.** A local cgo package's source copy holds:

- the files `go list` reports: Go, cgo, C, C++, Objective-C, header,
  assembly and embedded files;
- every path its `#cgo` directives name through `${SRCDIR}`. The path is the
  text from `${SRCDIR}` to the next `,`, `=`, `:`, quote or the end of the
  flag, cleaned. If it exists at evaluation time, a directory is added to
  `trees` and a file to `files`. If it does not exist it is ignored. If it
  is outside `src`, that is a load problem naming the package and the path.

So a header directory named by `-I${SRCDIR}/include` is part of the
package's source, and editing a header rebuilds the package. A file that
only an `#include` reaches is not found this way; see
[Not in this version](#not-in-this-version).

A third-party package needs none of this: its source is the whole module.

**Binaries.** `Binary` gains `Cgo`, true when the main package or any
package in its closure is a cgo package, and `CXX`, true when any of them
has C++ files.

## Builders

`nix/builders.nix` takes `stdenv`, which `mkGoEnv` passes as `pkgs.stdenv`,
and the application's `packageOverrides`, `{ }` when a caller of
`goEnv.builders` passes none.

### `compile`

A node without `cgo` builds exactly as before: a bare `derivation` whose
builder is the tool.

A node with `cgo` builds with `stdenv.mkDerivation`:

- `__structuredAttrs = true`, with the same `manifest` attribute plus `cgo`;
- `strictDeps = true`;
- `buildInputs`, `nativeBuildInputs` and `env` from the package's override;
- `buildCommand` runs `gonixgo compile`.

The tool reads the manifest and the output path from `NIX_ATTRS_JSON_FILE`
in both cases. The outputs are the same: `$out/pkg.a` and `$out/importcfg`.

The result also exposes the override's `buildInputs` as `linkInputs`, for
the link, and the two keys the package would take as `overrideKeys`, for the
warning about entries that match nothing.

### `link`

A node without `cgo` builds exactly as before.

A node with `cgo` builds with `stdenv.mkDerivation` in the same way. Its
`buildInputs` are the `linkInputs` of the main package and of every
dependency, without duplicates. Its manifest carries `cgo` and `cxx`.

Any change to a library changes the output path of the package that uses
it, and so the link manifest, which names that path. The build ID derived
from the manifest therefore still changes exactly when an input does.

### `localDir`

The filter keeps a path that is a listed file, a directory leading to a
listed file or tree, or anything at or under a listed tree.

## `gonixgo compile` for a cgo package

`compile.Manifest` gains an optional `cgo` object with the node's fields.
When it is present, `compile` performs the
[steps above](#the-build-steps-of-a-cgo-package), with these points settled:

- **`${SRCDIR}`** in the four flag lists is replaced by the manifest's
  `srcDir`.
- **pkg-config.** `pkgConfig` entries that start with `--` are flags; the
  rest are package names. `$PKG_CONFIG`, or `pkg-config`, runs twice, with
  `--cflags` and with `--libs`, and each output is split as cmd/go splits
  it.
- **Compilers.** `$CC` and `$CXX`, which stdenv sets. If one is unset,
  `go env` supplies it.
- **Flag probes.** `-fno-caret-diagnostics`, `-Qunused-arguments`,
  `-Wl,--no-gc-sections`, `-ffile-prefix-map`, `-gno-record-gcc-switches`
  and `-frandom-seed` are each passed only if the compiler accepts them,
  probed as cmd/go probes: an empty file on standard input, and the
  compiler's output searched for the words that mean an unknown option. A
  compiler without `-ffile-prefix-map` gets `-fdebug-prefix-map` if it
  accepts that.
- **Source paths.** The second `-ffile-prefix-map` maps the source root to
  `/_/<module>[@<version>]`, as cmd/go maps the module directory. The pair
  comes from the package directory and `trimTo` by dropping the path
  elements they share at their ends: `<store>/internal/cadd` and
  `example.com/app/internal/cadd` give `<store>` and `example.com/app`.
- **`-frandom-seed`** is a hash of the import path and the file name.
- **Concurrency.** The C compiles of one package run concurrently, at most
  `NIX_BUILD_CORES` at a time. Object numbers are assigned before they
  start, so the archive does not depend on timing.
- **Trial link.** If it fails, that is not an error. As in cmd/go, the
  package gets no `_cgo_import.go` and its archive gets an empty member
  named `dynimportfail`, which tells the Go linker to link externally.
- **Assembly.** In a cgo package the assembly files go to the C compiler,
  and the Go assembler does not run. A `.s` file in Go assembler syntax is
  an error there, as it is under `go build`.

### Environment

`compile` and `asm` keep the minimal environment they have today.

`cgo`, the C compiler and `pkg-config` run with the derivation's
environment, because the compiler wrapper and `pkg-config` read their
configuration from it. On top of it the tool sets what it sets for every Go
tool (`HOME`, `XDG_CONFIG_HOME`, `GOCACHE`, `GOENV=off`, `GOFLAGS=`,
`GOTOOLCHAIN=local`, `GOOS`, `GOARCH`, `PWD`) and `TERM=dumb`.

## `gonixgo link` for a binary with cgo packages

`link.Manifest` gains `cgo` and `cxx`. When `cgo` is set:

- `-extld=<compiler>` is added after the caller's `ldflags`, unless they
  already name an `-extld`. The compiler is `$CXX` when `cxx` is set,
  otherwise `$CC`.
- `go tool link` runs with the derivation's environment plus the Go
  variables above and `GOROOT=`.

The Go linker decides between internal and external linking, as it does
under `go build`. A package's link flags travel inside its archive, where
cgo recorded them, so the link needs no flag list of its own; it needs the
libraries, which `buildInputs` provide.

A binary without cgo packages links without a C compiler, as today, even
when cgo is enabled and it uses the cgo parts of the standard library.

## Error handling

| Situation | Behaviour |
|---|---|
| A package has SWIG, Fortran or `.syso` files | `resolve` reports it with the other load problems, naming the package and the kind of file. |
| A local package's `#cgo` directive names a path outside `src` | `resolve` reports it, naming the package and the path. |
| A `packageOverrides` entry has an unknown attribute | `buildGoApplication` throws, naming the key and the attribute. |
| A `packageOverrides` key matches no cgo package of the build | A warning naming the key. The build goes on. |
| A package uses `#cgo pkg-config:` and `pkg-config` is not on `PATH` | `compile` fails, naming the pkg-config packages and the two `packageOverrides` attributes to set. |
| `pkg-config` fails | Its output, then the same hint. |
| The C compiler fails | Its output, then one line saying that a missing header or library is supplied through `packageOverrides."<import path>".buildInputs`. |
| The trial link fails | Not an error; see above. The log gets the linker's output and says so. |
| The final link fails | The linker's output, then the same one-line hint. |

## Not in this version

- **`.syso` files.** They stay rejected. Support is a possible later
  addition: a `sysoFiles` field on the compile node, the files appended to
  the archive as cmd/go appends them, and a C toolchain for the link of a
  binary that contains one.
- **SWIG and Fortran.** Rejected.
- **Files only an `#include` reaches**, in a local package: a header or
  source in a subdirectory that no `#cgo` directive names, or a file in the
  package directory with an extension `go list` does not report. Naming the
  directory in a directive, such as `#cgo CFLAGS: -I${SRCDIR}/sub`, makes it
  part of the package's source. An `extraSrc` override is a possible later
  addition.
- **cmd/go's allowlist for `#cgo` flags.** `go build` refuses flags outside
  a safe list so that building a dependency cannot run arbitrary code. A Nix
  build runs arbitrary code by design and relies on the sandbox, so gonixgo
  passes the flags through. The Nix sandbox is off by default on macOS.
- **`-linkmode=external` for a binary with no cgo packages.** Its link
  derivation has no C compiler.
- **Cross-compiling cgo packages.** Stage 2 covers cross-compilation. The
  cgo derivations use `pkgs.stdenv`, whose compiler targets the host
  platform, which is what stage 2 will need.
- **Tests of cgo packages.** Stage 4.
- **Hardening flags.** The compiler wrapper applies nixpkgs' defaults, as it
  does under `buildGoModule`. There is no override attribute for them.

## Departures from the 2026-10-03 design

| 2026-10-03 design | This design | Why |
|---|---|---|
| The C toolchain is available at link whenever cgo is enabled. | Only for a binary that contains a cgo package. | Pure-Go programs keep bare link derivations. |
| A compile node may carry `sysoFiles`. | No `sysoFiles`; `.syso` is rejected. | Left for later. |
| `cgoFiles`, `cFiles`, `cxxFiles`, `hFiles` and `cgo` are separate node fields; `cgo` has `cflags`, `ldflags`, `pkgConfig`. | One `cgo` attribute holds the files and the five lists. No `hFiles`: headers are found through `-I`, and a local package's are in its source copy. | The presence of `cgo` selects the builder. |
| Not covered. | `${SRCDIR}` is restored by `resolve`, expanded by `compile`, and the paths it names are added to a local package's source. | `go list` expands it to a path the build does not have. |
| Not covered. | Objective-C files. | Common on macOS; they build like C files. |
| Not covered. | cmd/go's flag allowlist is not ported. | See above. |

The 2026-10-03 document is edited to agree.

## Testing

### Unit tests (Go)

- `graph`: a cgo package is accepted and its node filled; `"C"` is not a
  dependency; `${SRCDIR}` is restored; named paths become files and trees;
  a path outside `src`, and SWIG, Fortran and `.syso` files, are load
  problems; `Cgo` and `CXX` on binaries.
- `emit`: golden output for a cgo package and a cgo link; a pure graph's
  output is unchanged; a `${SRCDIR}` flag survives a round trip through
  Nix.
- `cc`: the compiler command line, the flag probes against a fake compiler,
  the pkg-config argument order and output splitting.
- `compile`: a cgo package with a C file, a header directory and an
  exported function is compiled, linked and run with the real toolchain.
- `link`: where `-extld` goes, and that a caller's `-extld` wins.

### Integration fixture `cgo`

```
tests/fixtures/cgo/
  main.go
  internal/cadd/   cgo, a C file, a header under include/ named through
                   ${SRCDIR}, an exported Go function called from C, a .S file,
                   a cgo file that returns its __FILE__, a macro that only
                   $CGO_CFLAGS defines
  internal/cxx/    a C++ file behind an extern "C" header
  internal/objc/   an Objective-C file on darwin, a pure-Go stand-in elsewhere
  internal/zstd/   #cgo pkg-config: libzstd
  internal/lz4/    #cgo LDFLAGS: -llz4
  internal/pure/   pure Go
```

It also imports a small third-party cgo module,
`github.com/mattn/go-pointer`. Its `packageOverrides` give `internal/zstd`
zstd and pkg-config, `internal/lz4` lz4 alone, and `internal/cadd` a
`CGO_CFLAGS` that defines the macro. lz4 has no pkg-config flags to carry
its path, so it links only if `buildInputs` reach the link. Neither library
is in the macOS SDK, so neither builds without its entry.

`tests/run.sh` asserts:

- The binary runs and prints the expected line. The line holds the macro's
  value, which shows the entry's `env` reached the compile, and the
  `__FILE__` of a cgo file, which must be what `go build -trimpath` makes
  it: `/_/<module>/<path>`.
- `go version -m` matches a `go build -trimpath -buildvcs=false` of the
  fixture. The reference build runs in a Nix shell that holds the same
  libraries.
- Editing `internal/cadd/add.c`, or the header under `include/`, changes
  the derivations of `internal/cadd`, the main package and the binary, and
  no others.
- The result's references include no `gosrc-`, `gomod-`, `golocal-` or
  `gopkg-` path.
- `internal/pure` in this fixture, and the link of the `nethttp` fixture,
  are not stdenv derivations.
- An entry for an import path wins over one for the module; an unknown
  attribute is an error naming it; a key that matches no cgo package is a
  warning naming it; `goEnv.builders` works without `packageOverrides`.

## Changes by file

| File | Change |
|---|---|
| `internal/golist/golist.go` | Decode the cgo flag lists. |
| `internal/graph/` | Accept cgo packages; fill the cgo fields; restore `${SRCDIR}`; source trees; `Cgo` and `CXX` on binaries; the narrower rejection. |
| `internal/emit/emit.go` | Print `cgo`, `trees`, and the link's `cgo` and `cxx`. |
| `internal/cc/` (new) | The C compiler as cmd/go drives it: command line, flag probes, pkg-config. |
| `internal/compile/` | `cgo.go` (new) holds the cgo steps; `compile.go` calls it, adds the generated Go files, drops `-complete`, and appends the objects. |
| `internal/gotool/gotool.go` | Run a tool with the derivation's environment. |
| `internal/link/link.go` | `cgo` and `cxx`: `-extld` and the environment. |
| `nix/builders.nix` | The stdenv forms of `compile` and `link`, the override lookup, `trees`, link inputs. |
| `nix/mk-go-env.nix`, `nix/build-go-application.nix` | Pass `stdenv` and `packageOverrides`; check the override attributes. |
| `flake.nix`, `tests/fixtures.nix`, `tests/run.sh`, `tests/fixtures/cgo/` | The fixture, its libraries, the reference shell, the checks. |
| `README.md` | Document `packageOverrides` and cgo; update the list of what is not supported. |
