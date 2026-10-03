# The builder functions the generated graph calls. Each defines how one
# kind of node becomes a derivation. The wiring between nodes is in the
# graph `gonixgo resolve` prints.
{ lib, cacert, go, tool, stdlib, system, goos, goarch }:

# Per-application settings.
{ srcStr, cgoEnabled, ldflags }:
let
  goBin = "${go}/bin/go";
  builder = "${tool}/bin/gonixgo";
  std = stdlib cgoEnabled;

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

  # A store copy of the source holding exactly `files`, which are relative
  # to the source root. A change to any other file leaves it untouched.
  localDir = { name, files }:
    let
      keepFile = lib.genAttrs files (_: true);
      keepDir = lib.genAttrs (lib.concatMap parents files) (_: true);
    in
    builtins.path {
      inherit name;
      path = srcStr;
      filter = path: type:
        let rel = lib.removePrefix "${srcStr}/" path;
        in if type == "directory" then keepDir ? ${rel} else keepFile ? ${rel};
    };

  # One package. Pure Go needs no stdenv: the tool is the builder.
  compile =
    { name, importPath, src, subdir, trimTo, lang, isMain, goFiles, sFiles, embed, deps, ... }:
    derivation {
      inherit name system builder;
      args = [ "compile" ];
      __structuredAttrs = true;
      manifest = {
        go = goBin;
        inherit goos goarch importPath isMain trimTo lang goFiles sFiles embed;
        srcDir = if subdir == "" then "${src}" else "${src}/${subdir}";
        importcfgs = [ "${std}/importcfg" ] ++ map (dep: "${dep}/importcfg") deps;
      };
    };

  # One binary.
  link = { name, binName, main, deps, modinfo, godebug }:
    derivation {
      inherit name system builder;
      args = [ "link" ];
      __structuredAttrs = true;
      manifest = {
        go = goBin;
        inherit goos goarch binName modinfo godebug ldflags;
        main = "${main}/pkg.a";
        importcfgs = [ "${std}/importcfg" "${main}/importcfg" ]
          ++ map (dep: "${dep}/importcfg") deps;
      };
    };
}
