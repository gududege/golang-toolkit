package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// runJSONExport writes JSON output.
func runJSONExport(cfg *Config, records []Record, groupNames []string) error {
	arr := recordsToJSONables(records, groupNames)

	if cfg.DryRun {
		out, err := marshalJSON(mapsToOrdered(arr, groupNames), cfg.Export.JSON.PrettyLevel)
		if err != nil {
			return execErr(err.Error())
		}
		fmt.Println(string(out))
		return nil
	}

	// Append: read the existing array and append to it.
	if cfg.Export.Append {
		existing, err := readJSONArray(cfg.Export.JSON.File)
		if err != nil {
			return execErr(err.Error())
		}
		arr, err = mergeJSONArrays(existing, arr)
		if err != nil {
			return execErr(err.Error())
		}
	}

	out, err := marshalJSON(mapsToOrdered(arr, groupNames), cfg.Export.JSON.PrettyLevel)
	if err != nil {
		return execErr(err.Error())
	}
	return writeExportFile(cfg.Export.JSON.File, out)
}

// recordsToJSONables converts the record list to []map[string]any keyed by groupNames.
func recordsToJSONables(records []Record, groupNames []string) []map[string]any {
	arr := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		m := map[string]any{}
		for _, g := range groupNames {
			m[g] = rec[g]
		}
		arr = append(arr, m)
	}
	return arr
}

// orderedField is a single key/value pair with a fixed position.
type orderedField struct {
	key   string
	value any
}

// orderedMap is a JSON object whose fields keep insertion order during marshaling.
type orderedMap []orderedField

// mapsToOrdered converts generic maps to ordered maps using the given key order.
func mapsToOrdered(arr []map[string]any, groupNames []string) []orderedMap {
	out := make([]orderedMap, 0, len(arr))
	for _, m := range arr {
		om := make(orderedMap, 0, len(groupNames))
		for _, g := range groupNames {
			om = append(om, orderedField{key: g, value: m[g]})
		}
		out = append(out, om)
	}
	return out
}

// marshalJSON serializes the ordered array according to pretty-level.
// 0: compact; 1: one-level indent (single space); 2: full indent (two spaces).
func marshalJSON(arr []orderedMap, pretty int) ([]byte, error) {
	switch pretty {
	case 0:
		return marshalJSONArrayCompact(arr)
	case 2:
		return marshalJSONArrayIndent(arr, "  ", "  ")
	default: // 1: one-level indent
		return marshalJSONArrayIndent(arr, " ", "")
	}
}

func marshalJSONArrayCompact(arr []orderedMap) ([]byte, error) {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, om := range arr {
		if i > 0 {
			sb.WriteByte(',')
		}
		if err := writeObjectCompact(&sb, om); err != nil {
			return nil, err
		}
	}
	sb.WriteByte(']')
	return []byte(sb.String()), nil
}

func marshalJSONArrayIndent(arr []orderedMap, itemIndent, fieldIndent string) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("[\n")
	for i, om := range arr {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString(itemIndent)
		sb.WriteByte('{')
		if fieldIndent != "" {
			sb.WriteByte('\n')
		}
		for j, f := range om {
			if j > 0 {
				sb.WriteByte(',')
				if fieldIndent == "" {
					sb.WriteByte(' ')
				} else {
					sb.WriteByte('\n')
				}
			}
			if fieldIndent != "" {
				sb.WriteString(itemIndent)
				sb.WriteString(fieldIndent)
			}
			keyBytes, err := json.Marshal(f.key)
			if err != nil {
				return nil, err
			}
			sb.Write(keyBytes)
			if fieldIndent != "" {
				sb.WriteString(": ")
			} else {
				sb.WriteByte(':')
			}
			valBytes, err := json.Marshal(f.value)
			if err != nil {
				return nil, err
			}
			sb.Write(valBytes)
		}
		if fieldIndent != "" {
			sb.WriteByte('\n')
			sb.WriteString(itemIndent)
		}
		sb.WriteByte('}')
	}
	if len(arr) > 0 {
		sb.WriteByte('\n')
	}
	sb.WriteByte(']')
	return []byte(sb.String()), nil
}

func writeObjectCompact(sb *strings.Builder, om orderedMap) error {
	sb.WriteByte('{')
	for i, f := range om {
		if i > 0 {
			sb.WriteByte(',')
		}
		keyBytes, err := json.Marshal(f.key)
		if err != nil {
			return err
		}
		sb.Write(keyBytes)
		sb.WriteByte(':')
		valBytes, err := json.Marshal(f.value)
		if err != nil {
			return err
		}
		sb.Write(valBytes)
	}
	sb.WriteByte('}')
	return nil
}

// readJSONArray reads an existing JSON file and parses it as an array.
func readJSONArray(path string) ([]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var arr []map[string]any
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("parse existing JSON %q: %w", path, err)
	}
	return arr, nil
}

// mergeJSONArrays checks that the first record structures are compatible, then appends.
func mergeJSONArrays(existing, incoming []map[string]any) ([]map[string]any, error) {
	if len(existing) == 0 {
		return incoming, nil
	}
	if len(incoming) == 0 {
		return existing, nil
	}
	if !sameKeys(existing[0], incoming[0]) {
		return nil, fmt.Errorf("JSON append: field structure mismatch (existing %v vs new %v)", keysOf(existing[0]), keysOf(incoming[0]))
	}
	return append(existing, incoming...), nil
}

func sameKeys(a, b map[string]any) bool {
	return slices.Equal(sortedKeys(a), sortedKeys(b))
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func sortedKeys(m map[string]any) []string {
	ks := keysOf(m)
	sort.Strings(ks)
	return ks
}

// writeExportFile creates the parent directory and writes the data (overwriting).
func writeExportFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return execErr(fmt.Sprintf("create dir %q: %v", filepath.Dir(path), err))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return execErr(fmt.Sprintf("write %q: %v", path, err))
	}
	return nil
}
