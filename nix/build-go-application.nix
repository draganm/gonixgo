# buildGoApplication resolves the package graph during evaluation, through
# builtins.exec, and turns it into derivations.
{ lib, runCommand, stdenv, go, evalGo, evalTool, mkBuilders, goos, goarch, goarm }:

{ pname
, version ? null
, src
, modRoot ? "."
, subPackages ? [ "." ]
, tags ? [ ]
, ldflags ? [ ]
, CGO_ENABLED ? null
, doCheck ? true
, checkFlags ? [ ]
, nativeCheckInputs ? [ ]
, checkEnv ? { }
, packageOverrides ? { }
, meta ? { }
}:
let
  exec = builtins.exec or (throw ''
    gonixgo runs `gonixgo resolve` during evaluation and needs builtins.exec.
    Enable it in one of these ways:
      nix build --option allow-unsafe-native-code-during-evaluation true ...
      NIX_CONFIG="allow-unsafe-native-code-during-evaluation = true" nix build ...
      allow-unsafe-native-code-during-evaluation = true    (in nix.conf)
    A flake's nixConfig cannot enable it.
  '');

  srcStr = "${src}";

  # Tests run where the build runs; a cross build cannot run them.
  runTests = doCheck && stdenv.buildPlatform.canExecute stdenv.hostPlatform;

  # Nix builds the tool and Go before running this, if they are missing.
  graphFn = exec [
    "${evalTool}/bin/gonixgo"
    "resolve"
    (builtins.toJSON {
      go = "${evalGo}/bin/go";
      src = srcStr;
      inherit (builtins) storeDir;
      inherit modRoot subPackages tags goos goarch goarm;
      doCheck = runTests;
      # A build for another platform: cgo is off there unless asked for.
      cross = stdenv.buildPlatform != stdenv.hostPlatform;
      cgoEnabled =
        if CGO_ENABLED == null then null
        else builtins.elem CGO_ENABLED [ 1 "1" true ];
    })
  ];

  # The builders need the cgo setting the resolver settled on. It is a
  # literal in the graph, so laziness ties the knot.
  graph = graphFn (mkBuilders {
    inherit srcStr ldflags packageOverrides checkFlags nativeCheckInputs checkEnv;
    inherit (graph) cgoEnabled;
  });

  # A misspelt attribute would otherwise be ignored, and the build would
  # fail later for want of what it was meant to supply.
  cgoAttrs = [ "buildInputs" "nativeBuildInputs" "env" ];
  testAttrs = [ "testExtraSrc" "nativeCheckInputs" "checkFlags" "checkEnv" ];
  overrideAttrs = cgoAttrs ++ testAttrs;
  unknownOverrides = lib.concatLists (lib.mapAttrsToList
    (key: entry: map (attr: "packageOverrides.\"${key}\".${attr}")
      (lib.attrNames (removeAttrs entry overrideAttrs)))
    packageOverrides);

  # An entry whose attributes of one kind no package takes changes
  # nothing, so it is most likely a mistake: a misspelt key, a module path
  # without its /v2, a pure-Go package, a package without tests. It is a
  # warning and not an error because the same key may match on another
  # platform or under other build tags.
  takenBy = drvs: lib.concatMap (drv: drv.overrideKeys or [ ]) drvs;
  unmatched = attrs: taken: map (key: "packageOverrides.\"${key}\"")
    (lib.filter (key: lib.any (attr: packageOverrides.${key} ? ${attr}) attrs && !lib.elem key taken)
      (lib.attrNames packageOverrides));
  unmatchedCgo = unmatched cgoAttrs
    (takenBy (lib.attrValues graph.packages ++ lib.attrValues graph.testPackages));
  # Without tests no package takes test attributes, and that is no mistake.
  unmatchedTest =
    if graph.tests == { } then [ ]
    else unmatched testAttrs (takenBy (lib.attrValues graph.tests));
  matchNothing = keys: what:
    "gonixgo: ${lib.concatStringsSep ", " keys} ${if lib.length keys == 1 then "matches" else "match"} no ${what} of this build and ${if lib.length keys == 1 then "has" else "have"} no effect";

  checked =
    assert lib.assertMsg (graph.goVersion == go.version)
      "gonixgo: evaluation resolved with Go ${graph.goVersion} but the build uses Go ${go.version}";
    assert lib.assertMsg (unknownOverrides == [ ])
      "gonixgo: unknown ${lib.concatStringsSep ", " unknownOverrides}; a packageOverrides entry takes ${lib.concatStringsSep ", " overrideAttrs}";
    lib.warnIf (unmatchedCgo != [ ])
      "${matchNothing unmatchedCgo "cgo package"}; a key is the import path of a cgo package or the path of its module"
      (lib.warnIf (unmatchedTest != [ ])
        "${matchNothing unmatchedTest "tested package"} on tests; a key is the import path of a main-module package with tests or the path of its module"
        graph);
in
runCommand (if version == null then pname else "${pname}-${version}")
{
  # With one binary, `nix run` needs no flags.
  meta =
    let names = lib.attrNames checked.bins;
    in lib.optionalAttrs (lib.length names == 1) { mainProgram = lib.head names; } // meta;
  # A failing test fails the build. The script never writes these paths
  # into the output, so they stay out of the result's references.
  gonixgoTests = lib.attrValues checked.tests;
  passthru = {
    inherit go;
    graph = checked;
    inherit (checked) modules packages bins tests testPackages testBins;
  };
}
  ''
    mkdir -p $out/bin
    ${lib.concatMapStringsSep "\n" (bin: "cp ${bin}/bin/* $out/bin/") (lib.attrValues checked.bins)}
  ''
