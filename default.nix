# Non-flake entry point: `import ./. { inherit pkgs; }` is mkGoEnv.
args: import ./nix/mk-go-env.nix args
