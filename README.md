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
| `go` | `pkgs.buildPackages.go` | The Go that builds. |
| `evalPkgs` | `null` | Package set for the evaluating machine, when it differs from the build platform; `null` means `pkgs.buildPackages`. |
| `evalGo` | `null` | The Go that resolves the graph during evaluation; `null` means `evalPkgs.go` when `evalPkgs` is given, otherwise `go`. It must be the same version as `go`. |

To choose a Go version: `gonixgo.lib.mkGoEnv { inherit pkgs; go = pkgs.go_1_25; }`.

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
| `CGO_ENABLED` | `null` | `null` uses Go's default for the target, which with nixpkgs' Go is on. |

Binaries land in `$out/bin`, named as `go build` names them. The result's
`passthru` has `packages`, `modules` and `bins`, each a set of derivations,
so one package can be built alone:

```bash
nix build --option allow-unsafe-native-code-during-evaluation true \
  '.#default.packages."example.com/app/internal/web"'
```

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

cgo, tests (`doCheck` is accepted and ignored), `replace` directives,
cross-compilation, `go.work`, and `vendor/` directories. Packages with cgo
files and `replace` directives are rejected during evaluation with a message
naming them; tests are ignored.

The integration tests have been run on aarch64-darwin only; Linux is
untested.

## Development

```bash
nix develop --command go test ./...   # unit tests
tests/run.sh                          # integration tests: real nix builds
```

The design is in `docs/superpowers/specs/2026-10-03-gonixgo-design.md`.

## License

MIT, see [LICENSE](LICENSE).
