# The integration fixtures, built with the goEnv under test.
{ goEnv }:
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
}
