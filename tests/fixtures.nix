# The integration fixtures, built with the goEnv under test.
{ goEnv, pkgs }:
{
  hello-deps = goEnv.buildGoApplication {
    pname = "hello-deps";
    src = ./fixtures/hello-deps;
  };
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
  cgo = goEnv.buildGoApplication {
    pname = "cgofix";
    src = ./fixtures/cgo;
    packageOverrides = {
      # The program prints EXTRA. Setting CGO_CFLAGS replaces its default.
      "example.com/cgofix/internal/cadd".env.CGO_CFLAGS = "-O2 -g -DEXTRA=10";
      "example.com/cgofix/internal/zstd" = {
        buildInputs = [ pkgs.zstd ];
        nativeBuildInputs = [ pkgs.pkg-config ];
      };
      # No pkg-config: the library reaches the link through buildInputs.
      "example.com/cgofix/internal/lz4".buildInputs = [ pkgs.lz4 ];
    };
  };
}
