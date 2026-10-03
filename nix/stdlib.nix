# The Go standard library compiled for one target: every archive, plus an
# importcfg listing them. One derivation serves every build in a goEnv.
{ lib, go, runCommand, runCommandCC, goos, goarch }:

cgoEnabled:
let
  # The cgo parts of the standard library need a C compiler.
  run = if cgoEnabled then runCommandCC else runCommand;
in
run "go-stdlib-${go.version}-${goos}-${goarch}${lib.optionalString cgoEnabled "-cgo"}"
{
  nativeBuildInputs = [ go ];
  env = {
    GOOS = goos;
    GOARCH = goarch;
    CGO_ENABLED = if cgoEnabled then "1" else "0";
  };
}
  ''
    export HOME="$NIX_BUILD_TOP/home"
    mkdir -p "$HOME" goroot/pkg

    # go install writes into GOROOT, so build in a writable copy that
    # shares the toolchain's tools and headers.
    goroot="$(go env GOROOT)"
    cp -R "$goroot/src" "$goroot/lib" goroot/
    chmod -R u+w goroot
    ln -s "$goroot/pkg/tool" "$goroot/pkg/include" goroot/pkg/

    GODEBUG=installgoroot=all GOROOT="$NIX_BUILD_TOP/goroot" go install -trimpath std

    mkdir -p "$out"
    cp -R goroot/pkg/*_*/. "$out/"
    (cd "$out" && find . -name '*.a' | sort | sed -e 's|^\./||' -e 's|\.a$||') |
      while read -r pkg; do
        echo "packagefile $pkg=$out/$pkg.a"
      done > "$out/importcfg"
  ''
