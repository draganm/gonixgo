# The builder functions the generated graph calls. Each defines how one
# kind of node becomes a derivation. The wiring between nodes is in the
# graph `gonixgo resolve` prints.
{ lib, cacert, stdenv, go, tool, stdlib, system, goos, goarch }:

# Per-application settings.
{ srcStr, cgoEnabled, ldflags, packageOverrides }:
let
  goBin = "${go}/bin/go";
  builder = "${tool}/bin/gonixgo";
  std = stdlib cgoEnabled;

  # What a cgo package needs beyond the platform: the packageOverrides
  # entry for its import path, else the one for its module.
  overrideFor = importPath: module:
    packageOverrides.${importPath} or packageOverrides.${module} or { };

  # Every directory on the way to a file: "a/b/c.go" gives [ "a" "a/b" ].
  parents = file:
    let parts = lib.splitString "/" file;
    in lib.genList (i: lib.concatStringsSep "/" (lib.take (i + 1) parts)) (lib.length parts - 1);
in
{
  # A fixed-output derivation holding a module's source tree. resolve adds
  # the tree to the store under this exact path during evaluation, so the
  # builder runs only where that did not happen.
  fetchModule = { name, path, version, hash }:
    derivation {
      inherit name system builder;
      args = [ "fetch" ];
      manifest = builtins.toJSON {
        go = goBin;
        inherit path version;
      };
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = hash;
      impureEnvVars = lib.fetchers.proxyImpureEnvVars ++ [ "GOPROXY" "NETRC" ];
      SSL_CERT_FILE = "${cacert}/etc/ssl/certs/ca-bundle.crt";
    };

  # A store copy of the source holding exactly `files` and everything
  # under each of `trees`, all relative to the source root. A change to any
  # other file leaves it untouched.
  localDir = { name, files, trees ? [ ] }:
    let
      keepFile = lib.genAttrs files (_: true);
      keepDir = lib.genAttrs (lib.concatMap parents (files ++ trees)) (_: true);
      # "." is the source root itself.
      inTree = rel: lib.any (tree: tree == "." || rel == tree || lib.hasPrefix "${tree}/" rel) trees;
    in
    builtins.path {
      inherit name;
      path = srcStr;
      filter = path: type:
        let rel = lib.removePrefix "${srcStr}/" path;
        in inTree rel || (if type == "directory" then keepDir ? ${rel} else keepFile ? ${rel});
    };

  # One package. Pure Go needs no stdenv: the tool is the builder. A cgo
  # package needs the C compiler, and whatever its override adds, set up as
  # stdenv sets them up; the tool is then the whole build.
  compile =
    { name, importPath, module, src, subdir, trimTo, lang, isMain, goFiles, sFiles, embed, deps, cgo ? null, ... }:
    let
      manifest = {
        go = goBin;
        inherit goos goarch importPath isMain trimTo lang goFiles sFiles embed;
        srcDir = if subdir == "" then "${src}" else "${src}/${subdir}";
        importcfgs = [ "${std}/importcfg" ] ++ map (dep: "${dep}/importcfg") deps;
      };
      override = overrideFor importPath module;
    in
    if cgo == null then
      derivation
        {
          inherit name system builder manifest;
          args = [ "compile" ];
          __structuredAttrs = true;
        }
    else
      stdenv.mkDerivation {
        inherit name;
        __structuredAttrs = true;
        strictDeps = true;
        manifest = manifest // { inherit cgo; };
        nativeBuildInputs = override.nativeBuildInputs or [ ];
        buildInputs = override.buildInputs or [ ];
        env = override.env or { };
        buildCommand = "${builder} compile";
        # The link of a binary with this package needs its libraries too.
        passthru.linkInputs = override.buildInputs or [ ];
      };

  # One binary. With a cgo package in it, the Go linker runs the C linker,
  # which needs the libraries of every such package.
  link = { name, binName, main, deps, modinfo, godebug, cgo ? false, cxx ? false }:
    let
      manifest = {
        go = goBin;
        inherit goos goarch binName modinfo godebug ldflags;
        main = "${main}/pkg.a";
        importcfgs = [ "${std}/importcfg" "${main}/importcfg" ]
          ++ map (dep: "${dep}/importcfg") deps;
      };
    in
    if !cgo then
      derivation
        {
          inherit name system builder manifest;
          args = [ "link" ];
          __structuredAttrs = true;
        }
    else
      stdenv.mkDerivation {
        inherit name;
        __structuredAttrs = true;
        strictDeps = true;
        manifest = manifest // { inherit cgo cxx; };
        buildInputs = lib.unique (lib.concatMap (pkg: pkg.linkInputs or [ ]) ([ main ] ++ deps));
        buildCommand = "${builder} link";
      };
}
