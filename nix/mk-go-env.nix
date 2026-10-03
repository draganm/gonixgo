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
  inherit (pkgs.stdenv.buildPlatform) system;
  goos = go.GOOS;
  goarch = go.GOARCH;

  tool = buildPkgs.callPackage ./tool.nix { };

  # The resolver runs on the machine that evaluates.
  evalTool = evalPkgs.callPackage ./tool.nix { };
  evalGo = evalPkgs.go;

  stdlib = import ./stdlib.nix {
    inherit lib go goos goarch;
    inherit (buildPkgs) runCommand runCommandCC;
  };

  builders = import ./builders.nix {
    inherit lib go tool stdlib system goos goarch;
    inherit (buildPkgs) cacert;
  };

  buildGoApplication = import ./build-go-application.nix {
    inherit lib go evalGo evalTool goos goarch;
    inherit (buildPkgs) runCommand;
    mkBuilders = builders;
  };
in
assert lib.assertMsg (evalGo.version == go.version)
  "gonixgo: evalPkgs has Go ${evalGo.version} but the build uses Go ${go.version}; they must be the same version";
{
  inherit buildGoApplication tool go stdlib builders;
}
