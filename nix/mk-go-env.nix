# mkGoEnv ties gonixgo to one nixpkgs: that nixpkgs builds the tool and the
# standard library, and performs the Go build. A cross pkgs, such as
# pkgsCross.aarch64-multiplatform, builds for its host platform.
{ pkgs
  # The Go that builds. It runs on the build platform; the target comes
  # from pkgs.
, go ? pkgs.pkgsBuildBuild.go
  # The package set for the evaluating machine, when it differs from the
  # build platform.
, evalPkgs ? null
  # The Go that resolves the graph at evaluation time. It must be the same
  # version as go; by default it is go itself, or evalPkgs.go.
, evalGo ? null
}:
let
  inherit (pkgs) lib;
  buildPkgs = pkgs.buildPackages;
  # What runs on the build platform and builds for it. Natively these are
  # pkgs' own derivations, and cross builds share them.
  nativePkgs = pkgs.pkgsBuildBuild;

  # Derivations run on the build platform and produce code for the target,
  # the platform the program runs on.
  inherit (pkgs.stdenv.buildPlatform) system;
  target = pkgs.stdenv.hostPlatform.go;
  goos = target.GOOS;
  goarch = target.GOARCH;
  # "" except for 32-bit ARM.
  goarm = target.GOARM or "";

  tool = nativePkgs.callPackage ./tool.nix { };

  # The resolver runs on the machine that evaluates.
  evalTool = (if evalPkgs != null then evalPkgs else nativePkgs).callPackage ./tool.nix { };
  evalGo' =
    if evalGo != null then evalGo
    else if evalPkgs != null then evalPkgs.go
    else go;

  # Where go runs, when it says; nixpkgs' Go does.
  goHost = go.stdenv.hostPlatform or null;

  stdlib = import ./stdlib.nix {
    inherit lib go goos goarch goarm;
    inherit (buildPkgs) runCommand;
    # The cgo parts compile with the C compiler for the target.
    inherit (pkgs) runCommandCC;
  };

  builders = import ./builders.nix {
    inherit lib go tool stdlib system goos goarch goarm;
    inherit (buildPkgs) cacert;
    # cgo packages and the binaries that contain them build with the C
    # compiler for the platform the program runs on.
    inherit (pkgs) stdenv;
  };

  buildGoApplication = import ./build-go-application.nix {
    inherit lib go evalTool goos goarch goarm;
    evalGo = evalGo';
    inherit (buildPkgs) runCommand;
    # Whether the build is cross, and whether the build platform can run
    # the tests.
    inherit (pkgs) stdenv;
    mkBuilders = builders;
  };
in
assert lib.assertMsg (goHost == null || pkgs.stdenv.buildPlatform.canExecute goHost)
  "gonixgo: go runs on ${goHost.system} but builds run on ${system}; take go from pkgs.pkgsBuildBuild, such as pkgs.pkgsBuildBuild.go_1_25";
assert lib.assertMsg (evalGo'.version == go.version)
  "gonixgo: evalGo is Go ${evalGo'.version} but the build uses Go ${go.version}; they must be the same version";
{
  inherit buildGoApplication tool go stdlib builders;
}
