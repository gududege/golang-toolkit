package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	regexp2 "github.com/dlclark/regexp2"
)

var version = "dev"

// Record is a single match result: named group -> value.
type Record map[string]string

// inputFile is an input file that has been processed together with its path.
type inputFile struct {
	path string
	text string // decoded to UTF-8
}

// Run is the real entry point. args includes the program name (same as os.Args) and returns an exit code.
// main.go calls os.Exit(Run(os.Args)).
func Run(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: extractor [flags]")
		return 2
	}
	cfg, err := loadConfig(args[1:])
	if err != nil {
		// Exit code 0 is used for help output; do not print an error.
		if e, ok := err.(*exitErr); ok && e.code == 0 {
			return 0
		}
		fmt.Fprintln(stderr, err.Error())
		return exitCodeOf(err)
	}
	if err := execute(cfg); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitCodeOf(err)
	}
	return 0
}

// exitCodeOf recovers the exit code from err; non-exitErr is treated as an execution error with code 1.
func exitCodeOf(err error) int {
	if e, ok := err.(*exitErr); ok {
		return e.code
	}
	return 1
}

// execute runs the full processing pipeline.
func execute(cfg *Config) error {
	// 1. Collect and decode input files.
	files, err := readInputs(cfg)
	if err != nil {
		return err
	}

	// 2. Compile the regex.
	re, err := compileRegex(cfg.Regex)
	if err != nil {
		return regexParseErr(fmt.Sprintf("invalid regex: %v", err))
	}

	// 3. Match each file and aggregate all records.
	allRecords, recordSources, groupNames, err := matchFiles(files, re)
	if err != nil {
		return err
	}
	if len(allRecords) == 0 {
		return &exitErr{msg: "No matches found", code: 3}
	}

	// Apply the export sort order to the output property list.
	groupNames = sortedGroupNames(groupNames, cfg.Export.Sort)

	// 4. Decide the output mode.
	splitEnabled := cfg.Split.Group != ""
	exportEnabled := cfg.Export.JSON.Enabled || cfg.Export.XML.Enabled ||
		cfg.Export.CSV.Enabled || cfg.Export.SQLite.Enabled

	if !splitEnabled && !exportEnabled {
		// Only print statistics to stdout.
		printStats(allRecords, groupNames)
		return nil
	}

	// 5. Split output.
	if splitEnabled {
		if err := runSplit(cfg, files, allRecords, recordSources); err != nil {
			return err
		}
	}

	// 6. Exports.
	if cfg.Export.JSON.Enabled {
		if err := runJSONExport(cfg, allRecords, groupNames); err != nil {
			return err
		}
	}
	if cfg.Export.XML.Enabled {
		if err := runXMLExport(cfg, allRecords, groupNames); err != nil {
			return err
		}
	}
	if cfg.Export.CSV.Enabled {
		if err := runCSVExport(cfg, allRecords, groupNames); err != nil {
			return err
		}
	}
	if cfg.Export.SQLite.Enabled {
		if err := runSQLiteExport(cfg, allRecords, groupNames); err != nil {
			return err
		}
	}
	return nil
}

// readInputs reads and decodes all input files.
func readInputs(cfg *Config) ([]inputFile, error) {
	enc := normalizeEncoding(cfg.Input.Encoding)
	if cfg.Input.File != "" {
		data, err := os.ReadFile(cfg.Input.File)
		if err != nil {
			return nil, execErr(fmt.Sprintf("read file %q: %v", cfg.Input.File, err))
		}
		text, err := decodeText(data, enc)
		if err != nil {
			return nil, execErr(err.Error())
		}
		return []inputFile{{path: cfg.Input.File, text: text}}, nil
	}
	// Directory mode.
	pattern := cfg.Input.Pattern
	if pattern == "" {
		pattern = "*"
	}
	matches, err := filepath.Glob(filepath.Join(cfg.Input.Directory, pattern))
	if err != nil {
		return nil, execErr(fmt.Sprintf("glob %q: %v", pattern, err))
	}
	// Keep only regular files.
	var files []inputFile
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(m)
		if err != nil {
			return nil, execErr(fmt.Sprintf("read file %q: %v", m, err))
		}
		text, err := decodeText(data, enc)
		if err != nil {
			return nil, execErr(err.Error())
		}
		files = append(files, inputFile{path: m, text: text})
	}
	return files, nil
}

