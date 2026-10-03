# mkGoEnv ties gonixgo to one nixpkgs: that nixpkgs builds the tool and the
# standard library, and performs the Go build.
{ pkgs
, go ? pkgs.buildPackages.go
, evalPkgs ? pkgs.buildPackages
}:
let
  inherit (pkgs) lib;
  buildPkgs = pkgs.buildPackages;

  # Derivations run on the build platform and produce code for the target.
  goos = go.GOOS;
  goarch = go.GOARCH;

  tool = buildPkgs.callPackage ./tool.nix { };

  stdlib = import ./stdlib.nix {
    inherit lib go goos goarch;
    inherit (buildPkgs) runCommand runCommandCC;
  };
in
{
  inherit tool go stdlib;
}
