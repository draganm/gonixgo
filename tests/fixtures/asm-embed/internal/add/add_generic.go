//go:build !amd64 && !arm64

// Package add adds numbers, in assembly where it can.
package add

// Add returns a + b.
func Add(a, b int64) int64 { return a + b }
