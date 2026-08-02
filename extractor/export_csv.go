package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// runCSVExport writes CSV output.
func runCSVExport(cfg *Config, records []Record, groupNames []string) error {
	delim := cfg.Export.CSV.Delimiter
	runeDelim := ','
	switch delim {
	case "|":
		runeDelim = '|'
	case ";":
		runeDelim = ';'
	case "\t":
		runeDelim = '\t'
	}

	if cfg.DryRun {
		out, err := marshalCSV(records, groupNames, runeDelim)
		if err != nil {
			return execErr(err.Error())
		}
		// Print raw CSV text (no table formatting).
		fmt.Print(string(out))
		return nil
	}

	appendMode := cfg.Export.Append
	if appendMode {
		existing, err := readCSVHeader(cfg.Export.CSV.File, runeDelim)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return execErr(fmt.Sprintf("CSV append: existing file %q is empty", cfg.Export.CSV.File))
			}
			if !os.IsNotExist(err) {
				return execErr(err.Error())
			}
			appendMode = false // File does not exist; treat as create.
		} else if !sameStringSlice(existing, groupNames) {
			if cfg.Export.CSV.DelimiterExplicit {
				return execErr(fmt.Sprintf("CSV append: header mismatch with delimiter %q (existing %v vs new %v)", cfg.Export.CSV.Delimiter, existing, groupNames))
			}
			return execErr(fmt.Sprintf("CSV append: header mismatch (existing %v vs new %v)", existing, groupNames))
		}
	}

	out, err := marshalCSVRows(records, groupNames, runeDelim, appendMode)
	if err != nil {
		return execErr(err.Error())
	}
	return writeCSVFile(cfg.Export.CSV.File, out, appendMode)
}

// marshalCSV generates the full CSV text (including the header).
func marshalCSV(records []Record, groupNames []string, delim rune) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Comma = delim
	if err := w.Write(groupNames); err != nil {
		return nil, err
	}
	if err := writeRows(w, records, groupNames); err != nil {
		w.Flush()
		return nil, err
	}
	w.Flush()
	return buf.Bytes(), nil
}

// marshalCSVRows generates rows; append mode omits the header.
func marshalCSVRows(records []Record, groupNames []string, delim rune, append bool) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Comma = delim
	if !append {
		if err := w.Write(groupNames); err != nil {
			return nil, err
		}
	}
	if err := writeRows(w, records, groupNames); err != nil {
		w.Flush()
		return nil, err
	}
	w.Flush()
	return buf.Bytes(), nil
}

func writeRows(w *csv.Writer, records []Record, groupNames []string) error {
	for _, rec := range records {
		row := make([]string, len(groupNames))
		for i, g := range groupNames {
			row[i] = rec[g]
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// writeCSVFile writes CSV: append mode appends, otherwise it overwrites.
func writeCSVFile(path string, data []byte, append bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return execErr(fmt.Sprintf("create dir: %v", err))
	}
	flag := os.O_CREATE | os.O_WRONLY
	if append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return execErr(fmt.Sprintf("open %q: %v", path, err))
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// readCSVHeader reads the first line of an existing CSV file using the given delimiter.
func readCSVHeader(path string, delim rune) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = delim
	return r.Read()
}

// sameStringSlice compares two string slices for equality (order-sensitive).
func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
