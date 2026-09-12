package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config is the full configuration object. The structure maps to the spec.md definition.
type Config struct {
	Input  InputConfig  `mapstructure:"input"`
	Regex  RegexConfig  `mapstructure:"regex"`
	Split  SplitConfig  `mapstructure:"split"`
	Export ExportConfig `mapstructure:"export"`
	DryRun bool         `mapstructure:"dry-run"`

	// Regexes enables multi-regex parallel mode. Each task carries its own
	// regex and optional split/export outputs. Mutually exclusive with the
	// top-level regex/split/export configuration.
	Regexes []RegexTaskConfig `mapstructure:"regexes"`

	// ConfigPath is used only with --config (or the positional argument) and
	// does not participate in mapstructure unmarshaling.
	ConfigPath string `mapstructure:"-"`
}

// RegexTaskConfig is a single extraction task in multi-regex mode.
type RegexTaskConfig struct {
	Name    string        `mapstructure:"name"`    // Optional label used in errors and warnings.
	Pattern string        `mapstructure:"pattern"` // Required regex pattern with named groups.
	Flags   string        `mapstructure:"flags"`   // Regex flags: m, s, i.
	Split   *SplitConfig  `mapstructure:"split"`   // Optional split output for this task.
	Export  *ExportConfig `mapstructure:"export"`  // Optional structured exports for this task.
}

type InputConfig struct {
	File      string `mapstructure:"file"`
	Directory string `mapstructure:"directory"`
	Pattern   string `mapstructure:"pattern"`
	Encoding  string `mapstructure:"encoding"`
}

type RegexConfig struct {
	Pattern string `mapstructure:"pattern"`
	Flags   string `mapstructure:"flags"`
}

type SplitConfig struct {
	Group     string `mapstructure:"group"`
	OutputDir string `mapstructure:"output-dir"`
	File      string `mapstructure:"file"`
	Encoding  string `mapstructure:"encoding"`
}

type ExportConfig struct {
	Append bool               `mapstructure:"append"`
	Sort   string             `mapstructure:"sort"`
	JSON   JSONExportConfig   `mapstructure:"json"`
	CSV    CSVExportConfig    `mapstructure:"csv"`
	SQLite SQLiteExportConfig `mapstructure:"sqlite"`
}

type JSONExportConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	File        string `mapstructure:"file"`
	PrettyLevel int    `mapstructure:"pretty-level"`
}

type CSVExportConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	File              string `mapstructure:"file"`
	Delimiter         string `mapstructure:"delimiter"`
	DelimiterExplicit bool   `mapstructure:"-"`
}

type SQLiteExportConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	File         string `mapstructure:"file"`
	Table        string `mapstructure:"table"`
	OverrideType string `mapstructure:"override-type"`
}

// setupFlags defines all flags and returns the parsed pflag.FlagSet plus remaining positional arguments.
// No validation happens here; only registration and binding.
func setupFlags(args []string) (*pflag.FlagSet, []string, error) {
	fs := pflag.NewFlagSet("extractor", pflag.ContinueOnError)
	fs.SortFlags = false

	// Required (with short names)
	fs.StringP("config", "c", "", "Config file path (YAML, TOML)")
	fs.StringP("input-file", "i", "", "Input file path (required unless --config or --input-directory is set)")
	fs.StringP("input-directory", "d", "", "Input directory (required unless --config or --input-file is set; mutually exclusive with --input-file)")
	fs.StringP("regex-pattern", "r", "", "Regular expression pattern with named groups (required unless --config is set)")

	// Optional input
	fs.String("input-pattern", "", "Directory file glob pattern (default: \"*\")")
	fs.String("input-encoding", "", "Input file encoding: utf8, utf8bom, gbk (default: utf8)")

	// Regex flags
	fs.String("regex-flags", "", "Regex flags: m (multiline), s (dotall), i (ignore case)")

	// Split
	fs.String("split-group", "", "Group name that triggers split output (enables split mode when set)")
	fs.String("split-output-dir", "", "Split output directory (default: \".\")")
	fs.String("split-file", "", "Split file name template; use {group_name} placeholders, or \".ext\" for source-based naming (default: {group}.txt)")
	fs.String("split-encoding", "", "Split output encoding: utf8, utf8bom, gbk (default: utf8)")

	// Export common
	fs.Bool("export-append", false, "Append to existing export files instead of overwriting")
	fs.String("export-sort", "", "Sort output properties: asc, desc, none (default: none)")

	// JSON
	fs.String("export-json-file", "", "JSON export file path (enables JSON export when set)")
	fs.Int("export-json-pretty-level", 0, "JSON indentation: 0=compact, 1=one-level, 2=full (default: 1)")

	// CSV
	fs.String("export-csv-file", "", "CSV export file path (enables CSV export when set)")
	fs.String("export-csv-delimiter", "", "CSV delimiter: , | ; \\t (default: ,)")

	// SQLite
	fs.String("export-sqlite-file", "", "SQLite export file path (enables SQLite export when set)")
	fs.String("export-sqlite-table", "", "SQLite table name (default: \"data\")")
	fs.String("export-sqlite-override-type", "", "SQLite column type override (format: col:type,col:type; types: string,int,int32,int64,float,double,bool,date,datetime)")

	// Dry run
	fs.Bool("dry-run", false, "Only print to stdout, do not write files")

	// Version
	fs.BoolP("version", "v", false, "Show version")

	fs.Usage = func() { customUsage(fs) }

	if err := fs.Parse(args); err != nil {
		if isHelpError(err) {
			return fs, nil, &exitErr{msg: "", code: 0}
		}
		return fs, nil, err
	}
	return fs, fs.Args(), nil
}

