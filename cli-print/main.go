package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

var version string

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
	exePath, _ := os.Executable()
	cwd, _ := os.Getwd()
	args := os.Args[1:]

	fmt.Printf("cli-print: %s\n", exePath)
	fmt.Printf("cwd: %s\n", cwd)
	fmt.Printf("argc: %d\n", len(args))

	for i, arg := range args {
		fmt.Printf("argv[%d]: %s\n", i, quoteString(arg))
	}

	exitCode := len(args)
	if exitCode > 127 {
		exitCode = 127
	}

	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--exit-code" {
			var parsed int
			if _, err := fmt.Sscanf(args[i+1], "%d", &parsed); err == nil {
				if parsed < 0 {
					parsed = 0
				}
				if parsed > 255 {
					parsed = 255
				}
				exitCode = parsed
				break
			}
		}
	}

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

	fmt.Printf("exit_code: %d\n", exitCode)
	os.Exit(exitCode)
}
