package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// version is injected at build time via goreleaser ldflags or falls back to
// runtime/debug.ReadBuildInfo when built with go install.
var version = "dev"

// httpClient is used for all outbound HTTP requests (releases metadata + SDK downloads).
var httpClient = &http.Client{Timeout: 5 * time.Minute}

// osExit is overridable for testing. Production code should never call os.Exit directly.
var osExit = os.Exit

func init() {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		version = info.Main.Version
	}
}

// Config holds all parsed CLI flags and derived state for the extraction workflow.
type Config struct {
	version        string
	file           string
	os             string
	arch           string
	target         string
	download       bool
	releaseVersion string
	runtimeType    string
	showVersion    bool
}

// main is the entry point. It either downloads + extracts from a remote SDK archive
// or extracts from a local archive file, depending on which flags were set.
func main() {
	cfg := parseFlags()

	if cfg.showVersion {
		fmt.Printf("dotnet-runtime-extractor %s\n", version)
		return
	}

	if cfg.download {
		if err := downloadAndExtract(cfg); err != nil {
			slog.Error("Failed to download and extract", "error", err)
			osExit(1)
		}
	} else {
		if err := extractFromArchive(cfg); err != nil {
			slog.Error("Failed to extract from archive", "error", err)
			osExit(1)
		}
	}
}

// parseFlags reads and validates CLI flags, returning a Config.
// On validation failure or conflicting flags it prints usage and exits.
func parseFlags() *Config {
	showVersion := flag.Bool("version", false, "Show version information")
	releaseVersion := flag.String("release-version", "", ".NET release version to download (e.g., 10, 10.0, or 10.0.5)")
	file := flag.String("file", "", "Path to local SDK archive file")
	runtimeType := flag.String("runtime", "none", "Runtime type to extract [none, aspnet, desktop, all]")
	targetOS := flag.String("os", "win", "Target platform [win, linux, osx]")
	arch := flag.String("arch", "x64", "Target architecture [x86, x64, arm, arm64]")
	target := flag.String("target", "runtime", "Extraction target directory")

	flag.Parse()

	if *showVersion {
		return &Config{showVersion: true}
	}

	if (*releaseVersion == "" && *file == "") || (*releaseVersion != "" && *file != "") {
		slog.Error("Either --release-version or --file must be provided, not both")
		flag.Usage()
		osExit(1)
	}

	validRuntimes := map[string]bool{"none": true, "aspnet": true, "desktop": true, "all": true}
	if !validRuntimes[*runtimeType] {
		slog.Error("Invalid --runtime value, must be none, aspnet, desktop, or all")
		flag.Usage()
		osExit(1)
	}

	validOS := map[string]bool{"win": true, "linux": true, "osx": true}
	if !validOS[*targetOS] {
		slog.Error("Invalid --os value, must be win, linux, or osx")
		flag.Usage()
		osExit(1)
	}

	validArch := map[string]bool{"x86": true, "x64": true, "arm": true, "arm64": true}
	if !validArch[*arch] {
		slog.Error("Invalid --arch value, must be x86, x64, arm, or arm64")
		flag.Usage()
		osExit(1)
	}

	cfg := &Config{
		version:        *releaseVersion,
		file:           *file,
		os:             *targetOS,
		arch:           *arch,
		target:         *target,
		download:       *releaseVersion != "",
		releaseVersion: *releaseVersion,
		runtimeType:    *runtimeType,
	}

	if cfg.os != "win" && (cfg.runtimeType == "desktop" || cfg.runtimeType == "all") {
		if cfg.runtimeType == "desktop" {
			slog.Error("Desktop runtime is not available on Linux/OSX")
			flag.Usage()
			osExit(1)
		}
		slog.Warn("Desktop runtime is not available on Linux/OSX, only ASP.NET Core will be extracted")
		cfg.runtimeType = "aspnet"
	}

	slog.Info("Configuration", "release-version", cfg.version, "file", cfg.file, "os", cfg.os, "arch", cfg.arch, "target", cfg.target, "runtime", cfg.runtimeType)

	return cfg
}

// downloadAndExtract resolves the download URL, fetches the SDK archive, then extracts it.
func downloadAndExtract(cfg *Config) error {
	url, version, fileHash, err := resolveDownloadURL(cfg.version, cfg.os, cfg.arch, cfg.releaseVersion)
	if err != nil {
		return fmt.Errorf("failed to resolve download URL: %w", err)
	}

	slog.Info("Downloading .NET SDK", "url", url)

	zipPath, err := downloadToTarget(url, version, fileHash, cfg.target, cfg.os)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	cfg.file = zipPath

	return extractFromArchive(cfg)
}

// resolveVersionBand maps user input to a version band used for releases.json lookup:
//
//	"10"    → "10.0"
//	"10.0"  → "10.0"
//	"10.0.5" → "10.0"
func resolveVersionBand(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) == 1 {
		return version + ".0"
	}
	if len(parts) == 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}