// isHelpError reports whether err is the pflag help-request error.
func isHelpError(err error) bool {
	return err != nil && err.Error() == "pflag: help requested"
}

// flagToKey maps pflag names to viper dotted keys for nested struct unmarshaling.
var flagToKey = map[string]string{
	"input-file":                  "input.file",
	"input-directory":             "input.directory",
	"input-pattern":               "input.pattern",
	"input-encoding":              "input.encoding",
	"regex-pattern":               "regex.pattern",
	"regex-flags":                 "regex.flags",
	"split-group":                 "split.group",
	"split-output-dir":            "split.output-dir",
	"split-file":                  "split.file",
	"split-encoding":              "split.encoding",
	"export-append":               "export.append",
	"export-sort":                 "export.sort",
	"export-json-file":            "export.json.file",
	"export-json-pretty-level":    "export.json.pretty-level",
	"export-csv-file":             "export.csv.file",
	"export-csv-delimiter":        "export.csv.delimiter",
	"export-sqlite-file":          "export.sqlite.file",
	"export-sqlite-table":         "export.sqlite.table",
	"export-sqlite-override-type": "export.sqlite.override-type",
	"dry-run":                     "dry-run",
}

// loadConfig builds and returns the full Config. args does not include the program name.
func loadConfig(args []string) (*Config, error) {
	fs, _, err := setupFlags(args)
	if err != nil {
		return nil, err
	}

	// Handle --version before any other processing.
	if showVersion, _ := fs.GetBool("version"); showVersion {
		fmt.Printf("extractor %s\n", version)
		return nil, &exitErr{msg: "", code: 0}
	}

	// A single positional argument is treated as the config file path. This
	// makes "extractor config.yaml" work and enables dragging a config file
	// onto the executable (Windows passes the path as the first argument).
	configPath := ""
	if cf := fs.Lookup("config"); cf != nil {
		configPath = cf.Value.String()
	}
	if positional := fs.Args(); len(positional) > 0 {
		if configPath != "" {
			return nil, argErr("config file cannot be provided both via --config and as a positional argument")
		}
		if len(positional) > 1 {
			return nil, argErr(fmt.Sprintf("unexpected positional arguments %q; at most one config file path is allowed", strings.Join(positional, " ")))
		}
		configPath = positional[0]
	}

	v := viper.New()

	// 1. Set all default values (lowest priority).
	v.SetDefault("input.encoding", "utf8")
	v.SetDefault("input.pattern", "*")
	v.SetDefault("export.sort", "none")
	v.SetDefault("export.json.pretty-level", 1)
	v.SetDefault("export.csv.delimiter", ",")
	v.SetDefault("export.sqlite.table", "data")

	// 2. Read the config file specified by --config or the positional argument (medium priority).
	// JSON is no longer supported as a config format.
	if configPath != "" {
		if strings.EqualFold(filepath.Ext(configPath), ".json") {
			return nil, argErr(fmt.Sprintf("config file %q: JSON config format is no longer supported; use YAML or TOML", configPath))
		}
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config file %q: %w", configPath, err)
		}
	}

	// 3. Apply CLI values only for explicitly set flags (highest priority).
	for flag, key := range flagToKey {
		if !fs.Changed(flag) {
			continue
		}
		if f := fs.Lookup(flag); f != nil {
			v.Set(key, f.Value.String())
		}
	}

	cfg := &Config{ConfigPath: configPath}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	// Semantics: export formats are enabled when their output file is set.
	cfg.Export.JSON.Enabled = cfg.Export.JSON.File != ""
	cfg.Export.CSV.Enabled = cfg.Export.CSV.File != ""
	cfg.Export.SQLite.Enabled = cfg.Export.SQLite.File != ""
	cfg.Export.CSV.DelimiterExplicit = fs.Changed("export-csv-delimiter")

	// Multi-regex mode: viper defaults do not reach array items, so per-task
	// export defaults are injected into the raw maps and the tasks are decoded
	// explicitly.
	if raw := v.Get("regexes"); raw != nil {
		items, ok := raw.([]any)
		if !ok {
			return nil, argErr("regexes must be a list of task objects")
		}
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				setNestedDefault(m, []string{"export", "json", "pretty-level"}, 1)
			}
		}
		cfg.Regexes = nil
		dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			Result:           &cfg.Regexes,
			WeaklyTypedInput: true,
		})
		if err != nil {
			return nil, fmt.Errorf("decode regexes: %w", err)
		}
		if err := dec.Decode(items); err != nil {
			return nil, fmt.Errorf("decode regexes: %w", err)
		}
	}

	// Validate encoding values.
	if cfg.Input.Encoding != "" {
		if !isSupportedEncoding(cfg.Input.Encoding) {
			return nil, argErr("--input-encoding must be one of: utf8, utf8bom, gbk")
		}
	}
	if cfg.Split.Encoding != "" {
		if !isSupportedEncoding(cfg.Split.Encoding) {
			return nil, argErr("--split-encoding must be one of: utf8, utf8bom, gbk")
		}
	}

	// Validate pretty-level values.
	if cfg.Export.JSON.PrettyLevel < 0 || cfg.Export.JSON.PrettyLevel > 2 {
		return nil, argErr("--export-json-pretty-level must be 0, 1, or 2")
	}

	// Validate regex flags contain only supported characters.
	for _, f := range cfg.Regex.Flags {
		if f != 'm' && f != 's' && f != 'i' {
			return nil, argErr("--regex-flags contains unsupported flag '" + string(f) + "'; supported: m, s, i")
		}
	}

	// Validate sort option.
	switch cfg.Export.Sort {
	case "asc", "desc", "none":
	default:
		return nil, argErr("--export-sort must be one of asc, desc, or none")
	}

	// Validation.
	if cfg.Input.File != "" && cfg.Input.Directory != "" {
		return nil, argErr("--input-file and --input-directory are mutually exclusive")
	}
	if configPath == "" {
		// Without --config, required flags are enforced.
		if cfg.Input.File == "" && cfg.Input.Directory == "" {
			return nil, argErr("either --input-file or --input-directory is required")
		}
		if cfg.Regex.Pattern == "" {
			return nil, argErr("--regex-pattern is required")
		}
	}

	// CSV delimiter validation.
	if cfg.Export.CSV.Enabled {
		switch cfg.Export.CSV.Delimiter {
		case ",", "|", ";", "\t":
		default:
			return nil, argErr("--export-csv-delimiter only supports , | ; \\t")
		}
	}

	// Multi-regex mode validation and per-task normalization.
	if len(cfg.Regexes) > 0 {
		if err := validateRegexTasks(cfg); err != nil {
			return nil, err
		}
	} else if configPath != "" && cfg.Regex.Pattern == "" {
		return nil, argErr("regex.pattern is required when the config file does not define regexes")
	}

	return cfg, nil
}

