package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runSplit writes the content of the specified named group to separate text files.
// used is the shared path registry across all regex tasks: a path claimed by
// one task is visible to the others, so colliding split file names get unique
// _1/_2 suffixes instead of silently overwriting each other.
func runSplit(cfg *Config, files []inputFile, used map[string]bool, records []Record, sources []string) error {
	group := cfg.Split.Group

	// Validate the group exists in the matched records.
	if len(records) > 0 {
		if _, ok := records[0][group]; !ok {
			return execErr(fmt.Sprintf("split group %q not found in regex named groups", group))
		}
	}

	enc := cfg.Split.Encoding
	if enc == "" {
		enc = "utf8"
	}
	outputDir := cfg.Split.OutputDir
	if outputDir == "" {
		outputDir = "."
	}
	// The output directory may contain {group_name} placeholders, in which
	// case each record is written to its own rendered (and sanitized) directory.
	outputDirTemplated := strings.Contains(outputDir, "{")

	if !cfg.DryRun && !outputDirTemplated {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			return execErr(fmt.Sprintf("create output dir %q: %v", outputDir, err))
		}
	}

	tmpl := cfg.Split.File
	// When the template is just an extension (e.g. ".txt"), use sourceFile_index.ext.
	extOnly := tmpl != "" && strings.HasPrefix(tmpl, ".")

	// Per-source counters for extension-only templates.
	counters := map[string]int{}

	for i, rec := range records {
		text, ok := rec[group]
		if !ok {
			text = ""
		}
		// Render and sanitize the output directory per record when templated.
		dir := outputDir
		if outputDirTemplated {
			dir = safeDirPath(renderTemplateName(outputDir, rec))
			if dir == "" || dir == "." {
				dir = "."
			}
		}
		var name string
		switch {
		case tmpl == "":
			// safeFileName applied below after the switch.
			name = text + ".txt"
		case extOnly:
			sourceFile := filepath.Base(sources[i])
			counters[sourceFile]++
			name = fmt.Sprintf("%s_%d%s", sourceFile, counters[sourceFile], tmpl)
		default:
			name = renderTemplateName(tmpl, rec)
		}
		name = safeFileName(name)

		full := filepath.Join(dir, name)
		// Handle duplicate names: name -> name_1, name_2, ...
		full = resolveDuplicate(used, full)

		data, err := encodeText(text, enc)
		if err != nil {
			return execErr(err.Error())
		}

		if cfg.DryRun {
			fmt.Printf("==== Split file: %s ====\n", full)
			fmt.Println(text)
			fmt.Println()
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return execErr(fmt.Sprintf("create dir %q: %v", filepath.Dir(full), err))
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			return execErr(fmt.Sprintf("write %q: %v", full, err))
		}
	}
	return nil
}

// renderTemplateName replaces {group_name} placeholders with record values.
func renderTemplateName(tmpl string, rec Record) string {
	out := tmpl
	for k, v := range rec {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out
}

// resolveDuplicate appends an _index suffix to duplicate file paths.
// The final path is recorded in the used map.
func resolveDuplicate(used map[string]bool, path string) string {
	if !used[path] {
		used[path] = true
		return path
	}
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	name := strings.TrimSuffix(filepath.Base(path), ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, i, ext))
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

// safeFileName replaces illegal file-name characters.
func safeFileName(name string) string {
	if name == "" {
		return "output"
	}
	illegal := []string{"\\", "/", ":", "*", "?", "\"", "<", ">", "|"}
	for _, c := range illegal {
		name = strings.ReplaceAll(name, c, "_")
	}
	return name
}

// safeDirPath sanitizes each segment of a rendered directory path, preserving
// separators, Windows drive prefixes, and root markers. Segments that reduce
// to "." or ".." are replaced to prevent escaping the output root.
func safeDirPath(path string) string {
	if path == "" || path == "." || path == ".." {
		return path
	}
	var b strings.Builder
	// Preserve a Windows drive prefix like "C:" verbatim.
	if len(path) >= 2 && path[1] == ':' && isDriveLetter(path[0]) {
		b.WriteString(path[:2])
		path = path[2:]
	}
	for i := 0; i < len(path); {
		if path[i] == '/' || path[i] == '\\' {
			b.WriteByte(path[i])
			i++
			continue
		}
		start := i
		for i < len(path) && path[i] != '/' && path[i] != '\\' {
			i++
		}
		seg := safeFileName(path[start:i])
		if seg == "." || seg == ".." {
			seg = "_"
		}
		b.WriteString(seg)
	}
	return b.String()
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
