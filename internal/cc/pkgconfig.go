package cc

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/draganm/gonixgo/internal/gotool"
)

// PkgConfig runs pkg-config for the arguments of a package's #cgo
// pkg-config directives and returns the compiler and the linker flags. The
// program is the first field of $PKG_CONFIG in env, or pkg-config.
func PkgConfig(env []string, dir string, args []string) (cflags, ldflags []string, err error) {
	// pkg-config takes flags anywhere; cmd/go moves them to the front and
	// ends them with its own "--".
	var flags, names []string
	for _, arg := range args {
		switch {
		case arg == "--":
		case strings.HasPrefix(arg, "--"):
			flags = append(flags, arg)
		default:
			names = append(names, arg)
		}
	}
	prog := "pkg-config"
	if fields, err := gotool.SplitFlags([]string{getenv(env, "PKG_CONFIG")}); err != nil {
		return nil, nil, fmt.Errorf("$PKG_CONFIG: %w", err)
	} else if len(fields) > 0 {
		prog = fields[0]
	}
	run := func(mode string) ([]string, error) {
		cmdArgs := append(append([]string{mode}, flags...), "--")
		cmdArgs = append(cmdArgs, names...)
		cmd := exec.Command(prog, cmdArgs...)
		cmd.Dir = dir
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w\n%s", prog, strings.Join(cmdArgs, " "), err, bytes.TrimSpace(stderr.Bytes()))
		}
		return SplitPkgConfigOutput(out)
	}
	if cflags, err = run("--cflags"); err != nil {
		return nil, nil, err
	}
	if ldflags, err = run("--libs"); err != nil {
		return nil, nil, err
	}
	return cflags, ldflags, nil
}

// getenv returns the last value of key in env.
func getenv(env []string, key string) string {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value = v
		}
	}
	return value
}

// SplitPkgConfigOutput splits pkg-config's output into flags as a POSIX
// shell splits a line into words, without any expansion: a character the
// shell would act on is an error unless it is quoted.
func SplitPkgConfigOutput(out []byte) ([]string, error) {
	var flags []string
	var word []byte
	inWord := false // a word has begun; quotes can begin an empty one
	quote := byte(0)
	for i := 0; i < len(out); i++ {
		ch := out[i]
		switch {
		case quote == '\'':
			if ch == '\'' {
				quote = 0
			} else {
				word = append(word, ch)
			}
		case quote == '"':
			switch {
			case ch == '"':
				quote = 0
			case ch == '\\' && i+1 < len(out) && strings.IndexByte("$`\"\\\n", out[i+1]) >= 0:
				// Inside double quotes a backslash escapes only these.
				i++
				if out[i] != '\n' {
					word = append(word, out[i])
				}
			case ch == '$' || ch == '`':
				return nil, fmt.Errorf("pkg-config output has an unescaped %q", ch)
			default:
				word = append(word, ch)
			}
		case ch == '\\':
			if i+1 == len(out) {
				return nil, errors.New("pkg-config output ends in a backslash")
			}
			i++
			// A backslash before a newline continues the line.
			if out[i] != '\n' {
				word = append(word, out[i])
				inWord = true
			}
		case ch == '\'' || ch == '"':
			quote, inWord = ch, true
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			if inWord {
				flags = append(flags, string(word))
				word, inWord = word[:0], false
			}
		case strings.IndexByte("|&;<>()$`", ch) >= 0:
			return nil, fmt.Errorf("pkg-config output has an unquoted %q", ch)
		default:
			word = append(word, ch)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("pkg-config output has an unterminated quote")
	}
	if inWord {
		flags = append(flags, string(word))
	}
	return flags, nil
}