// validateRegexTasks validates multi-regex mode configuration and normalizes
// per-task export defaults and enabled flags. It reports an error when the
// top-level single-regex configuration is mixed in, when a task is incomplete,
// or when two tasks write to the same export file.
func validateRegexTasks(cfg *Config) error {
	if cfg.Regex.Pattern != "" || cfg.Split.Group != "" || cfg.Split.File != "" ||
		cfg.Export.JSON.File != "" ||
		cfg.Export.CSV.File != "" || cfg.Export.SQLite.File != "" {
		return argErr("regexes cannot be combined with top-level regex, split, or export configuration")
	}

	// Export output conflict rules: one normalized path must never be shared
	// by different export categories (json/csv/sqlite). Sharing a path
	// within the same category across tasks is allowed — writes follow
	// configuration order and each task's export.append decides overwrite vs
	// append.
	type sharedExport struct {
		category string
		norm     string
		orig     string
		labels   []string
	}
	pathCategory := map[string]string{} // normalized path -> category
	var shared []sharedExport
	sharedIdx := map[string]int{} // category \x00 norm -> index into shared

	for i := range cfg.Regexes {
		t := &cfg.Regexes[i]
		label := taskLabel(t.Name, i)

		if t.Pattern == "" {
			return argErr(fmt.Sprintf("%s: pattern is required", label))
		}
		for _, f := range t.Flags {
			if f != 'm' && f != 's' && f != 'i' {
				return argErr(fmt.Sprintf("%s: flags contains unsupported flag %q; supported: m, s, i", label, string(f)))
			}
		}

		if t.Split != nil {
			if t.Split.Encoding != "" && !isSupportedEncoding(t.Split.Encoding) {
				return argErr(fmt.Sprintf("%s: split.encoding must be one of: utf8, utf8bom, gbk", label))
			}
		}

		if t.Export != nil {
			applyExportDefaults(t.Export)
			if err := validateExportConfig(t.Export, label); err != nil {
				return err
			}
			for _, tgt := range exportTargets(t.Export) {
				norm := normalizeExportPath(tgt.Path)
				if prevCat, ok := pathCategory[norm]; ok {
					if prevCat != tgt.Category {
						return argErr(fmt.Sprintf("export file %q is used by both %s and %s export", tgt.Path, prevCat, tgt.Category))
					}
					idx := sharedIdx[tgt.Category+"\x00"+norm]
					shared[idx].labels = append(shared[idx].labels, label)
					continue
				}
				pathCategory[norm] = tgt.Category
				sharedIdx[tgt.Category+"\x00"+norm] = len(shared)
				shared = append(shared, sharedExport{category: tgt.Category, norm: norm, orig: tgt.Path, labels: []string{label}})
			}
		}
	}

	// Warn (non-fatal) about same-category files written by multiple tasks.
	sort.Slice(shared, func(a, b int) bool {
		return shared[a].category+"\x00"+shared[a].norm < shared[b].category+"\x00"+shared[b].norm
	})
	for _, s := range shared {
		if len(s.labels) > 1 {
			fmt.Fprintf(stderr, "warning: export %s file %q is written by multiple tasks (%s); writes follow configuration order and each task's export.append decides overwrite vs append\n",
				s.category, s.orig, strings.Join(s.labels, ", "))
		}
	}
	return nil
}

