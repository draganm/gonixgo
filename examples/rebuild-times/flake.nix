{
  description = "One Go service built two ways, to compare rebuild times";

  inputs.gonixgo.url = "github:draganm/gonixgo";
  # The same nixpkgs, and so the same Go, for both builders.
  inputs.nixpkgs.follows = "gonixgo/nixpkgs";

  outputs = { nixpkgs, gonixgo, ... }:
    let
      eachSystem = f:
        nixpkgs.lib.genAttrs [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ]
          (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = eachSystem (pkgs: {
        # One derivation per package, no hash. Needs
        # --option allow-unsafe-native-code-during-evaluation true
        gonixgo = (gonixgo.lib.mkGoEnv { inherit pkgs; }).buildGoApplication {
          pname = "svc";
          src = ./.;
          # The Prometheus client has a cgo file on macOS, and the gonixgo
          # revision this example is locked to does not build cgo packages.
          CGO_ENABLED = 0;
        };

        # Everything in one derivation. Same settings: no cgo, and no tests,
        # because gonixgo does not run them yet.
        buildGoModule = pkgs.buildGoModule {
          pname = "svc";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-rMgbC2Hvgp6ScuNGQhAkFA+ZT76tZHKwIZthQlKMIrw=";
          env.CGO_ENABLED = 0;
          doCheck = false;
        };
      });
    };
}
