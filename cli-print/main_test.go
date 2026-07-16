package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestQuoteString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: "<empty>"},
		{input: "plain", want: `"plain"`},
		{input: `he"llo`, want: `"he\"llo"`},
		{input: `a\b`, want: `"a\\b"`},
		{input: "line\nbreak", want: `"line\nbreak"`},
		{input: "carriage\rreturn", want: `"carriage\rreturn"`},
		{input: "tab\tchar", want: `"tab\tchar"`},
		{input: "multi\nline\ttext\rhere", want: `"multi\nline\ttext\rhere"`},
		{input: "spaces", want: `"spaces"`},
		{input: "unicode→✓", want: `"unicode→✓"`},
		// only a single special character still gets quoted
		{input: "\n", want: `"\n"`},
		{input: `\`, want: `"\\"`},
	}
	for _, tt := range tests {
		got := quoteString(tt.input)
		if got != tt.want {
			t.Errorf("quoteString(%q) = %s; want %s", tt.input, got, tt.want)
		}
	}
}

func TestQuoteStringAllSpecial(t *testing.T) {
	// All ASCII special characters in one string.
	input := "'\"\\\n\r\t\x00\x01"
	got := quoteString(input)
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Errorf("quoteString should wrap output in double quotes; got %s", got)
	}
	// Verify escaping: the core characters we handle.
	if !strings.Contains(got, `\"`) {
		t.Errorf("double quote should be escaped; got %s", got)
	}
	if !strings.Contains(got, `\\`) {
		t.Errorf("backslash should be escaped; got %s", got)
	}
	if !strings.Contains(got, `\n`) {
		t.Errorf("newline should be escaped; got %s", got)
	}
}

func TestQuoteStringUnicode(t *testing.T) {
	// Ensure CJK and emoji pass through unmodified inside the quotes.
	input := "你好，世界！🚀"
	got := quoteString(input)
	want := `"你好，世界！🚀"`
	if got != want {
		t.Errorf("quoteString(%q) = %s; want %s", input, got, want)
	}
}

// runOut runs cmd and returns its combined output, treating a non-zero exit
// (expected for cli-print when args are given) as normal.
func runOut(cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.CombinedOutput()
	if err != nil {
		// cli-print exits with arg count, so non-zero is expected with args.
		// Still return the output for inspection.
		return out, err
	}
	return out, nil
}

