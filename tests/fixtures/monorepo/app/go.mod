module example.com/monorepo/app

go 1.22

require (
	example.com/monorepo/lib v0.0.0-00010101000000-000000000000
	github.com/google/go-cmp v0.6.0
)

replace example.com/monorepo/lib => ../lib

replace github.com/google/go-cmp => github.com/google/go-cmp v0.7.0
