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
          fixtures = import ./tests/fixtures.nix {
            inherit goEnv pkgs mkGoEnv;
            # A platform this machine cannot build for, so that evalPkgs
            # is needed to resolve it.
            linuxPkgs = nixpkgs.legacyPackages.x86_64-linux;
          };
          # What a plain `go build` of the cgo fixture needs; the
          # integration tests build their reference binary in it.
          cgoShell = pkgs.mkShell {
            packages = [ goEnv.go pkgs.pkg-config ];
            buildInputs = [ pkgs.zstd pkgs.lz4 ];
          };
        } // nixpkgs.lib.optionalAttrs (system == "aarch64-darwin") {
          # What a plain `go build` of the cgo fixture for x86_64 macOS
          # needs: the cross C toolchain, pkg-config and the libraries for
          # that platform.
          cgoShellX86_64Darwin =
            let cross = pkgs.pkgsCross.x86_64-darwin;
            in cross.mkShell {
              packages = [ goEnv.go cross.pkg-config ];
              buildInputs = [ cross.zstd cross.lz4 ];
            };
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
