# The integration fixtures, built with the goEnv under test.
{ goEnv, pkgs, mkGoEnv, linuxPkgs }:
let
  # The tests fixture's arguments, which run.sh varies through testsWith.
  testsArgs = {
    pname = "testsfix";
    src = ./fixtures/tests;
    subPackages = [ "cmd/app" ];
    checkFlags = [ "-v" ];
    nativeCheckInputs = [ pkgs.hello ];
    checkEnv = { FIXTURE_SCOPE = "program"; FIXTURE_PROGRAM = "1"; };
    packageOverrides = {
      "example.com/tests/p" = {
        testExtraSrc = [ "shared" ];
        checkFlags = [ "-skip" "TestSkipped" ];
        nativeCheckInputs = [ pkgs.jq ];
        checkEnv.FIXTURE_SCOPE = "package";
      };
      # The program prints the macro, and cnum's test checks it.
      "example.com/tests/cnum".env.CGO_CFLAGS = "-O2 -g -DFIXTURE_VALUE=42";
    };
  };

  helloArgs = {
    pname = "hello-deps";
    src = ./fixtures/hello-deps;
  };

  # The cgo fixture's arguments, with its libraries from the package set p.
  cgoArgs = p: {
    pname = "cgofix";
    src = ./fixtures/cgo;
    packageOverrides = {
      # The program prints EXTRA. Setting CGO_CFLAGS replaces its default.
      "example.com/cgofix/internal/cadd".env.CGO_CFLAGS = "-O2 -g -DEXTRA=10";
      "example.com/cgofix/internal/zstd" = {
        buildInputs = [ p.zstd ];
        nativeBuildInputs = [ p.pkg-config ];
      };
      # No pkg-config: the library reaches the link through buildInputs.
      "example.com/cgofix/internal/lz4".buildInputs = [ p.lz4 ];
    };
  };

  # Cross builds take their target from the package set.
  arm64 = pkgs.pkgsCross.aarch64-multiplatform;
  arm64Env = mkGoEnv { pkgs = arm64; };
  armv6Env = mkGoEnv { pkgs = pkgs.pkgsCross.raspberryPi; };
in
{
  hello-deps = goEnv.buildGoApplication helloArgs;
  asm-embed = goEnv.buildGoApplication {
    pname = "asm-embed";
    version = "1.2.3";
    src = ./fixtures/asm-embed;
    subPackages = [ "." "cmd/second" ];
    ldflags = [ "-X main.version=1.2.3" ];
  };
  nethttp = goEnv.buildGoApplication {
    pname = "nethttp";
    src = ./fixtures/nethttp;
  };
  cgo = goEnv.buildGoApplication (cgoArgs pkgs);
  tests = goEnv.buildGoApplication testsArgs;
  # The tests fixture with the attributes f returns, given the default
  # arguments, laid over them.
  testsWith = f: goEnv.buildGoApplication (testsArgs // f testsArgs);

  # Linux builds made here with the native Go. They cannot run here.
  hello-deps-aarch64-linux = arm64Env.buildGoApplication helloArgs;
  hello-deps-armv6l-linux = armv6Env.buildGoApplication helloArgs;
  # cgo stays off in a cross build unless asked for, so evaluating this
  # fails on the package that needs it.
  cgo-aarch64-linux = arm64Env.buildGoApplication (cgoArgs arm64);
  # Only evaluated: this machine resolves, a Linux machine would build.
  hello-deps-x86_64-linux =
    (mkGoEnv { pkgs = linuxPkgs; evalPkgs = pkgs; }).buildGoApplication helloArgs;
}