// TestMainOutput builds the binary and exercises the full main() pipeline.
func TestMainOutput(t *testing.T) {
	exe, err := buildSelf(t)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	t.Run("prints binary path and cwd", func(t *testing.T) {
		out, err := runOut(exec.Command(exe))
		if err != nil {
			t.Fatalf("exec: %v\n%s", err, out)
		}
		lines := strings.Split(string(out), "\n")
		if len(lines) < 2 {
			t.Fatalf("expected >=2 output lines, got %d", len(lines))
		}
		if !strings.HasPrefix(lines[0], "cli-print: ") {
			t.Errorf("line 0:\n got: %q\nwant prefix: %q", lines[0], "cli-print: ")
		}
		if !strings.HasPrefix(lines[1], "cwd: ") {
			t.Errorf("line 1:\n got: %q\nwant prefix: %q", lines[1], "cwd: ")
		}
	})

	t.Run("prints argv", func(t *testing.T) {
		out, _ := runOut(exec.Command(exe, "hello", "world"))
		if !strings.Contains(string(out), `argv[0]: "hello"`) {
			t.Errorf("missing argv[0]; output:\n%s", out)
		}
		if !strings.Contains(string(out), `argv[1]: "world"`) {
			t.Errorf("missing argv[1]; output:\n%s", out)
		}
	})

	t.Run("argv count equals argc", func(t *testing.T) {
		out, _ := runOut(exec.Command(exe, "a", "b", "c"))
		if !strings.Contains(string(out), "argc: 3") {
			t.Errorf("expected argc: 3; output:\n%s", out)
		}
		// Verify exactly three argv lines.
		lines := strings.Split(string(out), "\n")
		var argvLines int
		for _, l := range lines {
			if strings.HasPrefix(l, "argv[") {
				argvLines++
			}
		}
		if argvLines != 3 {
			t.Errorf("expected 3 argv lines, got %d", argvLines)
		}
	})

	t.Run("empty args still prints header", func(t *testing.T) {
		out, err := runOut(exec.Command(exe))
		if err != nil {
			t.Fatalf("exec: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "argc: 0") {
			t.Errorf("expected argc: 0; output:\n%s", out)
		}
	})

	t.Run("prints env block", func(t *testing.T) {
		out, err := runOut(exec.Command(exe))
		if err != nil {
			t.Fatalf("exec: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "env: ") {
			t.Errorf("missing env line; output:\n%s", out)
		}
		if !strings.Contains(string(out), "env:PATH: ") {
			t.Errorf("missing env:PATH; output:\n%s", out)
		}
	})

	t.Run("prints exit code line", func(t *testing.T) {
		out, err := runOut(exec.Command(exe))
		if err != nil {
			t.Fatalf("exec: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "exit_code: ") {
			t.Errorf("missing exit_code line; output:\n%s", out)
		}
	})

	t.Run("default exit code equals arg count", func(t *testing.T) {
		out, _ := runOut(exec.Command(exe, "x", "y"))
		if !strings.Contains(string(out), "exit_code: 2") {
			t.Errorf("expected exit_code: 2; output:\n%s", out)
		}
	})

	t.Run("zero args gives exit code 0", func(t *testing.T) {
		out, err := runOut(exec.Command(exe))
		if err != nil {
			t.Fatalf("exec: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "exit_code: 0") {
			t.Errorf("expected exit_code: 0; output:\n%s", out)
		}
	})

	t.Run("--exit-code override", func(t *testing.T) {
		out, _ := runOut(exec.Command(exe, "--exit-code", "42"))
		if !strings.Contains(string(out), "exit_code: 42") {
			t.Errorf("expected exit_code: 42; output:\n%s", out)
		}
	})

	t.Run("--exit-code clamps to 0-255", func(t *testing.T) {
		out, _ := runOut(exec.Command(exe, "--exit-code", "999"))
		if !strings.Contains(string(out), "exit_code: 255") {
			t.Errorf("expected exit_code: 255; output:\n%s", out)
		}
	})

	t.Run("--exit-code negative clamps to 0", func(t *testing.T) {
		out, _ := runOut(exec.Command(exe, "--exit-code", "-5"))
		if !strings.Contains(string(out), "exit_code: 0") {
			t.Errorf("expected exit_code: 0; output:\n%s", out)
		}
	})

	t.Run("non-numeric --exit-code is ignored", func(t *testing.T) {
		out, _ := runOut(exec.Command(exe, "--exit-code", "abc", "x"))
		// args = ["--exit-code", "abc", "x"] → 3 args → exit_code 3 (before clamp)
		if !strings.Contains(string(out), "exit_code: 3") {
			t.Errorf("expected exit_code: 3; output:\n%s", out)
		}
	})

	t.Run("exit code is actual exit status", func(t *testing.T) {
		cmd := exec.Command(exe, "--exit-code", "42")
		err := cmd.Run()
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 42 {
				t.Errorf("process exited with code %d; want 42", exitErr.ExitCode())
			}
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("output format is stable", func(t *testing.T) {
		out, _ := runOut(exec.Command(exe, "a"))
		lines := strings.Split(string(out), "\n")
		// Last non-empty line is exit_code.
		var lastLine string
		for _, l := range lines {
			if l != "" {
				lastLine = l
			}
		}
		if lastLine != "exit_code: 1" {
			t.Errorf("last non-empty line should be exit_code: 1; got %q", lastLine)
		}
	})
}

// buildSelf compiles the current package and returns the binary path.
func buildSelf(t *testing.T) (string, error) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "cli-print"+exeSuffix())
	out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, out)
	}
	return exe, nil
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