// taskLabel returns the display name of a regex task for error messages.
func taskLabel(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("regexes[%d]", index)
}

// applyExportDefaults fills task-level export defaults that mirror the viper
// defaults applied in single-regex mode.
func applyExportDefaults(ec *ExportConfig) {
	if ec.Sort == "" {
		ec.Sort = "none"
	}
	if ec.CSV.Delimiter == "" {
		ec.CSV.Delimiter = ","
	}
}

// validateExportConfig validates one task's export configuration and enables
// the formats whose output file is configured.
func validateExportConfig(ec *ExportConfig, label string) error {
	if ec.JSON.PrettyLevel < 0 || ec.JSON.PrettyLevel > 2 {
		return argErr(fmt.Sprintf("%s: export.json.pretty-level must be 0, 1, or 2", label))
	}
	switch ec.Sort {
	case "asc", "desc", "none":
	default:
		return argErr(fmt.Sprintf("%s: export.sort must be one of asc, desc, or none", label))
	}
	if ec.CSV.File != "" {
		switch ec.CSV.Delimiter {
		case ",", "|", ";", "\t":
		default:
			return argErr(fmt.Sprintf("%s: export.csv.delimiter only supports , | ; \\t", label))
		}
	}
	ec.JSON.Enabled = ec.JSON.File != ""
	ec.CSV.Enabled = ec.CSV.File != ""
	ec.SQLite.Enabled = ec.SQLite.File != ""
	return nil
}