// compileRegex compiles the regex according to the given flags.
func compileRegex(rc RegexConfig) (*regexp2.Regexp, error) {
	var flags string
	for _, f := range rc.Flags {
		switch f {
		case 'm', 's', 'i':
			flags += string(f)
		default:
			return nil, fmt.Errorf("unsupported regex flag %q (supported: m/s/i)", string(f))
		}
	}
	pattern := rc.Pattern
	if flags != "" {
		pattern = "(?" + flags + ")" + pattern
	}
	// Detect duplicate named groups in the original pattern before conversion.
	if dup := findDuplicateGroupInPattern(rc.Pattern); dup != "" {
		return nil, fmt.Errorf("duplicate named group %q", dup)
	}
	// Convert Go-style (?P<name>...) to .NET-style (?<name>...) for regexp2.
	pattern = convertNamedGroups(pattern)
	re, err := regexp2.Compile(pattern, regexp2.None)
	if err != nil {
		return nil, err
	}
	if err := validateGroupNames(re); err != nil {
		return nil, err
	}
	return re, nil
}

// convertNamedGroups converts Go-style (?P<name>...) named groups to .NET-style (?<name>...).
func convertNamedGroups(pattern string) string {
	return strings.ReplaceAll(pattern, "(?P<", "(?<")
}

// findDuplicateGroupInPattern scans the raw pattern for duplicate named groups.
func findDuplicateGroupInPattern(pattern string) string {
	seen := map[string]bool{}
	re := regexp2.MustCompile(`\(\?P<([a-zA-Z0-9][a-zA-Z0-9_.\-]*)>`, regexp2.None)
	match, _ := re.FindStringMatch(pattern)
	for match != nil {
		name := match.Groups()[1].String()
		if name == "_" {
			match, _ = re.FindNextMatch(match)
			continue
		}
		if seen[name] {
			return name
		}
		seen[name] = true
		match, _ = re.FindNextMatch(match)
	}
	return ""
}

var validGroupNameRe = regexp2.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]*$`, regexp2.None)

// validateGroupNames checks that all named groups conform to the allowed pattern.
func validateGroupNames(re *regexp2.Regexp) error {
	for _, name := range re.GetGroupNames() {
		if name == "" || name == "_" || name == "0" {
			continue
		}
		match, _ := validGroupNameRe.MatchString(name)
		if !match {
			return fmt.Errorf("invalid group name %q (must match [a-zA-Z0-9][a-zA-Z0-9_.-]*)", name)
		}
	}
	return nil
}

// matchFiles matches all files and aggregates records plus their originating file paths and the group name list.
// groupNames is deduplicated and preserves the order of appearance in the regex.
func matchFiles(files []inputFile, re *regexp2.Regexp) ([]Record, []string, []string, error) {
	groupNames := orderedGroupNames(re)
	var records []Record
	var sources []string
	for _, f := range files {
		match, err := re.FindStringMatch(f.text)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("regex match: %w", err)
		}
		for match != nil {
			rec := Record{}
			for _, g := range match.Groups() {
				if g.Name == "" || g.Name == "_" {
					continue
				}
				rec[g.Name] = g.String()
			}
			records = append(records, rec)
			sources = append(sources, f.path)
			match, err = re.FindNextMatch(match)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("regex match: %w", err)
			}
		}
	}
	return records, sources, groupNames, nil
}

// orderedGroupNames returns the named group names in order of first appearance in the regex.
func orderedGroupNames(re *regexp2.Regexp) []string {
	seen := map[string]bool{}
	var names []string
	for _, name := range re.GetGroupNames() {
		if name == "" || name == "_" || name == "0" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// sortedGroupNames returns a copy of names ordered according to sortMode.
// "none" keeps the original order; "asc" sorts ascending; "desc" sorts descending.
func sortedGroupNames(names []string, sortMode string) []string {
	out := make([]string, len(names))
	copy(out, names)
	switch sortMode {
	case "asc":
		sort.Strings(out)
	case "desc":
		sort.Sort(sort.Reverse(sort.StringSlice(out)))
	}
	return out
}

// printStats prints match statistics to stdout.
func printStats(records []Record, groupNames []string) {
	fmt.Printf("Matches: %d\n", len(records))
	fmt.Printf("Groups: %d\n", len(groupNames))
	if len(groupNames) > 0 {
		fmt.Printf("Group names: %s\n", strings.Join(groupNames, ", "))
	}
}
