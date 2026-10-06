package gotool

import "fmt"

// SplitFlags splits each element the way cmd/go splits a flag list, such as
// the argument of `go build -ldflags` or the value of $CGO_CFLAGS: on white
// space, with a leading single or double quote grouping up to its match.
// There is no unescaping inside quotes.
func SplitFlags(flags []string) ([]string, error) {
	var out []string
	for _, flag := range flags {
		s := flag
		for {
			for len(s) > 0 && isSpace(s[0]) {
				s = s[1:]
			}
			if len(s) == 0 {
				break
			}
			if quote := s[0]; quote == '"' || quote == '\'' {
				s = s[1:]
				end := 0
				for end < len(s) && s[end] != quote {
					end++
				}
				if end == len(s) {
					return nil, fmt.Errorf("%q: unterminated %c string", flag, quote)
				}
				out = append(out, s[:end])
				s = s[end+1:]
				continue
			}
			end := 0
			for end < len(s) && !isSpace(s[end]) {
				end++
			}
			out = append(out, s[:end])
			s = s[end:]
		}
	}
	return out, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
