# rebuild-times

One small Go service, built from the same source by `buildGoModule` and by
gonixgo, to compare what a rebuild costs. `main.go` imports grpc, cobra and
the Prometheus client, which comes to 126 third-party packages from 14
modules.

```bash
nix build .#buildGoModule
nix build --option allow-unsafe-native-code-during-evaluation true .#gonixgo

./bench.sh    # five rebuilds of each after a one-line change, then one with no change
```

Both builds set `CGO_ENABLED = 0`, because the Prometheus client has a cgo
file on macOS and gonixgo does not build cgo packages yet. Neither runs
tests.

The `vendorHash` in `flake.nix` belongs to the `buildGoModule` half and has
to be updated whenever `go.mod` changes. The gonixgo half has nothing to
update.

## Results

Measured on an Apple M4 Pro (14 cores) with Nix 2.26.1, `max-jobs = 1` and
Go 1.26.7. Rebuild times are the median of five runs.

| | `buildGoModule` | gonixgo |
|---|---|---|
| Rebuild after a one-line change in `main.go` | 7.6 s | 2.3 s |
| Build with nothing changed | 0.6 s | 0.7 s |
| First build | 7 s | 17 s, or 13 s with `-j auto` |

After the one-line change `buildGoModule` compiles all 126 dependencies and
the standard library again. gonixgo builds three derivations: the main
package, the link and the application.

The first build is the one where no package of the service is in the store
yet. Both numbers were taken with the modules already downloaded. gonixgo's
does not include the standard library, which is a derivation of its own and
is built once per Go version: 7.5 s here.

`bench.sh` prints the rebuild and no-change times. The first-build times
were taken by hand, after removing the outputs with `nix store delete`.