// resolveDownloadURL fetches the .NET release metadata for the given version band and
// finds the SDK download URL matching the target OS and architecture.
// Supports fuzzy versioning (e.g. "10" → "10.0.5") and exact version pinning.
func resolveDownloadURL(version, targetOS, arch, releaseVersion string) (string, string, string, error) {
	parts := strings.Split(version, ".")
	versionBand := resolveVersionBand(version)

	releasesURL := fmt.Sprintf("https://builds.dotnet.microsoft.com/dotnet/release-metadata/%s/releases.json", versionBand)

	slog.Info("Fetching releases.json", "url", releasesURL)

	data, err := fetchURL(releasesURL)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to fetch releases.json: %w", err)
	}

	latestRelease := gjson.Get(data, "latest-release").String()
	if latestRelease == "" {
		return "", "", "", fmt.Errorf("could not find latest-release in releases.json")
	}

	targetReleaseVersion := releaseVersion

	// Fuzzy matching: if user gave fewer than 3 version parts or the patch doesn't match latest, resolve to latest
	isFuzzy := len(parts) < 3 || (len(parts) >= 3 && parts[2] != strings.Split(latestRelease, ".")[2])
	if targetReleaseVersion == "" || isFuzzy {
		targetReleaseVersion = latestRelease
		slog.Info("Using fuzzy version, resolved to latest", "latest", latestRelease)
	}

	slog.Info("Target release version", "version", targetReleaseVersion)

	rid := fmt.Sprintf("%s-%s", targetOS, arch)

	downloadURL, actualVersion, fileHash := findSDKByRID(data, targetReleaseVersion, targetOS, rid)
	if downloadURL == "" {
		return "", "", "", fmt.Errorf("could not find SDK for RID %s", rid)
	}

	slog.Info("Resolved download URL", "url", downloadURL)
	return downloadURL, actualVersion, fileHash, nil
}

// findSDKByRID searches the releases JSON for the matching release-version and RID,
// returning the download URL, the actual SDK version string, and the file hash.
func findSDKByRID(data, releaseVersion, targetOS, rid string) (string, string, string) {
	fileExt := archiveExt(targetOS)

	releases := gjson.Get(data, "releases").Array()
	for _, release := range releases {
		if release.Get("release-version").String() == releaseVersion {
			sdkFiles := release.Get("sdk.files").Array()
			for _, f := range sdkFiles {
				url := f.Get("url").String()
				if f.Get("rid").String() == rid && strings.HasSuffix(url, fileExt) {
					hash := f.Get("hash-sha256").String()
					if hash == "" {
						hash = f.Get("hash").String()
					}
					idx := strings.LastIndex(url, "/dotnet-sdk-")
					if idx != -1 {
						name := url[idx+len("/dotnet-sdk-"):]
						version := strings.TrimSuffix(name, fileExt)
						return url, version, hash
					}
				}
			}
		}
	}
	return "", "", ""
}

