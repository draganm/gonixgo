# buildGoApplication resolves the package graph during evaluation, through
# builtins.exec, and turns it into derivations.
{ lib, runCommand, go, evalGo, evalTool, mkBuilders, goos, goarch }:

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

  # Nix builds the tool and Go before running this, if they are missing.
  graphFn = exec [
    "${evalTool}/bin/gonixgo"
    "resolve"
    (builtins.toJSON {
      go = "${evalGo}/bin/go";
      src = srcStr;
      inherit (builtins) storeDir;
      inherit modRoot subPackages tags goos goarch doCheck;
      cgoEnabled =
        if CGO_ENABLED == null then null
        else builtins.elem CGO_ENABLED [ 1 "1" true ];
    })
  ];

  # The builders need the cgo setting the resolver settled on. It is a
  # literal in the graph, so laziness ties the knot.
  graph = graphFn (mkBuilders {
    inherit srcStr ldflags;
    inherit (graph) cgoEnabled;
  });

  checked =
    assert lib.assertMsg (graph.goVersion == go.version)
      "gonixgo: evaluation resolved with Go ${graph.goVersion} but the build uses Go ${go.version}";
    graph;
in
runCommand (if version == null then pname else "${pname}-${version}")
{
  inherit meta;
  passthru = {
    inherit go;
    graph = checked;
    inherit (checked) modules packages bins;
    tests = { };
  };
}
  ''
    mkdir -p $out/bin
    ${lib.concatMapStringsSep "\n" (bin: "cp ${bin}/bin/* $out/bin/") (lib.attrValues checked.bins)}
  ''
