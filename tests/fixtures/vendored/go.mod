module example.com/vendored

go 1.22

require (
	example.com/cdep v0.0.0-00010101000000-000000000000
	github.com/google/go-cmp v0.6.0
)

replace example.com/cdep => ./cdep