// fetchURL is a simple helper that GETs a URL and returns the response body as a string.
func fetchURL(url string) (string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// downloadToTarget downloads the SDK archive to the target directory.
// It verifies the cached file's SHA512 hash against the expected metadata hash to skip
// redundant downloads when possible.
func downloadToTarget(url, version, expectedHash, target, targetOS string) (string, error) {
	if err := os.MkdirAll(target, 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory: %w", err)
	}

	fileExt := archiveExt(targetOS)

	filename := fmt.Sprintf("dotnet-sdk-%s%s", version, fileExt)
	archivePath := filepath.Join(target, filename)

	if _, err := os.Stat(archivePath); err == nil {
		slog.Info("Local file found, computing hash", "path", archivePath)
		localHash, err := computeFileHash(archivePath)
		if err != nil {
			return "", fmt.Errorf("failed to compute local file hash: %w", err)
		}

		if strings.EqualFold(localHash, expectedHash) {
			slog.Info("Local file hash matches, skipping download", "hash", localHash)
			return archivePath, nil
		}

		slog.Info("Local file hash mismatch, re-downloading", "local", localHash, "expected", expectedHash)
		if err := os.Remove(archivePath); err != nil {
			return "", fmt.Errorf("failed to remove outdated file: %w", err)
		}
	}

	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	out, err := os.Create(archivePath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", err
	}

	slog.Info("Downloaded SDK archive", "path", archivePath)
	return archivePath, nil
}

// computeFileHash computes the SHA512 hash of a file and returns it as a hex string.
// This is used to validate locally cached SDK archives against the metadata hash from
// the Microsoft release API.
func computeFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha512.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// isSafePath checks that the given name does not escape the target directory via ".."
// segments or absolute paths.
func isSafePath(name string) bool {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	return !filepath.IsAbs(cleaned) &&
		!strings.HasPrefix(cleaned, "..") &&
		!strings.HasPrefix(cleaned, string(filepath.Separator))
}

// archiveExt returns the file extension used by the .NET SDK archive for the given OS.
func archiveExt(targetOS string) string {
	if targetOS == "win" {
		return ".zip"
	}
	return ".tar.gz"
}

// shouldExtractShared determines whether a file under the shared/ directory should be
// extracted based on its runtime framework name and the configured runtime type.
// Microsoft.NETCore.App is always included; AspNetCore.App and WindowsDesktop.App
// are included only when the corresponding runtime type is selected.
func shouldExtractShared(name, runtimeType string) bool {
	if strings.Contains(name, "Microsoft.NETCore.App/") {
		return true
	}
	if (runtimeType == "aspnet" || runtimeType == "all") && strings.Contains(name, "Microsoft.AspNetCore.App/") {
		return true
	}
	if (runtimeType == "desktop" || runtimeType == "all") && strings.Contains(name, "Microsoft.WindowsDesktop.App/") {
		return true
	}
	return false
}

// extractFromArchive opens the SDK archive (zip or tar.gz) and extracts the selected
// runtime components (shared frameworks, host/fxr, and dotnet binary) into the target
// directory. Selection logic is governed by cfg.runtimeType.
func extractFromArchive(cfg *Config) error {
	slog.Info("Extracting from archive", "file", cfg.file, "target", cfg.target, "runtime", cfg.runtimeType)

	if strings.HasSuffix(cfg.file, ".zip") {
		r, err := zip.OpenReader(cfg.file)
		if err != nil {
			return fmt.Errorf("failed to open zip file: %w", err)
		}
		defer r.Close()

		for _, f := range r.File {
			name := f.Name
			if strings.HasSuffix(name, "/") {
				continue
			}

			archiveName := strings.TrimPrefix(name, "./")

			var targetName string
			switch {
			case strings.HasPrefix(archiveName, "shared/"):
				if shouldExtractShared(archiveName, cfg.runtimeType) {
					targetName = archiveName
				}
			case strings.HasPrefix(archiveName, "host/fxr/"):
				targetName = archiveName
			case strings.HasPrefix(archiveName, "dotnet") || strings.HasSuffix(archiveName, "/dotnet"):
				targetName = filepath.Base(archiveName)
			}

			if targetName != "" && isSafePath(targetName) {
				targetPath := filepath.Join(cfg.target, targetName)
				if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
					return fmt.Errorf("failed to create directory for %s: %w", targetName, err)
				}
				slog.Info("Extracting", "from", name, "to", targetPath)
				if err := extractZipEntry(f, targetPath); err != nil {
					return fmt.Errorf("failed to extract %s: %w", name, err)
				}
			}
		}
	} else if strings.HasSuffix(cfg.file, ".tar.gz") {
		file, err := os.Open(cfg.file)
		if err != nil {
			return fmt.Errorf("failed to open tar.gz file: %w", err)
		}
		defer file.Close()

		gr, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gr.Close()

		tr := tar.NewReader(gr)

		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("failed to read tar header: %w", err)
			}

			name := header.Name
			if header.Typeflag == tar.TypeDir {
				continue
			}

			var targetName string
			archiveName := strings.TrimPrefix(name, "./")

			switch {
			case strings.HasPrefix(archiveName, "shared/"):
				if shouldExtractShared(archiveName, cfg.runtimeType) {
					targetName = archiveName
				}
			case strings.HasPrefix(archiveName, "host/fxr/"):
				targetName = archiveName
			case strings.HasPrefix(archiveName, "dotnet"):
				targetName = filepath.Base(archiveName)
			}

			if targetName != "" && isSafePath(targetName) {
				targetPath := filepath.Join(cfg.target, targetName)
				if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
					return fmt.Errorf("failed to create directory for %s: %w", targetName, err)
				}
				slog.Info("Extracting", "from", name, "to", targetPath)

				// Handle symlink entries (common in Linux/OSX SDK archives)
				if header.Typeflag == tar.TypeSymlink {
					if err := os.Symlink(header.Linkname, targetPath); err != nil {
						return fmt.Errorf("failed to create symlink %s: %w", targetName, err)
					}
					continue
				}

				dst, err := os.Create(targetPath)
				if err != nil {
					return fmt.Errorf("failed to create file %s: %w", targetName, err)
				}

				if _, err := io.Copy(dst, tr); err != nil {
					dst.Close()
					return fmt.Errorf("failed to extract %s: %w", name, err)
				}

				dst.Close()
			}
		}
	} else {
		return fmt.Errorf("unsupported archive format: %s", cfg.file)
	}

	slog.Info("Extraction completed successfully", "target", cfg.target)
	return nil
}

// extractZipEntry extracts a single file from a zip archive to the given destination path.
func extractZipEntry(f *zip.File, dest string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}
