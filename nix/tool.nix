# The gonixgo binary. It imports nothing outside the standard library, so
# there is no vendor hash to maintain.
{ lib, buildGoModule }:
let
  # Only what the binary is built from. Editing docs, tests or the Nix
  # library must not rebuild the tool, and with it every package.
  goSources = dir:
    lib.fileset.fileFilter (f: f.hasExt "go" && !lib.hasSuffix "_test.go" f.name) dir;
in
buildGoModule {
  pname = "gonixgo";
  version = "0.1.0";
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      (goSources ../cmd)
      (goSources ../internal)
    ];
  };
  vendorHash = null;
  subPackages = [ "cmd/gonixgo" ];
  # A static binary can be the builder of a derivation with no other inputs.
  env.CGO_ENABLED = 0;
  doCheck = false;
}
