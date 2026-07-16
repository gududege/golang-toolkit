package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

// blockRef represents a single cross-reference from one block to another.
type blockRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// blockInfo represents a scanned block and all blocks it references.
type blockInfo struct {
	Name string     `json:"name"`
	Type string     `json:"type"`
	Use  []blockRef `json:"use"`
}

var version = "dev"

// outputFilePerm is the file permission for the JSON output.
const outputFilePerm os.FileMode = 0644

// blockTypeOrder defines the sort order for JSON output — OB first, UNKNOWN last.
var blockTypeOrder = map[string]int{
	"OB":      0,
	"FC":      1,
	"FB":      2,
	"DB":      3,
	"UDT":     4,
	"DI":      5,
	"UNKNOWN": 6,
}

// allowedExts restricts file scanning to known TIA Portal block extensions.
var allowedExts = []string{".db", ".udt", ".scl", ".awl", ".s7dcl"}

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

// detectBlockType determines the TIA Portal block type from its source content.
// It reads the first line for the block header and checks for VAR/STRUCT
// presence to distinguish DB from DI (instance data block).
func detectBlockType(content string) string {
	content = strings.TrimLeft(content, "\uFEFF")
	firstLine, _, _ := strings.Cut(content, "\n")
	firstLine = strings.TrimRight(firstLine, "\r")

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

// isBlockFile checks whether the file extension is in the allowed list.
func isBlockFile(path string) bool {
	return slices.Contains(allowedExts, strings.ToLower(filepath.Ext(path)))
}

// walkFiles recursively scans directories and returns all block file paths found.
func walkFiles(paths []string) ([]string, error) {
	// nil slice intentional — append works fine on nil
	var files []string
	for _, root := range paths {
		err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return fmt.Errorf("access %q: %w", path, err)
			}
			if !fi.IsDir() && isBlockFile(path) {
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

// collectBlockNames extracts block names from file paths by stripping
// directory and extension — the block name is the file's base name.
func collectBlockNames(files []string) []string {
	blockNames := make([]string, 0, len(files))
	for _, f := range files {
		blockNames = append(blockNames, strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
	}
	return blockNames
}

// buildRefRegex compiles a regex that matches any block name as a whole word.
// Each name is escaped to handle special characters.
func buildRefRegex(blockNames []string) *regexp.Regexp {
	escaped := make([]string, len(blockNames))
	for i, name := range blockNames {
		escaped[i] = regexp.QuoteMeta(name)
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(escaped, "|") + `)\b`)
}

// resolveRefType looks up the block type of a referenced block by scanning
// the file list for a matching name, then detecting its type.
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

// scanBlockRefs reads every block file, detects its type, and finds all
// cross-references to other blocks by matching against the compiled regex.
// Self-references are silently skipped.
func scanBlockRefs(
	files []string,
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

		matches := re.FindAllIndex(content, -1)
		for _, loc := range matches {
			match := string(content[loc[0]:loc[1]])
			if match == fileName {
				continue
			}
			if seen[match] {
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

// parseFlags registers and parses command-line flags using pflag.
func parseFlags() (outputFile string, scanPaths []string, help, showVersion bool) {
	pflag.StringVarP(&outputFile, "output-file", "o", "", "Path to the output JSON file")
	pflag.StringSliceVarP(&scanPaths, "scan-path", "p", nil, "Directories to scan (can be specified multiple times)")
	pflag.BoolVarP(&help, "help", "h", false, "Show this help message")
	pflag.BoolVarP(&showVersion, "version", "v", false, "Show version")

	pflag.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: tia-scan-calls [flags]

Description:
  Scans files under the specified directories, detects cross-references
  between blocks, and outputs a JSON call graph.

Flags:
  -o, --output-file <path>   Path to the output JSON file
  -p, --scan-path <dir>      Directories to scan (can be specified multiple times)
  -v, --version              Show version
  -h, --help                 Show this help message

Examples:
  tia-scan-calls -p ./udts -p ./blocks/Function -o ./out.json
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

	if outputFile == "" {
		fmt.Fprintln(os.Stderr, "Error: --output-file (-o) is required")
		pflag.Usage()
		os.Exit(1)
	}
	if len(scanPaths) == 0 {
		fmt.Fprintln(os.Stderr, "Error: --scan-paths (-p) is required")
		pflag.Usage()
		os.Exit(1)
	}

	files, err := walkFiles(scanPaths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No files found in the specified paths")
		os.Exit(1)
	}

	// Collect block names and build the reference-matching regex.
	blockNames := collectBlockNames(files)
	fmt.Printf("Found %d files to scan\n", len(files))

	re := buildRefRegex(blockNames)
	out := scanBlockRefs(files, re)

	// Sort output by block type order (OB → FC → FB → DB → UDT → UNKNOWN),
	// preserving original order within the same type.
	sort.SliceStable(out, func(i, j int) bool {
		return blockTypeOrder[out[i].Type] < blockTypeOrder[out[j].Type]
	})

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputFile, data, outputFilePerm); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Done. Processed %d blocks.\n", len(out))
}
