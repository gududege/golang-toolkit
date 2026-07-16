package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestShouldExtractShared(t *testing.T) {
	tests := []struct {
		name        string
		runtimeType string
		expected    bool
	}{
		{"shared/Microsoft.NETCore.App/10.0.5/", "none", true},
		{"shared/Microsoft.NETCore.App/10.0.5/", "aspnet", true},
		{"shared/Microsoft.NETCore.App/10.0.5/", "desktop", true},
		{"shared/Microsoft.NETCore.App/10.0.5/", "all", true},
		{"shared/Microsoft.AspNetCore.App/10.0.5/", "none", false},
		{"shared/Microsoft.AspNetCore.App/10.0.5/", "aspnet", true},
		{"shared/Microsoft.AspNetCore.App/10.0.5/", "desktop", false},
		{"shared/Microsoft.AspNetCore.App/10.0.5/", "all", true},
		{"shared/Microsoft.WindowsDesktop.App/10.0.5/", "none", false},
		{"shared/Microsoft.WindowsDesktop.App/10.0.5/", "aspnet", false},
		{"shared/Microsoft.WindowsDesktop.App/10.0.5/", "desktop", true},
		{"shared/Microsoft.WindowsDesktop.App/10.0.5/", "all", true},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_"+tt.runtimeType, func(t *testing.T) {
			result := shouldExtractShared(tt.name, tt.runtimeType)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestResolveVersionBand(t *testing.T) {
	tests := []struct {
		version     string
		expected    string
		description string
	}{
		{"10", "10.0", "single digit version"},
		{"10.0", "10.0", "two digit version"},
		{"10.0.5", "10.0", "three digit version"},
		{"10.0.201", "10.0", "SDK version"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := resolveVersionBand(tt.version)
			if result != tt.expected {
				t.Errorf("resolveVersionBand(%q) = %q, want %q", tt.version, result, tt.expected)
			}
		})
	}
}

func TestParseFlags_Success(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	tests := []struct {
		name            string
		args            []string
		expectedOS      string
		expectedArch    string
		expectedRuntime string
	}{
		{
			name:            "Default values",
			args:            []string{"cmd", "-file", "test.zip"},
			expectedOS:      "win",
			expectedArch:    "x64",
			expectedRuntime: "none",
		},
		{
			name:            "Custom values",
			args:            []string{"cmd", "-file", "test.zip", "-os", "linux", "-arch", "arm64", "-runtime", "aspnet"},
			expectedOS:      "linux",
			expectedArch:    "arm64",
			expectedRuntime: "aspnet",
		},
		{
			name:            "Linux all converts to aspnet",
			args:            []string{"cmd", "-file", "test.zip", "-os", "linux", "-runtime", "all"},
			expectedOS:      "linux",
			expectedArch:    "x64",
			expectedRuntime: "aspnet",
		},
		{
			name:            "OSX all converts to aspnet",
			args:            []string{"cmd", "-file", "test.zip", "-os", "osx", "-runtime", "all"},
			expectedOS:      "osx",
			expectedArch:    "x64",
			expectedRuntime: "aspnet",
		},
		{
			name:            "Win desktop allowed",
			args:            []string{"cmd", "-file", "test.zip", "-os", "win", "-runtime", "desktop"},
			expectedOS:      "win",
			expectedArch:    "x64",
			expectedRuntime: "desktop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Args = tt.args
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			cfg := parseFlags()

			if cfg.os != tt.expectedOS {
				t.Errorf("expected os %s, got %s", tt.expectedOS, cfg.os)
			}
			if cfg.arch != tt.expectedArch {
				t.Errorf("expected arch %s, got %s", tt.expectedArch, cfg.arch)
			}
			if cfg.runtimeType != tt.expectedRuntime {
				t.Errorf("expected runtimeType %s, got %s", tt.expectedRuntime, cfg.runtimeType)
			}
		})
	}
}

func TestIsSafePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"simple path", "shared/Microsoft.NETCore.App/10.0.5", true},
		{"nested dirs", "host/fxr/10.0.5/hostfxr.dll", true},
		{"dotnet binary", "dotnet.exe", true},
		{"single name", "dotnet", true},
		{"traversal not allowed", "../etc/passwd", false},
		{"traversal in middle", "shared/../../etc/passwd", false},
		{"traversal via clean", "safe/../danger/foo", true},
		{"absolute path unix", "/etc/passwd", false},
		{"absolute win drive", "C:/Windows/system32", false},
		{"mixed traversal like", "foo/..bar/baz", true},
		{"dot dot with prefix", "safe/..danger/foo", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSafePath(tt.path)
			if result != tt.expected {
				t.Errorf("isSafePath(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestArchiveExt(t *testing.T) {
	if ext := archiveExt("win"); ext != ".zip" {
		t.Errorf("archiveExt(win) = %q, want .zip", ext)
	}
	if ext := archiveExt("linux"); ext != ".tar.gz" {
		t.Errorf("archiveExt(linux) = %q, want .tar.gz", ext)
	}
	if ext := archiveExt("osx"); ext != ".tar.gz" {
		t.Errorf("archiveExt(osx) = %q, want .tar.gz", ext)
	}
}

func TestComputeFileHash(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.bin")
	content := []byte("hello world")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	hash, err := computeFileHash(path)
	if err != nil {
		t.Fatalf("computeFileHash failed: %v", err)
	}

	if len(hash) != 128 {
		t.Errorf("expected SHA512 hex length 128, got %d", len(hash))
	}

	if _, err := computeFileHash("nonexistent"); err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestExtractZipEntry(t *testing.T) {
	tmpDir := t.TempDir()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	content := []byte("test content")
	f, err := w.Create("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(tmpDir, "extracted.txt")
	if err := extractZipEntry(r.File[0], dest); err != nil {
		t.Fatalf("extractZipEntry failed: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", string(got), string(content))
	}

	nestedDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := extractZipEntry(r.File[0], filepath.Join(nestedDir, "nested.txt")); err != nil {
		t.Errorf("expected success for nested dir, got: %v", err)
	}
}

func TestParseFlags_ErrorPaths(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		exitCode int
	}{
		{"missing both flags", []string{"cmd"}, 1},
		{"both flags provided", []string{"cmd", "-release-version", "10", "-file", "test.zip"}, 1},
		{"invalid runtime", []string{"cmd", "-file", "test.zip", "-runtime", "invalid"}, 1},
		{"invalid os", []string{"cmd", "-file", "test.zip", "-os", "solaris"}, 1},
		{"invalid arch", []string{"cmd", "-file", "test.zip", "-arch", "riscv"}, 1},
		{"desktop on linux", []string{"cmd", "-file", "test.zip", "-os", "linux", "-runtime", "desktop"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldArgs := os.Args
			oldOsExit := osExit
			defer func() { os.Args = oldArgs; osExit = oldOsExit }()

			os.Args = tt.args
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			exitCalled := false
			osExit = func(code int) {
				exitCalled = true
				if code != tt.exitCode {
					t.Errorf("expected exit code %d, got %d", tt.exitCode, code)
				}
			}

			parseFlags()

			if !exitCalled {
				t.Error("expected os.Exit to be called but it wasn't")
			}
		})
	}
}

func TestParseFlags_Version(t *testing.T) {
	oldArgs := os.Args
	oldOsExit := osExit
	defer func() { os.Args = oldArgs; osExit = oldOsExit }()

	os.Args = []string{"cmd", "-version"}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	osExit = func(code int) { panic("should not exit") }

	cfg := parseFlags()
	if cfg == nil || !cfg.showVersion {
		t.Error("expected showVersion to be true")
	}
}

func TestExtractFromArchive_TarGz(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a tar.gz file mimicking a Linux .NET SDK archive
	tarGzPath := filepath.Join(tmpDir, "dotnet-sdk-test.tar.gz")
	f, err := os.Create(tarGzPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	entries := []struct {
		name     string
		content  string
		isSymlnk bool
		linkname string
	}{
		{"shared/Microsoft.NETCore.App/10.0.5/System.Console.dll", "console", false, ""},
		{"shared/Microsoft.AspNetCore.App/10.0.5/Microsoft.AspNetCore.dll", "aspnet", false, ""},
		{"shared/Microsoft.WindowsDesktop.App/10.0.5/PresentationFramework.dll", "desktop", false, ""},
		{"host/fxr/10.0.5/hostfxr.dll", "hostfxr", false, ""},
		{"dotnet", "dotnet binary", false, ""},
		{"sdk/10.0.5/Sdk.props", "sdk data", false, ""},
	}

	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Size:     int64(len(e.content)),
			Mode:     0755,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	// Test extraction with "all" — should exclude SDK files
	outDir := filepath.Join(tmpDir, "out")
	cfg := &Config{
		file:        tarGzPath,
		target:      outDir,
		runtimeType: "all",
		os:          "linux",
	}
	if err := extractFromArchive(cfg); err != nil {
		t.Fatalf("extractFromArchive failed: %v", err)
	}

	checkFile(t, outDir, "dotnet")
	checkFile(t, outDir, "shared/Microsoft.NETCore.App/10.0.5/System.Console.dll")
	checkFile(t, outDir, "shared/Microsoft.AspNetCore.App/10.0.5/Microsoft.AspNetCore.dll")
	checkFile(t, outDir, "shared/Microsoft.WindowsDesktop.App/10.0.5/PresentationFramework.dll")
	checkFile(t, outDir, "host/fxr/10.0.5/hostfxr.dll")
	noFile(t, outDir, "sdk/10.0.5/Sdk.props")
}

func checkFile(t *testing.T, base, rel string) {
	t.Helper()
	path := filepath.Join(base, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", rel, err)
	}
}

func noFile(t *testing.T, base, rel string) {
	t.Helper()
	path := filepath.Join(base, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected %s to NOT exist, but it does", rel)
	}
}
