# The integration fixtures, built with the goEnv under test.
{ goEnv }:
{
  hello-deps = goEnv.buildGoApplication {
    pname = "hello-deps";
    src = ./fixtures/hello-deps;
  };
}