// exportTarget is one configured export output with its format category.
type exportTarget struct {
	Category string // "json" | "csv" | "sqlite"
	Path     string // raw path (used in messages)
}

// exportTargets returns the configured export outputs of a task.
func exportTargets(ec *ExportConfig) []exportTarget {
	targets := make([]exportTarget, 0, 3)
	if ec.JSON.File != "" {
		targets = append(targets, exportTarget{"json", ec.JSON.File})
	}
	if ec.CSV.File != "" {
		targets = append(targets, exportTarget{"csv", ec.CSV.File})
	}
	if ec.SQLite.File != "" {
		targets = append(targets, exportTarget{"sqlite", ec.SQLite.File})
	}
	return targets
}

// normalizeExportPath canonicalizes an export path for conflict comparison:
// filepath.Clean plus case folding on case-insensitive platforms (Windows),
// so "./out.json" vs "out.json" and "Out.JSON" vs "out.json" are detected as
// the same file.
func normalizeExportPath(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// setNestedDefault sets value at the nested key path when the leaf is absent.
func setNestedDefault(m map[string]any, path []string, value any) {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			if _, ok := cur[k]; !ok {
				cur[k] = value
			}
			return
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
}

// customUsage prints grouped help in the order: Input, Regex, Config, Split, Export.
// Sections are separated by a single blank line.
func customUsage(fs *pflag.FlagSet) {
	fmt.Fprintf(stderr, "Usage:\n  %s [flags] [config-file]\n\n", fs.Name())
	fmt.Fprintln(stderr, "A single positional argument is treated as the config file path,")
	fmt.Fprintln(stderr, "so a config file can be dragged onto the executable to run.")
	fmt.Fprintln(stderr)

	sections := []struct {
		name  string
		desc  string
		flags []string
	}{
		{"Input flags", "",
			[]string{"input-file", "input-directory", "input-pattern", "input-encoding"}},
		{"Regex flags", "",
			[]string{"regex-pattern", "regex-flags"}},
		{"Config flags", "",
			[]string{"config", "dry-run", "version"}},
		{"Split flags", "",
			[]string{"split-group", "split-output-dir", "split-file", "split-encoding"}},
		{"Export flags", "",
			[]string{"export-append", "export-sort", "export-json-file", "export-json-pretty-level", "export-csv-file", "export-csv-delimiter", "export-sqlite-file", "export-sqlite-table", "export-sqlite-override-type"}},
	}

	for i, sec := range sections {
		if i > 0 {
			fmt.Fprintln(stderr)
		}
		fmt.Fprintf(stderr, "%s:\n", sec.name)
		if sec.desc != "" {
			fmt.Fprintln(stderr, sec.desc)
		}
		printFlagSection(fs, sec.flags)
	}
}

// printFlagSection prints the usage lines for the listed flag names in registration order.
func printFlagSection(fs *pflag.FlagSet, names []string) {
	section := pflag.NewFlagSet(fs.Name(), pflag.ContinueOnError)
	section.SortFlags = false
	for _, name := range names {
		if f := fs.Lookup(name); f != nil {
			section.AddFlag(f)
		}
	}
	usage := strings.TrimRight(section.FlagUsagesWrapped(0), "\n")
	fmt.Fprintln(stderr, usage)
}

// exitErr is an error carrying an exit code.
type exitErr struct {
	msg  string
	code int
}

func (e *exitErr) Error() string { return e.msg }

func argErr(msg string) error        { return &exitErr{msg: msg, code: 2} }
func execErr(msg string) error       { return &exitErr{msg: msg, code: 1} }
func regexParseErr(msg string) error { return &exitErr{msg: msg, code: 4} }

// stderr is a helper variable for writing to stderr in tests.
var stderr = os.Stderr

// isSupportedEncoding reports whether enc is a supported encoding name (after normalization).
func isSupportedEncoding(enc string) bool {
	switch normalizeEncoding(enc) {
	case "utf8", "utf8bom", "gbk":
		return true
	default:
		return false
	}
}
