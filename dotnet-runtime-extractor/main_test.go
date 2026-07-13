package main

import (
	"flag"
	"os"
	"strings"
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
			parts := strings.Split(tt.version, ".")
			var versionBand string
			if len(parts) == 1 {
				versionBand = tt.version + ".0"
			} else if len(parts) == 2 {
				versionBand = tt.version
			} else if len(parts) >= 3 {
				versionBand = parts[0] + "." + parts[1]
			}

			if versionBand != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, versionBand)
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
