{
  description = "gonixgo";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

    systems.url = "github:nix-systems/default";

  };

  outputs = { self, nixpkgs, systems, ... }@inputs:
    let
      eachSystem = f:
        nixpkgs.lib.genAttrs (import systems)
        (system: f system nixpkgs.legacyPackages.${system});
      mkGoEnv = import ./nix/mk-go-env.nix;
    in {

      lib = { inherit mkGoEnv; };

      packages = eachSystem (system: pkgs: rec {
        gonixgo = (mkGoEnv { inherit pkgs; }).tool;
        default = gonixgo;
      });

      # Anything that needs builtins.exec lives under legacyPackages, which
      # `nix flake check` and `nix flake show` do not evaluate.
      legacyPackages = eachSystem (system: pkgs:
        let goEnv = mkGoEnv { inherit pkgs; };
        in {
          inherit goEnv;
          fixtures = import ./tests/fixtures.nix { inherit goEnv; };
        });

      devShells = eachSystem (system: pkgs: {
        default = pkgs.mkShell {
          shellHook = ''
            # Set here the env vars you want to be available in the shell
          '';
          hardeningDisable = [ "all" ];

          packages = with pkgs; [ go ];
        };
      });
    };
}
