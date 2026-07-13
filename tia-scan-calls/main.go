package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/pflag"
)

type blockRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type blockInfo struct {
	Name string     `json:"name"`
	Type string     `json:"type"`
	Use  []blockRef `json:"use"`
}

var version string

var (
	reBlockOB    = regexp.MustCompile(`^ORGANIZATION_BLOCK\s+"`)
	reBlockFB    = regexp.MustCompile(`^FUNCTION_BLOCK\s+"`)
	reBlockFC    = regexp.MustCompile(`^FUNCTION\s+"`)
	reBlockDB    = regexp.MustCompile(`^DATA_BLOCK\s+"`)
	reBlockUDT   = regexp.MustCompile(`^TYPE\s+"`)
	reVAR        = regexp.MustCompile(`\bVAR\b`)
	reEND_VAR    = regexp.MustCompile(`\bEND_VAR\b`)
	reSTRUCT     = regexp.MustCompile(`\bSTRUCT\b`)
	reEND_STRUCT = regexp.MustCompile(`\bEND_STRUCT\b`)
)

func detectBlockType(content string) string {
	content = strings.TrimLeft(content, "\uFEFF")
	firstLine := content
	if idx := strings.IndexAny(content, "\r\n"); idx >= 0 {
		firstLine = content[:idx]
	}

	switch {
	case reBlockOB.MatchString(firstLine):
		return "OB"
	case reBlockFB.MatchString(firstLine):
		return "FB"
	case reBlockFC.MatchString(firstLine):
		return "FC"
	case reBlockDB.MatchString(firstLine):
		hasVar := reVAR.MatchString(content) && reEND_VAR.MatchString(content)
		hasStruct := reSTRUCT.MatchString(content) && reEND_STRUCT.MatchString(content)
		if !hasVar && !hasStruct {
			return "DI"
		}
		return "DB"
	case reBlockUDT.MatchString(firstLine):
		return "UDT"
	default:
		return "UNKNOWN"
	}
}

func walkFiles(paths []string) ([]string, error) {
	// intentionally nil — append works fine on nil, and this is never serialized
	var files []string
	for _, root := range paths {
		err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return fmt.Errorf("access %q: %w", path, err)
			}
			if !fi.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("error scanning directory %q: %w", root, err)
		}
	}
	return files, nil
}

func collectBlockNames(files []string) []string {
	blockNames := make([]string, 0, len(files))
	for _, f := range files {
		blockNames = append(blockNames, strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
	}
	return blockNames
}

func buildRefRegex(blockNames []string) *regexp.Regexp {
	escaped := make([]string, len(blockNames))
	for i, name := range blockNames {
		escaped[i] = regexp.QuoteMeta(name)
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(escaped, "|") + `)\b`)
}

func resolveRefType(files []string, match string) string {
	for _, f := range files {
		if strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)) != match {
			continue
		}
		refContent, err := os.ReadFile(f)
		if err != nil {
			return "UNKNOWN"
		}
		return detectBlockType(string(refContent))
	}
	return "UNKNOWN"
}

func scanBlockRefs(
	files []string,
	blockNames []string,
	re *regexp.Regexp,
) []blockInfo {
	results := make([]blockInfo, 0, len(files))

	for _, path := range files {
		fileName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"Warning: cannot read %q: %v\n",
				path,
				err,
			)
			continue
		}

		blockType := detectBlockType(string(content))

		seen := make(map[string]bool)
		refs := []blockRef{}

		for _, match := range re.FindAllString(string(content), -1) {
			if match == fileName || seen[match] {
				continue
			}
			seen[match] = true

			refs = append(refs, blockRef{
				Name: match,
				Type: resolveRefType(files, match),
			})
		}

		results = append(results, blockInfo{
			Name: fileName,
			Type: blockType,
			Use:  refs,
		})
	}

	return results
}

func parseFlags() (outputFile, scanPaths string, help, showVersion bool) {
	pflag.StringVarP(&outputFile, "output-file", "o", "", "Path to the output JSON file")
	pflag.StringVarP(&scanPaths, "scan-paths", "p", "", "Comma-separated directories to scan")
	pflag.BoolVarP(&help, "help", "h", false, "Show this help message")
	pflag.BoolVarP(&showVersion, "version", "v", false, "Show version")

	pflag.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: tia-scan-calls [flags]

Description:
  Scans files under the specified directories, detects cross-references
  between blocks, and outputs a JSON call graph.

Flags:
  -o, --output-file <path>   Path to the output JSON file
  -p, --scan-paths <dir,...> Comma-separated directories to scan
  -v, --version              Show version
  -h, --help                 Show this help message

Examples:
  scan-calls --output-file result.json
  scan-calls -p udts,blocks\Function
  scan-calls -p myDir -o out.json
`)
	}
	pflag.Parse()
	return
}

func main() {
	outputFile, scanPaths, help, showVersion := parseFlags()
	if showVersion {
		fmt.Printf("tia-scan-calls %s\n", version)
		return
	}
	if help {
		pflag.Usage()
		return
	}

	paths := strings.Split(scanPaths, ",")
	for i := range paths {
		paths[i] = strings.TrimSpace(paths[i])
	}

	files, err := walkFiles(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No files found in the specified paths")
		os.Exit(1)
	}

	blockNames := collectBlockNames(files)
	fmt.Printf("Found %d files to scan\n", len(files))

	re := buildRefRegex(blockNames)
	out := scanBlockRefs(files, blockNames, re)

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Done. Processed %d blocks.\n", len(out))
}
