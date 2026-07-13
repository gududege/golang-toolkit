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
	"strings"

	"github.com/tidwall/gjson"
)

var version string

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

func main() {
	cfg := parseFlags()

	if cfg.showVersion {
		fmt.Printf("dotnet-runtime-extractor %s\n", version)
		return
	}

	if cfg.download {
		if err := downloadAndExtract(cfg); err != nil {
			slog.Error("Failed to download and extract", "error", err)
			os.Exit(1)
		}
	} else {
		if err := extractFromArchive(cfg); err != nil {
			slog.Error("Failed to extract from archive", "error", err)
			os.Exit(1)
		}
	}
}

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
		os.Exit(1)
	}

	validRuntimes := map[string]bool{"none": true, "aspnet": true, "desktop": true, "all": true}
	if !validRuntimes[*runtimeType] {
		slog.Error("Invalid --runtime value, must be none, aspnet, desktop, or all")
		flag.Usage()
		os.Exit(1)
	}

	validOS := map[string]bool{"win": true, "linux": true, "osx": true}
	if !validOS[*targetOS] {
		slog.Error("Invalid --os value, must be win, linux, or osx")
		flag.Usage()
		os.Exit(1)
	}

	validArch := map[string]bool{"x86": true, "x64": true, "arm": true, "arm64": true}
	if !validArch[*arch] {
		slog.Error("Invalid --arch value, must be x86, x64, arm, or arm64")
		flag.Usage()
		os.Exit(1)
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
			os.Exit(1)
		}
		slog.Warn("Desktop runtime is not available on Linux/OSX, only ASP.NET Core will be extracted")
		cfg.runtimeType = "aspnet"
	}

	slog.Info("Configuration", "release-version", cfg.version, "file", cfg.file, "os", cfg.os, "arch", cfg.arch, "target", cfg.target, "runtime", cfg.runtimeType)

	return cfg
}

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

func resolveDownloadURL(version, targetOS, arch, releaseVersion string) (string, string, string, error) {
	versionBand := version

	parts := strings.Split(version, ".")
	if len(parts) == 1 {
		versionBand = version + ".0"
	} else if len(parts) == 2 {
		versionBand = version
	} else if len(parts) >= 3 {
		versionBand = parts[0] + "." + parts[1]
	}

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

func findSDKByRID(data, releaseVersion, targetOS, rid string) (string, string, string) {
	var fileExt string
	if targetOS == "win" {
		fileExt = ".zip"
	} else {
		fileExt = ".tar.gz"
	}

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

func fetchURL(url string) (string, error) {
	resp, err := http.Get(url)
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

func downloadToTarget(url, version, expectedHash, target, targetOS string) (string, error) {
	if err := os.MkdirAll(target, 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory: %w", err)
	}

	var fileExt string
	if targetOS == "win" {
		fileExt = ".zip"
	} else {
		fileExt = ".tar.gz"
	}

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

	resp, err := http.Get(url)
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

func extractFromArchive(cfg *Config) error {
	slog.Info("Extracting from archive", "file", cfg.file, "target", cfg.target, "runtime", cfg.runtimeType)

	extractedFiles := make(map[string]string)

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

			if targetName != "" && !strings.Contains(targetName, "..") {
				extractedFiles[targetName] = name
			}
		}

		for targetName, zipName := range extractedFiles {
			targetPath := filepath.Join(cfg.target, targetName)
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("failed to create directory for %s: %w", targetName, err)
			}
			slog.Info("Extracting", "from", zipName, "to", targetPath)
			if err := extractZipFile(r, zipName, targetPath); err != nil {
				return fmt.Errorf("failed to extract %s: %w", zipName, err)
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

			if targetName != "" && !strings.Contains(targetName, "..") {
				targetPath := filepath.Join(cfg.target, targetName)
				if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
					return fmt.Errorf("failed to create directory for %s: %w", targetName, err)
				}
				slog.Info("Extracting", "from", name, "to", targetPath)

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
				defer dst.Close()

				if _, err := io.Copy(dst, tr); err != nil {
					return fmt.Errorf("failed to extract %s: %w", name, err)
				}
			}
		}
	} else {
		return fmt.Errorf("unsupported archive format: %s", cfg.file)
	}

	slog.Info("Extraction completed successfully", "target", cfg.target)
	return nil
}

func extractZipFile(r *zip.ReadCloser, name, dest string) error {
	for _, f := range r.File {
		if f.Name == name {
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
	}
	return fmt.Errorf("file not found in archive: %s", name)
}

func extractTarFile(tr *tar.Reader, name, dest string) error {
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if header.Name == name {
			if header.Typeflag == tar.TypeSymlink {
				linkDest := header.Linkname
				return os.Symlink(linkDest, dest)
			}

			_, err := os.Stat(dest)
			if err == nil {
				return nil
			}

			dst, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer dst.Close()

			_, err = io.Copy(dst, tr)
			return err
		}
	}
	return fmt.Errorf("file not found in archive: %s", name)
}
