// Package q stands between p and p's external test.
package q

import "example.com/tests/p"

// Double returns 2n.
func Double(n int) int { return p.Add(n, n) }
