# The builder functions the generated graph calls. Each defines how one
# kind of node becomes a derivation. The wiring between nodes is in the
# graph `gonixgo resolve` prints.
{ lib, cacert, stdenv, go, tool, stdlib, system, goos, goarch }:

# Per-application settings.
{ srcStr, cgoEnabled, ldflags, packageOverrides ? { }
, checkFlags ? [ ], nativeCheckInputs ? [ ], checkEnv ? { }
}:
let
  goBin = "${go}/bin/go";
  builder = "${tool}/bin/gonixgo";
  std = stdlib cgoEnabled;

  # What a package's cgo compile or its tests need beyond the platform:
  # the packageOverrides entry for its import path, else the one for its
  # module.
  overrideFor = importPath: module:
    packageOverrides.${importPath} or packageOverrides.${module} or { };

  # The key of the entry a package takes, for messages; null for none.
  overrideKey = importPath: module:
    if packageOverrides ? ${importPath} then importPath
    else if packageOverrides ? ${module} then module
    else null;

  # Every directory on the way to a file: "a/b/c.go" gives [ "a" "a/b" ].
  parents = file:
    let parts = lib.splitString "/" file;
    in lib.genList (i: lib.concatStringsSep "/" (lib.take (i + 1) parts)) (lib.length parts - 1);

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

  inherit localDir;

  # One package. Pure Go needs no stdenv: the tool is the builder. A cgo
  # package needs the C compiler, and whatever its override adds, set up as
  # stdenv sets them up; the tool is then the whole build.
  compile =
    { name, importPath, module, src ? null, subdir ? "", trimTo, lang, isMain
    , goFiles ? [ ], sFiles ? [ ], embed ? { }, deps, cgo ? null, testMain ? null, ... }:
    let
      manifest = {
        go = goBin;
        inherit goos goarch importPath isMain lang goFiles sFiles embed;
        # null: the package is under test and keeps its source paths.
        trimTo = if trimTo == null then "" else trimTo;
        # A test main has no source; its manifest carries it.
        srcDir = if src == null then "" else if subdir == "" then "${src}" else "${src}/${subdir}";
        importcfgs = [ "${std}/importcfg" ] ++ map (dep: "${dep}/importcfg") deps;
      } // lib.optionalAttrs (testMain != null) { inherit testMain; };
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
        passthru = {
          # The link of a binary with this package needs its libraries too.
          linkInputs = override.buildInputs or [ ];
          # The packageOverrides keys this package would take.
          overrideKeys = [ importPath module ];
        };
      };

  # One binary. With a cgo package in it, the Go linker runs the C linker,
  # which needs the libraries of every such package.
  link = { name, binName, main, deps, modinfo, godebug, cgo ? false, cxx ? false, test ? false }:
    let
      manifest = {
        go = goBin;
        inherit goos goarch binName modinfo godebug ldflags;
        main = "${main}/pkg.a";
        importcfgs = [ "${std}/importcfg" "${main}/importcfg" ]
          ++ map (dep: "${dep}/importcfg") deps;
      } // lib.optionalAttrs test { test = true; };
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

  # The tree a tested package's test copies compile from and its tests run
  # in: the files resolve listed, plus the entry's testExtraSrc.
  testDir = { name, importPath, module, files, trees ? [ ] }:
    let
      key = overrideKey importPath module;
      extra = map
        (path:
          let
            # localDir matches trees by their exact spelling, so ./a/,
            # a//b and a/. are written as a and a/b, and ./ as the root.
            elems = lib.filter (elem: elem != "" && elem != ".") (lib.splitString "/" path);
            clean = if elems == [ ] then "." else lib.concatStringsSep "/" elems;
            where = "packageOverrides.\"${key}\".testExtraSrc: \"${path}\"";
          in
          if lib.hasPrefix "/" path || lib.elem ".." elems then
            throw "gonixgo: ${where} leaves src; testExtraSrc paths are relative to src"
          else if !builtins.pathExists "${srcStr}/${clean}" then
            throw "gonixgo: ${where} does not exist in src"
          else clean)
        ((overrideFor importPath module).testExtraSrc or [ ]);
    in
    localDir { inherit name files; trees = trees ++ extra; };

  # One package's test run. Its output is the test log.
  runTest = { name, importPath, module, src, subdir, bin, binName }:
    let override = overrideFor importPath module;
    in
    stdenv.mkDerivation {
      inherit name;
      __structuredAttrs = true;
      strictDeps = true;
      nativeBuildInputs = nativeCheckInputs ++ override.nativeCheckInputs or [ ];
      manifest = {
        go = goBin;
        inherit importPath subdir;
        bin = "${bin}/bin/${binName}";
        srcDir = "${src}";
        flags = checkFlags ++ override.checkFlags or [ ];
        # Strings, paths and derivations; a path is copied to the store.
        env = lib.mapAttrs (_: value: "${value}") (checkEnv // override.checkEnv or { });
      };
      # Tests that serve on loopback, as httptest does, need it under the
      # macOS sandbox.
      __darwinAllowLocalNetworking = true;
      buildCommand = "${builder} test";
      # The packageOverrides keys this package would take.
      passthru.overrideKeys = [ importPath module ];
    };
}
