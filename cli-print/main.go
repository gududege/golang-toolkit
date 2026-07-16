// cli-print prints all CLI input (args, env, CWD) and exits with a computed code.
// It is used as a test/debug target for argument forwarding and environment propagation.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// version is set at build time via -ldflags, e.g.:
//
//	go build -ldflags="-X main.version=1.0.0"
var version = "dev"

// quoteString returns s wrapped in double quotes with C-style escaping.
// Special characters (", \, \n, \r, \t) are escaped; an empty string returns "<empty>".
func quoteString(s string) string {
	if len(s) == 0 {
		return "<empty>"
	}

	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			sb.WriteRune(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func main() {
	// Print binary path, working directory, and argument count.
	exePath, _ := os.Executable()
	cwd, _ := os.Getwd()
	args := os.Args[1:]

	fmt.Printf("cli-print: %s\n", exePath)
	fmt.Printf("cwd: %s\n", cwd)
	fmt.Printf("argc: %d\n", len(args))

	// Print each argument with C-style escaping.
	for i, arg := range args {
		fmt.Printf("argv[%d]: %s\n", i, quoteString(arg))
	}

	// Default exit code is the argument count
	exitCode := len(args)

	// Support --exit-code N override
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--exit-code" {
			var parsed int
			if _, err := fmt.Sscanf(args[i+1], "%d", &parsed); err == nil {
				exitCode = parsed
			}
			break
		}
	}

	// Clamp exit code to 0-255 range
	if exitCode < 0 {
		exitCode = 0
	}
	if exitCode > 255 {
		exitCode = 255
	}

	// Read and sort environment variables case-insensitively, then print them.
	env := os.Environ()
	sort.Slice(env, func(i, j int) bool {
		ki, _, _ := strings.Cut(env[i], "=")
		kj, _, _ := strings.Cut(env[j], "=")
		return strings.ToLower(ki) < strings.ToLower(kj)
	})

	fmt.Printf("env: %d variables\n", len(env))
	for _, e := range env {
		key, value, _ := strings.Cut(e, "=")
		fmt.Printf("env:%s: %s\n", key, quoteString(value))
	}

	// Print final exit code and exit.
	fmt.Printf("exit_code: %d\n", exitCode)
	os.Exit(exitCode)
}
