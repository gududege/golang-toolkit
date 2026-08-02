package main

import (
	"fmt"
	"os"
	"strings"

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

	// ConfigPath is used only with --config and does not participate in mapstructure unmarshaling.
	ConfigPath string `mapstructure:"-"`
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
	XML    XMLExportConfig    `mapstructure:"xml"`
	CSV    CSVExportConfig    `mapstructure:"csv"`
	SQLite SQLiteExportConfig `mapstructure:"sqlite"`
}

type JSONExportConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	File        string `mapstructure:"file"`
	PrettyLevel int    `mapstructure:"pretty-level"`
}

type XMLExportConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	File        string `mapstructure:"file"`
	Root        string `mapstructure:"root"`
	Element     string `mapstructure:"element"`
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
	fs.StringP("config", "c", "", "Config file path (YAML, TOML, JSON)")
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

	// XML
	fs.String("export-xml-file", "", "XML export file path (enables XML export when set)")
	fs.String("export-xml-root", "", "XML root element name (default: \"records\")")
	fs.String("export-xml-element", "", "XML record element name (default: \"record\")")
	fs.Int("export-xml-pretty-level", 0, "XML indentation: 0=compact, 1=one-level, 2=full (default: 1)")

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
	"export-xml-file":             "export.xml.file",
	"export-xml-root":             "export.xml.root",
	"export-xml-element":          "export.xml.element",
	"export-xml-pretty-level":     "export.xml.pretty-level",
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

	v := viper.New()

	// 1. Set all default values (lowest priority).
	v.SetDefault("input.encoding", "utf8")
	v.SetDefault("input.pattern", "*")
	v.SetDefault("export.sort", "none")
	v.SetDefault("export.json.pretty-level", 1)
	v.SetDefault("export.xml.pretty-level", 1)
	v.SetDefault("export.xml.root", "records")
	v.SetDefault("export.xml.element", "record")
	v.SetDefault("export.csv.delimiter", ",")
	v.SetDefault("export.sqlite.table", "data")

	// 2. Read the config file specified by --config (medium priority).
	configPath := ""
	if cf := fs.Lookup("config"); cf != nil {
		configPath = cf.Value.String()
	}
	if configPath != "" {
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
	cfg.Export.XML.Enabled = cfg.Export.XML.File != ""
	cfg.Export.CSV.Enabled = cfg.Export.CSV.File != ""
	cfg.Export.SQLite.Enabled = cfg.Export.SQLite.File != ""
	cfg.Export.CSV.DelimiterExplicit = fs.Changed("export-csv-delimiter")

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
	if cfg.Export.XML.PrettyLevel < 0 || cfg.Export.XML.PrettyLevel > 2 {
		return nil, argErr("--export-xml-pretty-level must be 0, 1, or 2")
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

	return cfg, nil
}

// customUsage prints grouped help in the order: Input, Regex, Config, Split, Export.
// Sections are separated by a single blank line.
func customUsage(fs *pflag.FlagSet) {
	fmt.Fprintf(stderr, "Usage:\n  %s [flags]\n\n", fs.Name())

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
			[]string{"export-append", "export-sort", "export-json-file", "export-json-pretty-level", "export-xml-file", "export-xml-root", "export-xml-element", "export-xml-pretty-level", "export-csv-file", "export-csv-delimiter", "export-sqlite-file", "export-sqlite-table", "export-sqlite-override-type"}},
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
