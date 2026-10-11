# gonixgo workspace and vendoring design

Date: 2026-10-12

This design adds `go.work` and `vendor/` to gonixgo. The
[gonixgo design](2026-10-03-gonixgo-design.md) left both out: it forces
`GOWORK=off`, and its `GOFLAGS=-mod=readonly` makes `go list` ignore
`vendor/`. Where the two documents differ this one replaces the earlier one;
see [Departures from the 2026-10-03 design](#departures-from-the-2026-10-03-design).

## Goal

Build projects that use a Go workspace (`go.work`) or vendored modules
(`vendor/`) the way `go build` builds them: the same packages, the same
source paths recorded in the binary, the same module info.

### Success criteria

- A program in a workspace builds with the workspace's other modules from
  `src`. Its module info matches `go build -trimpath` run in the workspace,
  including `(devel)` lines for the other workspace modules and checksums
  that only another module's `go.sum` holds.
- In a workspace, the tests of every workspace module's packages that the
  binaries contain run.
- A program with a `vendor/` directory builds its third-party packages from
  `vendor/` whenever `go build` would. Its module info matches
  `go build -trimpath`, which records no checksums in vendor mode.
- A vendored program evaluates with `GOPROXY=off` and an empty module cache.
- A vendored cgo package records its C files as `go build` does, under
  `/_/vendor`.
- `useGoWork = false` and `useVendor = false` build as `GOWORK=off` and
  `-mod=readonly` do.

## Scope

In this version:

- Workspace mode, from a `go.work` at `modRoot` or above it inside `src`.
- Vendor mode, from `go mod vendor` and from `go work vendor`.
- `replace` directives in either mode, from `go.mod` or `go.work`.

Not in this version; see [Not in this version](#not-in-this-version): a
`go.work` outside `src`, choosing a `go.work` by another name, and `-mod=mod`.

Verified on aarch64-darwin only, like the rest of gonixgo.

## Facts verified

Probed with Go 1.26.7 on aarch64-darwin, with a workspace of two modules
(`app`, `go 1.22`, and `lib`, `go 1.21`), a vendored module using go-cmp,
and a vendored module with a cgo package behind a directory `replace`.

| Fact | Evidence |
|---|---|
| In workspace mode `go list` reports every workspace module with `Main: true`, no version, its own `Dir`, and its own `GoVersion`. | `go list -deps -json` in `app`. |
| `go build -trimpath` in a workspace records a workspace module's files under its import path (`example.com/work/lib/lib.go`) and compiles it with its own `go` directive. | `runtime.Caller` and a loop-variable closure in the probe. |
| Module info in workspace mode lists the binary's own module on the `mod` line and every other workspace module that provides a package as `dep <path> (devel)` with an empty sum. | `go version -m` on the probe. |
| `go list` run in a workspace directory without `go.mod`, such as the workspace root, lists packages of the workspace modules by relative path. | `go list ./app` at the probe's root. |
| `GOWORK=<path>` behaves as the automatic search does. | `go list -m` in `app` with `GOWORK` set. |
| A `go.work` that uses a directory that does not exist makes `go list` fail as a whole: `go: cannot load module ../../outside listed in go.work file: open ../../outside/go.mod: no such file or directory`. | The probe with a third `use`. |
| `go work edit -json <go.work>` prints the `use` directories relative to the `go.work` file's directory (`"Use": [{"DiskPath": "./app"}, …]`). | Run on the probe. |
| With `vendor/modules.txt` present and a `go` directive of 1.14 or later, `go build` uses vendor mode unless `-mod` says otherwise. `go work vendor` writes `vendor/` next to `go.work`, and the workspace then builds from it. | Builds of the probes. |
| In vendor mode `go list` reports a vendored package's directory under `vendor/<import path>`, and its module with `Path`, `Version` and `GoVersion` (from `modules.txt`, e.g. `go 1.13`) but no `Dir`. A module replaced by a directory keeps `Replace` with the replacement's `Dir`. | `go list -deps -json` on the vendored probes. |
| `go build -trimpath` records a vendored package's files as `<path>@<version>/<subdir>`, as for the module cache, and module info in vendor mode has no checksums: `dep github.com/google/go-cmp v0.6.0` with an empty sum, and a directory replacement as `=> ./cdep (devel)`. | `go version -m` and `runtime.FuncForPC` on the probes. |
| cmd/go maps the vendor directory to `/_/vendor` for vendored C files: `__FILE__` in a vendored cgo file reads `/_/vendor/example.com/cdep/cdep.go`. | The probe; `ccompile` in `cmd/go/internal/work/exec.go` (`m.Dir == ""` gives `toPath = "vendor"`). |
| An out-of-date `vendor/` makes `go list` fail as a whole with `go: inconsistent vendoring in …`, which says to run `go mod vendor` and mentions `-mod=readonly`. | The probe with an extra `require`. |
| With `-mod=readonly`, `go list` ignores `vendor/`, in module and in workspace mode. | The probes. |
| Vendored go-cmp is 21 files, 200 KB. | `du` on the probe's `vendor/`. |

## Workspace mode

### Finding `go.work`

- The resolver looks for `go.work` in `src/modRoot` and each directory above
  it, up to and including `src`. The first one found is used: `GOWORK` is
  set to its path for every `go` command the resolver runs. Without one,
  `GOWORK=off`. A `go.work` above `src` is never used.
- `useGoWork ? true` is a new `buildGoApplication` argument; `false` sets
  `GOWORK=off` whatever `src` holds.
- `modRoot` may be a directory without `go.mod` when a `go.work` is in use,
  such as the workspace root with `subPackages = [ "services/api" ]`.
  Otherwise `modRoot` still needs a `go.mod`; the error then also says that
  no `go.work` was found.

### Packages

`go list` reports every workspace module as a main module. That is already
gonixgo's local case: sources from filtered copies of `src`, `-lang` from the
module's own `go` directive, and the import path as the trim path, as
`go build` records it. A workspace module outside `src` fails as a main
module outside `src` already does.

### Module info

Every module that provides a package gets a module info entry, main modules
included: a main module's entry is `dep <path> (devel)` with no sum. A
binary's `dep` lines are those of every module providing a package to it
except its own, whose `mod` line it already has. In module mode this changes
nothing; in workspace mode it adds the other workspace modules.

### Checksums

In workspace mode the checksums come from `go.work.sum` and from the
`go.sum` of every directory the `go.work` uses, read through
`go work edit -json`. In module mode they come from `modRoot`'s `go.sum`, as
before.

### Tests

The tested set stays "main-module packages with test files", which in
workspace mode covers every workspace module. Every module's `go.sum` is in
use, so their test-only imports load.

## Vendor mode

### When

- `go list` runs with `GOFLAGS` empty instead of `-mod=readonly`, so Go
  chooses vendor mode exactly when `go build` would.
- `useVendor ? true` is a new `buildGoApplication` argument; `false` sets
  `GOFLAGS=-mod=readonly`, which ignores `vendor/` and fetches modules as
  before.

### Packages

- A package whose module is not a main module and has no `Dir` is vendored.
  It builds from `src` like a local package: its files from `vendor/`,
  `-lang` from the module's `go` version in `modules.txt`, and the trim path
  `<path>@<version>/<subdir>`. Its compile is named
  `gopkg-<import path>-<version>`, where the version is the replacement's for
  a module replaced by another version and the required one otherwise.
- This holds for replaced modules too: `go mod vendor` copies them into
  `vendor/`, and the test for "vendored" comes before the one for a
  directory replacement.
- The package's vendor directory is its directory without the trailing
  import path; it must end in `vendor` and lie inside `src`.
- Nothing is fetched. `modules` is empty, no module is hashed or added to
  the store, and evaluation needs no network and no module cache.

### Module info

In vendor mode no entry has a checksum: `dep <path> <version>` with an empty
sum, and the `=>` line of a replacement without one either.

### cgo

A vendored cgo package's compile is given the absolute path of its vendor
directory, and its C prefix map sends that directory to `/_/vendor`, as
cmd/go's does. Other cgo packages keep the map derived from the trim path.

## Interfaces

### `buildGoApplication`

| Argument | Default | Meaning |
|---|---|---|
| `useGoWork` | `true` | Use the `go.work` found at `modRoot` or above it inside `src`. `false` builds in module mode, as `GOWORK=off`. |
| `useVendor` | `true` | Build from `vendor/` when `go build` would. `false` ignores `vendor/` and fetches modules, as `-mod=readonly`. |

### `gonixgo resolve`

| Argument | Type | Meaning |
|---|---|---|
| `goWork` | bool | Look for a `go.work` inside `src`. |
| `vendor` | bool | Let `go list` use `vendor/`. |

Both are false when absent, which is module mode with `-mod=readonly`, as
before.

### Graph and manifests

- `Package.VendorDir`: a vendored package's vendor directory relative to the
  source root, `""` otherwise. `Package.Local` now means "built from `src`":
  the main modules, directory replacements and vendored packages.
- The compile node of a vendored cgo package carries `vendorDir`; the
  builder passes `"${src}/${vendorDir}"` to the manifest as `vendorDir`.

## Error handling

| Situation | Behaviour |
|---|---|
| `go list` fails as a whole, or reports packages it could not load | Go's message, then one line per mode in use: `gonixgo used the workspace <go.work path>; set useGoWork = false to build without it` when a `go.work` is in use, and `gonixgo let go use vendor/; set useVendor = false to fetch modules instead` when `useVendor` is on and there is a `vendor` directory where Go looks for one: next to the `go.work` in workspace mode, next to `go.mod` otherwise. Go decides on the directory alone, so a `vendor/` that is not Go's gets the line too. |
| A `go.work` uses a directory outside `src` | Go's "cannot load module … listed in go.work file", then the workspace line. |
| An out-of-date `vendor/` | Go's "inconsistent vendoring" message, or "import lookup disabled by -mod=vendor" for an import it lacks, then the vendor line. |
| `modRoot` has no `go.mod` and no `go.work` is in use | `modRoot "x": no go.mod in <dir>, and no go.work in it or above it inside src`. |
| A vendored package outside a `vendor` directory, or outside `src` | Error naming the package and its directory. |

## Not in this version

- **A `go.work` outside `src`.** Make `src` the directory that holds it.
- **A `go.work` by another name** (`GOWORK=go.ci.work`). `useGoWork` only
  turns the search on or off.
- **`-mod=mod`.** It may edit `go.mod`, which a build cannot keep.

## Departures from the 2026-10-03 design

| 2026-10-03 design | This design | Why |
|---|---|---|
| `go list` runs with `GOWORK=off`. | It runs with the `go.work` found inside `src`, unless `useGoWork = false`. | Workspaces build as `go build` builds them. |
| `go list` runs with `GOFLAGS=-mod=readonly`. | It runs with `GOFLAGS` empty, unless `useVendor = false`. | Go chooses vendor mode by its own rules. Without `vendor/`, Go's default is `-mod=readonly` anyway. |
| A non-main module always comes from the module cache. | A vendored one comes from `src`. | Vendor mode. |
| Only modules outside the main module get module info entries. | Every module that provides a package does; a binary leaves out its own. | Workspace modules appear as `(devel)` dependencies. |

The 2026-10-03 document is edited to agree.

## Testing

### Unit tests (Go)

- **`graph`**, from recorded `go list` output:
  - a vendored package: local, its name, subdirectory, trim path, `-lang`
    and vendor directory;
  - a vendored replaced module: the same, with its `=>` line;
  - vendored module info without checksums;
  - workspace modules as `dep <path> (devel)`, the binary's own module left
    out, and two programs from different workspace modules each listing the
    other;
  - a vendored package outside a `vendor` directory is an error.
- **`resolve`**, with real Go and no network:
  - a `go.work` at `modRoot`, and one above it inside `src`;
  - a `go.work` above `src` is ignored;
  - `goWork = false` is module mode;
  - a workspace root without `go.mod` as `modRoot`;
  - checksums from every used module's `go.sum` and from `go.work.sum`;
  - vendor mode: vendored packages, no modules;
  - `go work vendor`;
  - `vendor = false` ignores `vendor/`;
  - the lines after a failed `go list`: a `use` outside `src`, and an
    out-of-date `vendor/`;
  - the `modRoot` error without `go.mod` or `go.work`.
- **`golist`:** `GOWORK` and `GOFLAGS` reach `go list`, and `WorkUses` reads
  the `use` directories.
- **`compile`:** a vendored cgo package maps its vendor directory to
  `/_/vendor`.

### Integration fixture `workspace`

```
tests/fixtures/workspace/
  go.work   go 1.22; use ./app and ./lib
  app/      example.com/work/app, go 1.22, no requirements and no go.sum.
            The program prints the source paths recorded for its own file,
            for lib's and for go-cmp's Equal, then lib.Captured(). Its test
            passes.
  lib/      example.com/work/lib, go 1.21; requires go-cmp, so only
            lib/go.sum has go-cmp's checksums. Where(), Captured() and
            CmpFile(). Its test passes.
```

`tests/fixtures.nix` builds it with `modRoot = "app"`; `workspaceWith` lays
other arguments over those. `tests/run.sh` asserts:

- **Output:** `example.com/work/app/main.go example.com/work/lib/lib.go
  github.com/google/go-cmp@v0.6.0/cmp/compare.go [3 3 3]`.
- **Module info** matches `go build -trimpath` run in `app`.
- **Tested set:** `example.com/work/app` and `example.com/work/lib`.
- **Workspace root:** `modRoot = "."` with `subPackages = [ "app" ]` prints
  the same.
- **Switch:** with `useGoWork = false`, evaluation fails on
  `example.com/work/lib`.

### Integration fixture `vendored`

```
tests/fixtures/vendored/
  go.mod    example.com/vendored, go 1.22; requires go-cmp v0.6.0 and
            example.com/cdep, replaced by ./cdep
  go.sum    go-cmp's checksums, for useVendor = false
  main.go   prints the source path recorded for go-cmp's Equal and
            cdep.Where()
  cdep/     example.com/cdep, go 1.21: a cgo package whose Where() returns
            the C __FILE__ of its preamble
  vendor/   go mod vendor's output, committed
```

`tests/run.sh` asserts:

- **Output:** `github.com/google/go-cmp@v0.6.0/cmp/compare.go
  /_/vendor/example.com/cdep/cdep.go`.
- **Module info** matches `go build -trimpath`, in the cgo shell.
- **No modules:** `modules` is empty.
- **Offline:** with `GOPROXY=off` and an empty `GOMODCACHE`, the
  `drvPath` evaluates.
- **Switch:** with `useVendor = false`, `modules` holds go-cmp v0.6.0.
- **stdenv:** cdep's compile builds with stdenv.

## Docs

- **README:** `useGoWork` and `useVendor` in the `buildGoApplication` table;
  `go.work` in the monorepo section; a Vendoring section; `go.work` and
  `vendor/` off the "Not yet supported" list.
- **2026-10-03 design:** the environment for `go list`, and its "Not in this
  version".

## Changes by file

| File | Change |
|---|---|
| `nix/build-go-application.nix` | `useGoWork` and `useVendor`, passed to the resolver as `goWork` and `vendor`. |
| `internal/golist/golist.go` | `Options.GOWORK` and `Options.IgnoreVendor` in the environment; `WorkUses`. |
| `internal/resolve/resolve.go` | The `go.work` search, the `modRoot` rule, checksums from the workspace, the lines after a failed `go list`. |
| `internal/graph/pkg.go` | Vendored packages and their vendor directory. |
| `internal/graph/graph.go` | `Package.VendorDir`; module info entries for main modules and without checksums for vendored modules; a binary leaves out its own module. |
| `internal/emit/emit.go` | `vendorDir` on vendored cgo compile nodes. |
| `nix/builders.nix` | `vendorDir` into the compile manifest. |
| `internal/compile/compile.go`, `internal/compile/cgo.go` | `VendorDir` and the `/_/vendor` prefix map. |
| `tests/fixtures.nix`, `tests/run.sh`, `tests/fixtures/workspace/`, `tests/fixtures/vendored/` | The fixtures and their checks. |
| `README.md`, `docs/superpowers/specs/2026-10-03-gonixgo-design.md` | As under [Docs](#docs). |
