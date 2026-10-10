# gonixgo tests design

Date: 2026-10-10

This is stage 4 of the build order in the
[gonixgo design](2026-10-03-gonixgo-design.md). It fills in that document's
Tests section and replaces it where the two differ; the differences are
listed under
[Departures from the 2026-10-03 design](#departures-from-the-2026-10-03-design).

## Goal

Run the tests of a program's own packages as part of its build.

`buildGoApplication` accepts `doCheck` and `checkFlags` today and ignores
them. `buildGoModule` runs `go test` over the whole module in one check
phase, so any edit reruns every test. Here each package's tests are
derivations of their own, made of the same kinds of nodes as the program,
so an edit reruns only the tests it can affect.

### Success criteria

- With the default `doCheck = true`, building a program runs the tests of
  every main-module package that its binaries contain and that has test
  files. A failing test fails the build.
- Tests that read `testdata/` by a relative path or through
  `runtime.Caller`, run tools, write under `$HOME`, or read other files of
  the repository pass as they do under `go test`, given the declarations
  this design adds.
- Editing a test file, or a file under `testdata/`, reruns that package's
  tests and changes no package or binary of the program.
- The program's packages and binaries are the same derivations with tests
  on or off, and the result refers to nothing the tests built.
- With `doCheck = false`, the graph has no module that only tests need.
- A test binary's module info matches `go test -c -trimpath`.

## Scope

In this version:

- Internal tests (`_test.go` files in package `P`) and external tests
  (package `P_test`), with `Test`, `Benchmark`, `Fuzz` and `Example`
  functions and `TestMain`.
- Test-only dependencies, local or third-party.
- `//go:embed` in test files.
- cgo packages under test.
- `checkFlags`, `checkEnv` and `nativeCheckInputs` for the whole program and
  per package, and `testExtraSrc` per package.

Not in this version; see [Not in this version](#not-in-this-version): vet,
coverage, the race detector, fuzzing, `-json` output, tests of other
modules, packages with only test files, and tests when cross-compiling.

Verified on aarch64-darwin only, like the rest of gonixgo.

## Facts verified

Probed with Go 1.26.7, Nix 2.26.1 and nixpkgs `nixos-26.05` on
aarch64-darwin. The probe module has:

- a package with internal and external tests, an `Example`, `TestMain`, a
  benchmark, a fuzz target and `testdata/`;
- the chain `p ← q ← p_test`;
- a main package with tests;
- packages with only internal or only external tests;
- embeds in a package, in an internal test file and in an external test
  file.

The design relies on these facts.

| Fact | Evidence |
|---|---|
| `go list -e -deps -json` without `-test` reports `TestGoFiles` and `XTestGoFiles` for every package. | The first pass's output. |
| With `-test`, a package `P` with internal test files gets a copy `P [P.test]` whose `ForTest` is `P` and whose `GoFiles` are `P`'s files followed by its internal test files. Without internal test files there is no copy, and the external test imports `P` itself. | The probe's packages with both kinds of test and with external tests only. |
| The external test package is `P_test [P.test]`, also with `ForTest` `P`. | Same. |
| A package that the external test imports and that itself imports `P` is printed again as `Q [P.test]`, with `ForTest` `P`, `DepOnly`, and an `ImportMap` that sends `P` to `P [P.test]`. | The chain `p ← q ← p_test`. |
| The test main is the package `P.test`, named `main`, with no `ForTest`. Its one `GoFiles` entry is the source cmd/go generated, kept in `GOCACHE` as `<hash>-d`, and it reports `DefaultGODEBUG`. | The probe's `P.test`; the file read back from the cache. |
| The generated source registers the `Test`, `Benchmark` and `Fuzz` functions and the `Example` functions with their expected output, sets `testdeps.ModulePath` and `testdeps.ImportPath`, and calls `TestMain` when the package defines it. | The file read back from the cache. |
| `P [P.test]` reports `EmbedPatterns` and `TestEmbedPatterns`, and its `EmbedFiles` cover both. `P_test [P.test]` reports `EmbedFiles` but no patterns; they are `P`'s `XTestEmbedPatterns`. | An embed in each kind of test file. |
| `go list` needs a build cache, with or without `-test`: under `GOCACHE=off` it fails with "build cache is disabled by GOCACHE=off, but required as of Go 1.12". A new, empty `GOCACHE` works but rebuilds the standard library's module index. | `GOCACHE=off go list -deps`; `GOCACHE=<empty dir>`: 389 index files written. |
| `go test -x -trimpath` compiles `P [P.test]` with `-p P`, also when `P` is a main package; `P_test [P.test]` with `-p P_test`; `Q [P.test]` as it compiles `Q`; and the test main with `-p main`, `-complete` and the main module's `-lang`. | `go test -x -a -trimpath`. |
| The test main is recorded as `_testmain.go`, with no directory, with or without `-trimpath`. Without `-trimpath`, `P`'s test files are recorded by their absolute paths. | `go tool objdump` on `go test -c` binaries. |
| The link adds `-X testing.testBinary=1` after the caller's `-ldflags`. `-s -w` are added only when `go test` runs the binary itself, not under `-c`. | `go test -x`; `cmd/go/internal/load/test.go`. |
| `go test` runs the binary as `<bin> -test.paniconexit0 -test.timeout=10m0s <flags>` in the package directory with `$GOROOT/bin` first on `PATH`, kills it a minute after the timeout, and ends with `ok  \t<P>\t<seconds>s` or `FAIL\t<P>\t<seconds>s`. | `go test -x`; `cmd/go/internal/test/test.go`. |
| `go test` hands these flags to the binary, prefixed with `test.`: `artifacts`, `bench`, `benchmem`, `benchtime`, `blockprofile`, `blockprofilerate`, `count`, `coverprofile`, `cpu`, `cpuprofile`, `failfast`, `fullpath`, `fuzz`, `fuzzminimizetime`, `fuzztime`, `list`, `memprofile`, `memprofilerate`, `mutexprofile`, `mutexprofilefraction`, `outputdir`, `parallel`, `run`, `short`, `shuffle`, `skip`, `timeout`, `trace`, `v`. It also accepts each with the prefix. Flags it does not know go to the binary unchanged. So does everything after `-args`, and `--` together with everything after it. | `cmd/go/internal/test/flagdefs.go` and `testflag.go`. |
| A test binary's module info, for a package that is not main: the path `P.test`, the main module, no `dep` lines, and the build settings with the test main's `DefaultGODEBUG`. For a main package: the program's own module info, unless a `//go:debug` line in a test file changes `DefaultGODEBUG`; then the `P.test` form. | `go version -m` on `go test -c -trimpath` binaries, one of them linking `github.com/fatih/color`; `load/test.go` sets the info before it attaches the test main's imports. |
| With `-trimpath`, module info records `-trimpath=true` and leaves out `-ldflags` and the `CGO_*` flags; without it, it records them. | `go version -m`; `setBuildInfo` in `load/pkg.go`. |
| Without `-trimpath`, cmd/go passes the C compiler the object directory's `-ffile-prefix-map` and not the module directory's. | `cmd/go/internal/work/exec.go`. |
| `buildGoModule` runs `go test -vet=off $checkFlags` with its tags and ldflags, and drops `-trimpath` for tests "in case they reference test assets". | `pkgs/build-support/go/module.nix`. |

### How `go test` builds a package's tests

From `go test -x -a`, checked against `cmd/go/internal/load/test.go`. For a
package `P`:

1. **Internal test package**, when `P` has internal test files: `P`'s files
   and its internal test files compile together as `P [P.test]`.
2. **Recompiled dependents.** Each package of the test binary that imports
   `P`, directly or not, compiles again against step 1's archive as
   `Q [P.test]`.
3. **External test package**, when `P` has external test files:
   `P_test [P.test]`, against step 1's archive, or against `P`'s own when
   there is no step 1.
4. **Test main**: `_testmain.go`, compiled as `main`.
5. **Link** into `P.test`.
6. **Run** in `P`'s directory.

## User-facing API

`doCheck` and `checkFlags` take effect, and `buildGoApplication` takes two
more arguments.

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

| Argument | Default | Meaning |
|---|---|---|
| `doCheck` | `true` | Build and run the tests of the program's packages. `false` skips the [`-test` pass](#resolver): nothing that only tests need is resolved or built. |
| `checkFlags` | `[ ]` | Flags for every test, spelt as for `go test`: `-run`, `-skip`, `-short`, `-v`, `-count`, `-timeout` and the other flags `go test` hands to the test binary. |
| `nativeCheckInputs` | `[ ]` | Tools on every test's `PATH`. |
| `checkEnv` | `{ }` | Environment variables for every test. Values are strings. |

A `packageOverrides` entry takes four more attributes. They apply to the
tests of the packages that take the entry:

| Attribute | Default | Meaning |
|---|---|---|
| `testExtraSrc` | `[ ]` | Files and directories, relative to `src`, that the tests read and that are not under the package's `testdata/`. A directory is included whole. |
| `nativeCheckInputs` | `[ ]` | Tools, after the program's. |
| `checkFlags` | `[ ]` | Flags, after the program's. When both give a flag, the package's value wins, as the later flag does. |
| `checkEnv` | `{ }` | Environment, merged over the program's. |

The lookup is the one cgo uses: a package takes the entry for its import
path if there is one, otherwise the entry for its module, and the two are
never merged. One entry may hold cgo and test attributes together.

### What is tested

Every main-module package that has `_test.go` files, after build
constraints and `tags`, and that is one of the program's main packages or
is imported by them, directly or not. These are not tested:

- packages of other modules, third-party ones included;
- main-module packages that only tests import;
- packages with only test files, which nothing imports.

Tests run only where the build platform can execute the target, as with
stdenv's `doCheck`.

### Where a test runs

- **Files.** The test runs in a writable copy of a tree laid out as `src`.
  The tree holds the package's files, its test files, its `testdata/` and
  its `testExtraSrc`. The working directory is the package's.
- **Source paths.** The package and its test files compile from the store
  copy of the same tree, so a path from `runtime.Caller` names a real file,
  which is read-only.
- **`PATH`.** Go's `bin` comes first, then what stdenv puts there: the
  `nativeCheckInputs`, the C compiler and the standard tools.
- **`HOME`.** An empty writable directory.
- **Flags.** `-test.paniconexit0 -test.timeout=10m0s`, then the program's
  `checkFlags`, then the package's.
- **Network.** Whatever the Nix sandbox allows. On macOS loopback is allowed
  too, for tests that serve on it, as `httptest` does.

### Results

`passthru.tests."<import path>"` is a package's test run; its output is the
test log. `passthru.testBins."<import path>"` is its test binary. The binary
builds even when the test fails, so a failure can be rerun by hand:

```sh
nix build '.#app.testBins."example.com/app/internal/web"'
cd internal/web && ../../result/bin/web.test -test.run TestFoo -test.v
```

## The generated graph

The graph gains four sets. Here `p` has both kinds of test, and its
external test imports `q`, which imports `p`:

```nix
testSources."example.com/app/p" = b.testDir {
  name = "gosrc-test-example.com-app-p";
  importPath = "example.com/app/p";
  module = "example.com/app";
  files = [ "p/p.go" "p/p_internal_test.go" "p/p_test.go" ];
  trees = [ "p/testdata" ];
};

testPackages."example.com/app/p [example.com/app/p.test]" = b.compile {
  name = "gotestpkg-example.com-app-p--example.com-app-p.test-";
  importPath = "example.com/app/p";
  src = testSources."example.com/app/p";
  subdir = "p";
  module = "example.com/app";
  trimTo = null;
  lang = "go1.24";
  isMain = false;
  goFiles = [ "p.go" "p_internal_test.go" ];
  deps = [ ];
};

testPackages."example.com/app/q [example.com/app/p.test]" = b.compile {
  name = "gotestpkg-example.com-app-q--example.com-app-p.test-";
  importPath = "example.com/app/q";
  src = b.localDir { name = "gosrc-example.com-app-q"; files = [ "q/q.go" ]; };
  subdir = "q";
  module = "example.com/app";
  trimTo = "example.com/app/q";
  lang = "go1.24";
  isMain = false;
  goFiles = [ "q.go" ];
  deps = [ testPackages."example.com/app/p [example.com/app/p.test]" ];
};

testPackages."example.com/app/p_test [example.com/app/p.test]" = b.compile {
  name = "gotestpkg-example.com-app-p_test--example.com-app-p.test-";
  importPath = "example.com/app/p_test";
  src = testSources."example.com/app/p";
  subdir = "p";
  module = "example.com/app";
  trimTo = null;
  lang = "go1.24";
  isMain = false;
  goFiles = [ "p_test.go" ];
  deps = [
    packages."github.com/google/go-cmp/cmp"
    testPackages."example.com/app/p [example.com/app/p.test]"
    testPackages."example.com/app/q [example.com/app/p.test]"
  ];
};

testPackages."example.com/app/p.test" = b.compile {
  name = "gotestpkg-example.com-app-p.test";
  importPath = "example.com/app/p.test";
  module = "example.com/app";
  trimTo = null;
  lang = "go1.24";
  isMain = true;
  testMain = "\n// Code generated by 'go test'. DO NOT EDIT.\n\npackage main\n…";
  deps = [
    testPackages."example.com/app/p [example.com/app/p.test]"
    testPackages."example.com/app/p_test [example.com/app/p.test]"
  ];
};

testBins."example.com/app/p" = b.link {
  name = "gotestbin-example.com-app-p";
  binName = "p.test";
  main = testPackages."example.com/app/p.test";
  deps = [ /* transitive closure, standard library excluded */ ];
  modinfo = "…";
  godebug = "…";
  test = true;
};

tests."example.com/app/p" = b.runTest {
  name = "gotest-example.com-app-p";
  importPath = "example.com/app/p";
  module = "example.com/app";
  src = testSources."example.com/app/p";
  subdir = "p";
  bin = testBins."example.com/app/p";
  binName = "p.test";
};
```

`testSources` holds one tree per tested package. `b.testDir` adds the
entry's `testExtraSrc` to it.

`trimTo = null` compiles without rewriting the source directory, so the
files keep their store paths. It is set on the package under test, its
external test package and the test main. A recompiled dependent such as
`q [p.test]` keeps its own source and `trimTo`: only what is under test is
untrimmed.

`testMain` is the source `go list` generated. A node with it has no `src`,
`subdir` or `goFiles`.

Further rules:

- `P [P.test]` of a main package has `isMain = false`, because `go test`
  compiles it with `-p P`.
- The `embed` of `P [P.test]` maps `EmbedPatterns` and `TestEmbedPatterns`
  over its `EmbedFiles`. The `embed` of `P_test [P.test]` maps `P`'s
  `XTestEmbedPatterns` over its own `EmbedFiles`.
- `P [P.test]` of a cgo package carries the same `cgo` attribute as `P`.
- A test-only package, third-party or local, is an ordinary `packages`
  node, and its module an ordinary `modules` node.
- `test = true` on a link adds `-X testing.testBinary=1`.
- With no tests, the four sets are empty.

| Node | Derivation name |
|---|---|
| `testSources."<P>"` | `gosrc-test-<P>` |
| `testPackages."<go list import path>"` | `gotestpkg-<go list import path>` |
| `testBins."<P>"` | `gotestbin-<P>` |
| `tests."<P>"` | `gotest-<P>` |

These names are sanitised like the other derivation names.

## Resolver

`golist.Package` decodes `ForTest`, `TestGoFiles`, `XTestGoFiles`,
`TestEmbedPatterns` and `XTestEmbedPatterns`.

**Tested set.** The main-module packages of the first pass's graph whose
`TestGoFiles` or `XTestGoFiles` are not empty.

**The `-test` pass.** When `doCheck` is set and the tested set is not empty,
`resolve` runs `go list -e -deps -test -json` with the first pass's
environment and tags, naming the tested packages by import path. `go`
writes each test main into the build cache. `resolve` uses the caller's
cache, as the first pass does. If `go env GOCACHE` is `off`, both passes
get a temporary cache, removed afterwards: `go list` needs one either way.

From the output `resolve` takes:

- the packages with `ForTest` set: `P [P.test]`, `P_test [P.test]` and
  `Q [P.test]`;
- each `P.test`, reading its source file at once;
- the packages outside the standard library that the first pass did not
  print. These test-only packages become ordinary nodes, and their modules
  are hashed and pre-seeded like the others.

The first pass already printed the rest, the program's own packages, and
their nodes do not change.

**Under test or recompiled.** A package with `ForTest` `P` whose import
path, without the bracketed suffix, is `P` or `P_test` is under test. It
compiles from `P`'s test source with `trimTo = null`. Any other is a
recompiled dependent. It compiles from the source of the package it copies,
with that package's `trimTo`. Imports go through `ImportMap`, as for every
package.

**Test source.** `P`'s test source holds:

- `P`'s own source files and trees, including, for a cgo package, the paths
  its directives name;
- its test files;
- the files its test embeds name;
- `P`'s `testdata` directory, if one exists at evaluation time.

**Test binaries.** There is one per tested package, named as `go test -c`
names it: `P`'s binary name followed by `.test`.

- **`deps`:** the transitive imports of `P.test` across `packages` and
  `testPackages`.
- **`godebug`:** `P.test`'s `DefaultGODEBUG`.
- **cgo:** it is a cgo link when one of its packages is a cgo package, as
  for any binary.
- **Module info:** it follows `go test -c -trimpath`.
  - A package that is not main gets the path `P.test`, the main module at
    `(devel)`, no `dep` lines, and the settings with `P.test`'s
    `DefaultGODEBUG`.
  - A main package whose `P.test` reports the same `DefaultGODEBUG` as `P`
    gets the program's own module info.

**Load problems.** A package of the `-test` pass with `Error` set fails
resolution, as in the first pass. All of them are reported together. When
one mentions `go.sum`, the report ends with the `go mod tidy` hint, and in
every case with a line saying that `doCheck = false` builds without tests.

## Builders

`mkBuilders` takes three more per-application settings: `checkFlags`,
`nativeCheckInputs` and `checkEnv`. Each is empty when a caller of
`goEnv.builders` passes none.

### `compile`

- **`trimTo = null`** leaves the source directory as it is. The manifest's
  `trimTo` is then empty.
- **`testMain`**, a string, goes to the tool in the manifest. A node with it
  has no `src`, `subdir` or `goFiles`, so `compile` takes those as optional.

The rest is as before. A node without `cgo` is a bare derivation; one with
`cgo` builds with stdenv and the package's entry. `P [P.test]` takes `P`'s
entry, because its `importPath` is `P`.

### `link`

`test = true` puts `test` in the manifest. A test binary that contains a
cgo package links through stdenv with its packages' link inputs, like any
binary.

### `testDir`

`b.testDir { name, importPath, module, files, trees ? [ ] }` is
`b.localDir` with the entry's `testExtraSrc` added to `trees`. A
`testExtraSrc` path that is absolute, has a `..` element, or does not exist
under `src` is an evaluation error naming the key and the path.

### `runTest`

`b.runTest { name, importPath, module, src, subdir, bin, binName }` is a
`stdenv.mkDerivation`. It has:

- `__structuredAttrs = true` and `strictDeps = true`;
- `nativeBuildInputs`: the program's `nativeCheckInputs`, then the entry's;
- `manifest`: `go`, `importPath`, the binary's path, `srcDir` (the test
  source), `subdir`, `flags` and `env`. `flags` are the program's
  `checkFlags` followed by the entry's. `env` is the program's `checkEnv`
  with the entry's merged over it;
- `__darwinAllowLocalNetworking = true`;
- `buildCommand`, which runs `gonixgo test`;
- `passthru.overrideKeys`, the two keys the package would take.

Its output is a file: the test log.

## `gonixgo compile` and `gonixgo link`

`compile.Manifest` gains `testMain`.

- **Untrimmed.** With an empty `trimTo`, `-trimpath` is `<work dir>=>`
  alone, for the compiler and the assembler alike. In a cgo package the C
  compiler gets the work directory's `-ffile-prefix-map` and not the source
  root's, as cmd/go does without `-trimpath`.
- **Test main.** With `testMain` set, `compile` writes it to `_testmain.go`
  in its work directory and compiles that one file. The work directory is
  trimmed away, so the file is recorded as `_testmain.go`, as under
  `go test`.

`link.Manifest` gains `test`. When it is set, `-X testing.testBinary=1`
follows the caller's `ldflags`, where cmd/go puts it, so `testing.Testing()`
reports true. The program's `ldflags` apply to its test binaries, as under
`buildGoModule`. As under `go test -c`, the binary is not stripped.

## `gonixgo test`

A new subcommand, in the new package `internal/testrun`. It reads its
manifest from `NIX_ATTRS_JSON_FILE`, as `compile` and `link` do, and then:

1. **Copies the tree.** It copies `srcDir` into a writable directory in the
   build directory, keeping symlinks and modes, and runs the binary in
   `<copy>/<subdir>`.
2. **Sets the environment.** It starts from the derivation's environment,
   prepends Go's `bin` directory to `PATH`, sets `HOME` to a new empty
   directory and `PWD` to the working directory, and then applies the
   manifest's `env`, which wins over all of them.
3. **Passes the flags.** First `-test.paniconexit0 -test.timeout=10m0s`,
   then `flags`, translated as `go test` translates them:
   - A flag that `go test` hands to the binary, written `-name`, `--name`,
     `-name=value` or `--name=value`, gets the `test.` prefix. When it takes
     a value and is written without `=`, the next argument is that value and
     passes unchanged.
   - A flag that already has the `test.` prefix, or that `go test` does not
     know, passes unchanged.
   - `-args` is dropped and everything after it passes as written. `--`
     passes, with everything after it.

   A build flag such as `-race` therefore reaches the binary, which rejects
   it by name.
4. **Enforces the timeout.** It kills the binary one minute after the
   effective `-test.timeout`, the last one given. `0` means no limit.
5. **Records the output.** It writes the binary's standard output and
   standard error, interleaved, both to `$out` and to its own standard
   error, which is the build log.
6. **Reports the result.** It ends with `ok  \t<P>\t<seconds>s` and exit
   status 0. If the binary fails, is killed or does not start, it ends with
   `FAIL\t<P>\t<seconds>s` and exit status 1.

## `buildGoApplication`

- **Arguments.** `nativeCheckInputs` and `checkEnv` are new. They and
  `checkFlags` go to `mkBuilders`.
- **When tests run.** `resolve` receives
  `doCheck && stdenv.buildPlatform.canExecute stdenv.hostPlatform`.
  `mkGoEnv` passes `pkgs.stdenv` for this.
- **Dependency.** The program's derivation lists every `tests.*` derivation
  in an attribute that its build command never writes into the output. A
  failing test therefore fails the build, and the result's references do
  not change.
- **`passthru`** gains `testBins`. `tests` holds the test runs.
- **Entry attributes.** An entry may hold `buildInputs`,
  `nativeBuildInputs`, `env`, `testExtraSrc`, `nativeCheckInputs`,
  `checkFlags` and `checkEnv`. Any other attribute is an error naming it,
  as before.
- **Entries that match nothing.** The warning is given separately for each
  kind of attribute.
  - Cgo attributes warn when no cgo package takes the key; `testPackages`
    nodes count.
  - Test attributes warn when no tested package takes the key. This warning
    is skipped when there are no tests.

## Error handling

| Situation | Behaviour |
|---|---|
| A test fails | `tests."P"` fails, and its log ends with the test output and `FAIL\tP\t<time>`. The program fails because it depends on the test. `testBins."P"` still builds. |
| A test hangs | At `-test.timeout` the binary panics, printing every goroutine's stack. A minute later the runner kills it if it is still running. |
| A test file has a type error | The `gotestpkg-` compile fails with the compiler's message. |
| A test file does not parse, or a test imports a package that no module provides or that `go.sum` lacks | `resolve` reports it with the other problems of the `-test` pass. The `go mod tidy` hint follows when it applies, then a line saying that `doCheck = false` builds without tests. |
| A `testExtraSrc` path is absolute, has a `..` element, or does not exist | Evaluation error naming the key and the path. |
| An entry has an unknown attribute | Evaluation error naming the key and the attribute. The message lists all seven attributes. |
| No tested package takes an entry with test attributes | A warning naming the key. There is no warning when there are no tests. |
| `checkFlags` holds a build flag such as `-race` or `-tags` | The test binary rejects it: `flag provided but not defined: -race`. |
| A test writes through a `runtime.Caller` path | Permission error, because that path is in the read-only store. Relative paths reach the writable copy. |
| A cross build | No `-test` pass; `tests` and `testBins` are empty. Not an error. |
| `go env GOCACHE` is `off` | Both `go list` passes use a temporary cache. |

## Not in this version

- **vet.** `go test` runs a subset of vet's checks; `buildGoModule` turns
  them off with `-vet=off`. A vet node per package is a possible later
  addition.
- **Coverage.** `-cover` is a build flag. `-coverprofile` reaches a binary
  that was not built for coverage, which says so and exits.
- **The race detector**, and the other build flags of `go test`. Build tags
  come from the program's `tags`.
- **Fuzzing.** Seed corpora under `testdata/fuzz` run as ordinary tests.
  `-fuzz` reaches a binary that lacks the instrumentation fuzzing needs.
- **`-json` output** (`test2json`).
- **Tests of other modules.** This covers third-party packages, and local
  modules behind a directory `replace` once those are supported. The main
  module's `go.sum` need not cover their tests' imports.
- **Packages with only test files.** Nothing imports them, so `subPackages`
  cannot reach them.
- **Tests when cross-compiling.** They are skipped, as stdenv skips
  `doCheck`. No `-test` pass runs, so `testBins` are not built either.
- **Writing beside the source through `runtime.Caller`.** That path is
  read-only.

## Departures from the 2026-10-03 design

| 2026-10-03 design | This design | Why |
|---|---|---|
| One `b.test` node per package. `gonixgo test` compiles the variants, recompiles dependents, generates the test main, links and runs. | The variants, recompiled dependents and test main are `testPackages` nodes built by `b.compile`. The binary is a `testBins` node built by `b.link`, and the run a `tests` node built by `b.runTest`. `gonixgo test` only runs. | cgo, `packageOverrides` and the store's caching apply to every piece. Editing a test file rebuilds only that package's test nodes. |
| A `test` node lists `testDeps` and `recompile`. | No such lists; each piece names its own `deps`. | Follows from the nodes. |
| `internal/gotest` generates the test main. | The graph carries, as a string, the test main that `go list -test` generated. | There is no template to keep in step with Go. The tool is built with nixpkgs' default Go, but the test main must suit the Go that builds. `go list` runs with that Go's version, which `mkGoEnv` asserts. |
| Everything compiles with `-trimpath`. | The package under test and its test files compile without it. | `runtime.Caller` paths have to name real files. `buildGoModule` drops `-trimpath` for tests for the same reason. |
| `checkFlags` are passed to the test binary as written. | `checkFlags` are spelt as for `go test`. | As in `buildGoModule`. |
| `packageOverrides` adds only `testExtraSrc`. | It also adds `nativeCheckInputs`, `checkFlags` and `checkEnv`, and `buildGoApplication` takes `nativeCheckInputs` and `checkEnv`. | Tests need tools, environment and per-package flags. |
| Packages of sibling modules behind a directory `replace` are tested. | Only main-module packages are tested. | The main module's `go.sum` need not cover a dependency's test-only imports, so those tests might not load. `buildGoModule` does not test them either. |
| `passthru.tests` only. | Also `passthru.testBins`. | So a failing test can be rerun by hand. |

The 2026-10-03 document is edited to agree.

## Testing

### Unit tests (Go)

- **`golist`:** the new fields decode, and the `-test` pass passes `-test`.
- **`graph`:** uses recorded `go list -test` output of the probe module.
  - The tested set leaves out third-party, test-only and unreached packages.
  - `P [P.test]` exists only with internal tests.
  - `Q [P.test]` is rewired through `ImportMap`.
  - A package with only external tests uses plain `P`.
  - A main package's variant is not main.
  - Both embed maps, the test sources and the names.
  - The three module-info forms.
  - Test-only third-party packages and their modules become ordinary nodes.
  - Problems of the `-test` pass carry the `doCheck` hint.
- **`resolve`:** uses real Go. There is no `-test` pass when `doCheck` is
  false. The test main is read from the cache. A temporary cache is used
  under `GOCACHE=off`.
- **`emit`:** golden output with the four sets, `trimTo = null`, `testMain`
  and `test`. A graph without tests prints the four sets empty.
- **`compile`:** an untrimmed package records its real source path. A
  `testMain` node compiles as `main` and records `_testmain.go`.
- **`link`:** `-X testing.testBinary=1` comes after the caller's `ldflags`.
- **`testrun`:**
  - flag translation, including values, `-args` and `--`;
  - the environment's order and overrides;
  - the copy's modes and symlinks;
  - the kill, with a shortened grace period;
  - the last line and the exit status.

### Integration fixture `tests`

```
tests/fixtures/tests/
  cmd/app/          the program; imports q, xonly and cnum; a test of package
                    main that checks testing.Testing()
  p/                internal and external tests and an Example; a test that
                    reads testdata/ by a relative path and one that reads it
                    through runtime.Caller; an embed in each kind of test file;
                    a test that reads shared/ (testExtraSrc); a test that
                    always fails, which the package's -skip skips; a test
                    that fails when FIXTURE_FAIL is set; go-cmp in the
                    external test
  q/                imports p; p's external test imports it
  xonly/            external tests only, with TestMain; a test that runs the
                    program's nativeCheckInputs tool, writes under $HOME and
                    reads checkEnv
  cnum/             a cgo package whose test checks a macro that its
                    packageOverrides CGO_CFLAGS defines
  internal/helper/  imported only by p's tests; its own test always fails
  shared/           a file p's test reads through testExtraSrc
```

The fixture's entry in `tests/fixtures.nix` sets these:

- **For the program:** `checkFlags = [ "-v" ]`,
  `nativeCheckInputs = [ pkgs.hello ]`, and `checkEnv`.
- **For `p`:** `testExtraSrc`, the `-skip`,
  `nativeCheckInputs = [ pkgs.jq ]`, and a `checkEnv` value that overrides
  the program's.
- **For `cnum`:** `env.CGO_CFLAGS`.

`tests/run.sh` asserts:

- **Build:** the program builds, which means every test passed, and runs.
- **Tested set:** `tests` and `testBins` name exactly `cmd/app`, `cnum`, `p`
  and `xonly`.
- **Skip:** `p`'s log shows its tests ran and the skipped one did not.
- **Module info:** for `p` and `cmd/app`, the test binary's module info
  matches `go test -c -trimpath` of the fixture.
- **Failure:** with `checkEnv.FIXTURE_FAIL = "1"` the build fails, naming
  the failing test and `p`, while `p`'s test binary still builds.
- **`doCheck = false`:** no go-cmp module, empty `tests` and `testBins`, and
  the same `packages` and `bins` derivations as with tests.
- **Incremental:** `check_incremental` now also compares `testPackages`,
  `testBins` and `tests`, and takes the fixture's arguments.
  - Editing `p/p_internal_test.go`, a file under `p/testdata/`, or
    `shared/`'s file changes only `p`'s test nodes.
  - Editing `p/p.go` changes `p`, `q`, the program, and the test nodes built
    on `p`.
- **References:** the program refers to no test derivation.
- **stdenv:** the cgo package's `P [P.test]` builds with stdenv; `p`'s test
  main does not.
- **Overrides:**
  - A misspelt test attribute is an error naming it.
  - A `testExtraSrc` path that is missing or leaves `src` is an error
    naming it.
  - Test attributes on a package that is not tested give a warning, and give
    none with `doCheck = false`.

## Changes by file

| File | Change |
|---|---|
| `internal/golist/golist.go` | Decode `ForTest`, `TestGoFiles`, `XTestGoFiles`, `TestEmbedPatterns` and `XTestEmbedPatterns`; list with `-test`. |
| `internal/graph/` | `tests.go` (new) holds the tested set, test sources, the test nodes and binaries from the `-test` pass, their module info, and its load problems. |
| `internal/emit/emit.go` | Print `testSources`, `testPackages`, `testBins` and `tests`, `trimTo = null`, `testMain` and `test`; refer to `testPackages` nodes. |
| `internal/resolve/resolve.go` | Run the `-test` pass under `doCheck`, read each test main, and fall back to a temporary cache for both passes under `GOCACHE=off`. |
| `internal/compile/` | An empty `TrimTo` in `compile.go` and `cgo.go`; `TestMain`. |
| `internal/link/link.go` | `Test`. |
| `internal/testrun/` (new) | The runner. |
| `cmd/gonixgo/main.go` | The `test` subcommand. |
| `nix/builders.nix` | `trimTo = null`, `testMain` and optional sources in `compile`; `test` in `link`; `testDir`; `runTest`; the check settings. |
| `nix/build-go-application.nix`, `nix/mk-go-env.nix` | Pass `stdenv`. Add the new arguments, when tests run, the dependency on them, `testBins`, the entry attributes and the per-kind warning. |
| `tests/fixtures.nix`, `tests/run.sh`, `tests/fixtures/tests/` | The fixture and its checks. `check_incremental` covers the test sets and takes a fixture's arguments. `check_no_source_refs` also catches test derivations. |
| `README.md` | Document tests, and take them off the list of what is not supported. |
