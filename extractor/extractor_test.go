package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	regexp2 "github.com/dlclark/regexp2"
	_ "modernc.org/sqlite"
)

// test fixtures
const (
	logFile = "test-data/2026-07-03.log"
	// Matches each DAT line: ...DAT ---> <len> .NNN,Level,Reader,Action,Count.
	logRegex    = `(?P<time>\d{4}-\d{2}-\d{2}T[\d:.]+) DAT ---> <(?P<len>\d+)> \.(?P<seq>\d{3}),(?P<level>\w+),(?P<reader>RFID\d+),(?P<action>Good \w+),(?P<count>\d+)\.`
	logFlags    = "m"
	wantMatches = 334 // counted via grep
)

const (
	splitFile  = "test-data/tia-blocks.scl"
	splitRegex = `(?xms)^(?<block>(?<kind>[A-Z_]+)\s+"(?<name>[^"]+)"(?<header>[^\r\n]*)\r?\n(?<content>.*?)^END_\k<kind>\b)`
)

func runExit(t *testing.T, args ...string) int {
	t.Helper()
	return Run(append([]string{"extractor"}, args...))
}

// 1. Stats mode: no split/export, prints statistics, exit 0.
func TestStatsMode(t *testing.T) {
	code := runExit(t, "-i", logFile, "--regex-pattern", logRegex, "--regex-flags", logFlags)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	// Black-box match count checks are hard, so validate via the internal function here.
	re, err := compileRegex(RegexConfig{Pattern: logRegex, Flags: logFlags})
	if err != nil {
		t.Fatalf("compile regex: %v", err)
	}
	data, _ := os.ReadFile(logFile)
	text, err := decodeText(data, "utf8")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	records, _, names, err := matchFiles([]inputFile{{path: logFile, text: text}}, re)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(records) != wantMatches {
		t.Fatalf("expected %d matches, got %d", wantMatches, len(records))
	}
	wantGroupSet := map[string]bool{"time": true, "len": true, "seq": true, "level": true, "reader": true, "action": true, "count": true}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	for k := range wantGroupSet {
		if !got[k] {
			t.Fatalf("missing group %q in %v", k, names)
		}
	}
	// Spot-check one record value.
	r := records[0]
	if r["reader"] != "RFID8" && r["reader"] != "RFID9" {
		t.Fatalf("unexpected reader %q", r["reader"])
	}
	if r["count"] == "" {
		t.Fatalf("count should be non-empty")
	}
}

// 4' — Unmatched optional Group: keep the field with an empty value when the group does not match.
func TestUnmatchedOptionalGroup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "1:\n2:99\n")
	// The optional seq group only matches the second record.
	pattern := `(?P<time>\d+):(?P<seq>\d+)?`
	out := filepath.Join(dir, "o.json")
	code := runExit(t, "-i", src, "--regex-pattern", pattern, "--regex-flags", "m",
		"--export-json-file", out)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var arr []map[string]any
	mustDecodeJSON(t, out, &arr)
	if len(arr) != 2 {
		t.Fatalf("expected 2 records, got %d", len(arr))
	}
	// The first seq should be an empty string, not missing.
	v, ok := arr[0]["seq"].(string)
	if !ok {
		t.Fatalf("seq missing or non-string: %v", arr[0]["seq"])
	}
	if v != "" {
		t.Fatalf("expected empty seq on optional miss, got %q", v)
	}
	if arr[1]["seq"] != "99" {
		t.Fatalf("second seq should be 99, got %v", arr[1]["seq"])
	}
}

// 2. Split: group by reader, duplicate names add _index, auto-create directories.
// Use a small synthetic input so the test does not generate tens of thousands of files from the large log.
func TestSplitExport(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "RFID8,a\nRFID9,b\nRFID8,c\n")
	outDir := filepath.Join(dir, "split")
	// pattern: (?P<reader>RFID\d+),(?P<x>\w)
	code := runExit(t, "-i", src, "--regex-pattern", `(?P<reader>RFID\d+),(?P<x>\w)`,
		"--split-group", "reader", "--split-output-dir", outDir, "--split-file", "{reader}.txt")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	st, err := os.Stat(outDir)
	if err != nil || !st.IsDir() {
		t.Fatalf("output dir not created: %v", err)
	}
	// 3 records: the first and third share RFID8.txt, the second is RFID9.txt.
	// Duplicates automatically add _index: RFID8.txt, RFID8_1.txt, RFID9.txt.
	expect := map[string]bool{
		"RFID8.txt":   true,
		"RFID8_1.txt": true,
		"RFID9.txt":   true,
	}
	ents, _ := os.ReadDir(outDir)
	if len(ents) != len(expect) {
		t.Fatalf("expected %d split files, got %d", len(expect), len(ents))
	}
	for _, e := range ents {
		if !expect[e.Name()] {
			t.Fatalf("unexpected file %q", e.Name())
		}
	}
	// Content should be the group value.
	b, _ := os.ReadFile(filepath.Join(outDir, "RFID8.txt"))
	if string(b) != "RFID8" {
		t.Fatalf("split file content = %q, want RFID8", string(b))
	}
	b, _ = os.ReadFile(filepath.Join(outDir, "RFID8_1.txt"))
	if string(b) != "RFID8" {
		t.Fatalf("dup split file content = %q, want RFID8", string(b))
	}
}

// 2b. Split: when only an extension is given, generate source_filename_index.ext.
func TestSplitExtOnlyTemplate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mylog.txt")
	mustWrite(t, src, "a1\na2\na3\n")
	outDir := filepath.Join(dir, "split")
	code := runExit(t, "-i", src, "--regex-pattern", `(?P<x>a\d)`, "--regex-flags", "m",
		"--split-group", "x", "--split-output-dir", outDir, "--split-file", ".txt")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// Expect mylog.txt_1.txt, mylog.txt_2.txt, mylog.txt_3.txt.
	for i := 1; i <= 3; i++ {
		p := filepath.Join(outDir, "mylog.txt_"+strconv.Itoa(i)+".txt")
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
	}
}

// 3. JSON Export to disk + deserialize back.
func TestJSONExport(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.json")
	code := runExit(t, "-i", logFile, "--regex-pattern", logRegex, "--regex-flags", logFlags,
		"--export-json-file", out)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("JSON file not written: %v", err)
	}
	var arr []map[string]any
	mustDecodeJSON(t, out, &arr)
	if len(arr) != wantMatches {
		t.Fatalf("expected %d records, got %d", wantMatches, len(arr))
	}
	// All fields present and the first reader is RFID8/RFID9.
	r, _ := arr[0]["reader"].(string)
	if r != "RFID8" && r != "RFID9" {
		t.Fatalf("reader = %q", r)
	}
}

// 3b. JSON export-sort: output properties follow the chosen sort order.
func TestJSONExportSort(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "b,1,a,2,c\n")
	pattern := `(?P<b>\w),(?P<v>\d),(?P<a>\w),(?P<w>\d),(?P<c>\w)`

	cases := []struct {
		sort string
		want []string
	}{
		{"none", []string{"b", "v", "a", "w", "c"}},
		{"asc", []string{"a", "b", "c", "v", "w"}},
		{"desc", []string{"w", "v", "c", "b", "a"}},
	}

	for _, tc := range cases {
		out := filepath.Join(dir, "sort_"+tc.sort+".json")
		code := runExit(t, "-i", src, "--regex-pattern", pattern,
			"--export-json-file", out, "--export-sort", tc.sort)
		if code != 0 {
			t.Fatalf("sort=%s exit %d", tc.sort, code)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("sort=%s read: %v", tc.sort, err)
		}
		got := extractJSONObjectKeys(string(data))
		if !sameStringSlice(got, tc.want) {
			t.Fatalf("sort=%s keys=%v want=%v", tc.sort, got, tc.want)
		}
	}
}

func extractJSONObjectKeys(s string) []string {
	// Trim array wrapper and take the first object.
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	s = strings.TrimSpace(s)
	idx := strings.Index(s, "{")
	if idx == -1 {
		return nil
	}
	end := strings.Index(s[idx:], "}")
	if end == -1 {
		return nil
	}
	obj := s[idx : idx+end+1]
	var keys []string
	dec := json.NewDecoder(strings.NewReader(obj))
	dec.Token() // {
	for dec.More() {
		tok, _ := dec.Token()
		if k, ok := tok.(string); ok {
			keys = append(keys, k)
		}
		dec.Token() // value
	}
	return keys
}

// 6. GBK input decoding.
func TestGBKInput(t *testing.T) {
	dir := t.TempDir()
	// Build a GBK-encoded text file containing a Chinese label.
	// "编号:1,名称:测试" encoded only matches the numeric/alphabetic parts.
	gbkBytes, err := encodeText("编号:1,名称:test\n编号:2,名称:demo\n", "gbk")
	if err != nil {
		t.Fatalf("gbk encode: %v", err)
	}
	src := filepath.Join(dir, "gbk.txt")
	os.WriteFile(src, gbkBytes, 0o644)
	out := filepath.Join(dir, "o.json")
	code := runExit(t, "-i", src, "--input-encoding", "gbk",
		"--regex-pattern", `编号:(?P<id>\d+),名称:(?P<name>\w+)`, "--regex-flags", "m",
		"--export-json-file", out)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var arr []map[string]any
	mustDecodeJSON(t, out, &arr)
	if len(arr) != 2 || arr[0]["id"] != "1" || arr[0]["name"] != "test" {
		t.Fatalf("unexpected records: %v", arr)
	}
}

// 4. CSV Export (dry-run with header): validate header directly via marshalCSV.
func TestCSVDryRun(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "a,1\nb,2\n")
	re, err := compileRegex(RegexConfig{Pattern: `(?P<k>\w+),(?P<v>\d)`})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, _ := os.ReadFile(src)
	text, _ := decodeText(data, "utf8")
	records, _, names, _ := matchFiles([]inputFile{{path: src, text: text}}, re)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	out, err := marshalCSV(records, names, ',')
	if err != nil {
		t.Fatalf("marshalCSV: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 1 {
		t.Fatalf("csv empty")
	}
	if !strings.Contains(lines[0], "k") || !strings.Contains(lines[0], "v") {
		t.Fatalf("csv missing header: %q", lines[0])
	}
}

// 4. CSV Export to disk (with header).
func TestCSVExport(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "a,1\nb,2\n")
	out := filepath.Join(dir, "o.csv")
	code := runExit(t, "-i", src, "--regex-pattern", `(?P<k>\w+),(?P<v>\d)`,
		"--export-csv-file", out)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	f, _ := os.Open(out)
	defer f.Close()
	r := csv.NewReader(f)
	all, err := r.ReadAll()
	if err != nil || len(all) != 3 {
		t.Fatalf("csv malformed: %v rows=%d", err, len(all))
	}
	if all[0][0] != "k" || all[0][1] != "v" {
		t.Fatalf("header %v", all[0])
	}
}

// 5. SQLite Export dry-run preview + disk validation of schema/rows.
func TestSQLiteExport(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.db")
	code := runExit(t, "-i", logFile, "--regex-pattern", logRegex, "--regex-flags", logFlags,
		"--export-sqlite-file", out)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	db, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM data").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != wantMatches {
		t.Fatalf("rows = %d, want %d", n, wantMatches)
	}
	rows, err := db.Query("PRAGMA table_info(data)")
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()
	var typeMap = map[string]string{}
	for rows.Next() {
		var cid int
		var nm, ty string
		var nn, pk int
		var dflt sql.NullString
		rows.Scan(&cid, &nm, &ty, &nn, &dflt, &pk)
		typeMap[nm] = ty
	}
	if typeMap["count"] != "INTEGER" {
		t.Fatalf("count column type = %q, want INTEGER", typeMap["count"])
	}
	if typeMap["reader"] != "TEXT" {
		t.Fatalf("reader column type = %q, want TEXT", typeMap["reader"])
	}
}

// 5b. SQLite override-type: force column type.
func TestSQLiteOverrideType(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "a,1\nb,2\n")
	out := filepath.Join(dir, "o.db")
	code := runExit(t, "-i", src, "--regex-pattern", `(?P<name>\w+),(?P<count>\d)`,
		"--export-sqlite-file", out, "--export-sqlite-override-type", "count:string")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	db, _ := sql.Open("sqlite", out)
	defer db.Close()
	rows, _ := db.Query("PRAGMA table_info(data)")
	defer rows.Close()
	var tyCount string
	for rows.Next() {
		var cid int
		var nm, ty string
		var nn, pk int
		var dflt sql.NullString
		rows.Scan(&cid, &nm, &ty, &nn, &dflt, &pk)
		if nm == "count" {
			tyCount = ty
		}
	}
	if tyCount != "TEXT" {
		t.Fatalf("override count type = %q, want TEXT", tyCount)
	}
}

// 7. No matches: exit code 3.
func TestNoMatches(t *testing.T) {
	code := runExit(t, "-i", logFile, "--regex-pattern", `NOSUCHPATTERN_ZZZ`)
	if code != 3 {
		t.Fatalf("expected exit 3 (no matches), got %d", code)
	}
}

// 8. Missing required arguments: exit code 2.
func TestMissingRequired(t *testing.T) {
	// Missing regex pattern.
	code := runExit(t, "-i", logFile)
	if code != 2 {
		t.Fatalf("expected exit 2 (missing regex), got %d", code)
	}
	// Missing any input.
	code = runExit(t, "--regex-pattern", "x")
	if code != 2 {
		t.Fatalf("expected exit 2 (missing input), got %d", code)
	}
}

// 8b. Regex compile error: exit code 4.
func TestRegexCompileError(t *testing.T) {
	code := runExit(t, "-i", logFile, "--regex-pattern", `(unterminated`)
	if code != 4 {
		t.Fatalf("expected exit 4 (regex compile), got %d", code)
	}
}

// 8c. Unsupported regex flag is treated as an argument error (exit 2) by upfront validation.
func TestUnsupportedRegexFlag(t *testing.T) {
	code := runExit(t, "-i", logFile, "--regex-pattern", "x", "--regex-flags", "z")
	if code != 2 {
		t.Fatalf("expected exit 2 (arg error), got %d", code)
	}
}

// 8d. input-file and input-directory are mutually exclusive.
func TestInputMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	code := runExit(t, "-i", logFile, "-d", dir, "--regex-pattern", "x")
	if code != 2 {
		t.Fatalf("expected exit 2 (mutual exclusion), got %d", code)
	}
}

// 8e. Config file: load via YAML config.
func TestConfigFileYAML(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.json")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "input:\n  file: " + logFile + "\nregex:\n  pattern: '" + logRegex + "'\n  flags: 'm'\nexport:\n  json:\n    file: " + out + "\n"
	os.WriteFile(cfgPath, []byte(cfg), 0o644)
	code := runExit(t, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("json not written via config: %v", err)
	}
}

// 8f. Unsupported encoding reports an error.
func TestUnsupportedEncoding(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "hello\n")
	code := runExit(t, "-i", src, "--input-encoding", "shift-jis", "--regex-pattern", "hello")
	if code == 0 || code == 3 {
		t.Fatalf("expected non-zero exit for unsupported encoding, got %d", code)
	}
}

// 9. Config-only mode: --config satisfies all required parameters, no CLI required fields checked.
func TestConfigOnlyMode(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.json")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "input:\n  file: " + logFile + "\nregex:\n  pattern: '" + logRegex + "'\n  flags: 'm'\nexport:\n  json:\n    file: " + out + "\n"
	os.WriteFile(cfgPath, []byte(cfg), 0o644)
	code := runExit(t, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("json not written via config: %v", err)
	}
}

// 10. Directory mode with no --input-pattern matches all files.
func TestDirectoryDefaultPattern(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "a1\n")
	mustWrite(t, filepath.Join(dir, "b.txt"), "b2\n")
	out := filepath.Join(dir, "o.json")
	code := runExit(t, "-d", dir, "--regex-pattern", `(?P<x>\w\d)`, "--export-json-file", out)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var arr []map[string]any
	mustDecodeJSON(t, out, &arr)
	if len(arr) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(arr))
	}
}

// 11. SQLite append inserts new rows without dropping the existing table.
func TestSQLiteAppend(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.db")
	code := runExit(t, "-i", logFile, "--regex-pattern", logRegex, "--regex-flags", logFlags,
		"--export-sqlite-file", out)
	if code != 0 {
		t.Fatalf("first export exit %d", code)
	}
	code = runExit(t, "-i", logFile, "--regex-pattern", logRegex, "--regex-flags", logFlags,
		"--export-sqlite-file", out, "--export-append")
	if code != 0 {
		t.Fatalf("append export exit %d", code)
	}
	db, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM data").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != wantMatches*2 {
		t.Fatalf("rows = %d, want %d", n, wantMatches*2)
	}
}

// 12. Split extension-only template uses the source filename per record.
func TestSplitExtOnlyPerSource(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "alpha.log"), "x\ny\n")
	mustWrite(t, filepath.Join(dir, "beta.log"), "z\n")
	outDir := filepath.Join(dir, "split")
	code := runExit(t, "-d", dir, "--regex-pattern", `(?P<v>\w)`, "--regex-flags", "m",
		"--split-group", "v", "--split-output-dir", outDir, "--split-file", ".txt")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, name := range []string{"alpha.log_1.txt", "alpha.log_2.txt", "beta.log_1.txt"} {
		p := filepath.Join(outDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
}

// JSON is no longer supported as a config file format (YAML/TOML only).
func TestJSONConfigRejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.json")
	mustWrite(t, cfgPath, "{}")

	if code := runExit(t, "--config", cfgPath); code != 2 {
		t.Fatalf("--config json: expected exit 2, got %d", code)
	}
	// Same via positional argument (drag-and-drop).
	if code := runExit(t, cfgPath); code != 2 {
		t.Fatalf("positional json config: expected exit 2, got %d", code)
	}
}

// 15. CSV append with explicit delimiter mismatch errors out.
func TestCSVDelimiterMismatch(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "o.csv")
	// First write a comma-delimited CSV.
	mustWrite(t, out, "k,v\na,1\n")
	// Append with semicolon delimiter explicitly specified.
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "b;2\n")
	code := runExit(t, "-i", src, "--regex-pattern", `(?P<k>\w+);(?P<v>\d)`,
		"--export-csv-file", out, "--export-append", "--export-csv-delimiter", ";")
	if code == 0 {
		t.Fatalf("expected non-zero exit for delimiter mismatch")
	}
}

// 16. Duplicate named groups are rejected at compile time.
func TestDuplicateNamedGroup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "a\n")
	code := runExit(t, "-i", src, "--regex-pattern", `(?P<v>\w)(?P<v>\w)`)
	if code != 4 {
		t.Fatalf("expected exit 4 (regex compile), got %d", code)
	}
}

// 17. Run with empty args returns exit code 2.
func TestRunEmptyArgs(t *testing.T) {
	code := Run([]string{"extractor"})
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

// 18. Encoding names are normalized case-insensitively.
func TestNormalizeEncodingCase(t *testing.T) {
	if normalizeEncoding("UTF-8") != "utf8" {
		t.Fatalf("UTF-8 should normalize to utf8")
	}
	if normalizeEncoding("  GbK  ") != "gbk" {
		t.Fatalf("GbK should normalize to gbk")
	}
}

// 19. Config file overrides defaults; CLI overrides config.
func TestConfigOverrideDefaults(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "b,2\na,1\n")

	// First: config file sets sort=desc, json-pretty-level=2, csv-delimiter, sqlite-table.
	cfgPath := filepath.Join(dir, "cfg.yaml")
	out := filepath.Join(dir, "out.json")
	cfg := fmt.Sprintf("input:\n  file: %s\nregex:\n  pattern: '(?P<k>\\w+),(?P<v>\\d)'\nexport:\n  sort: desc\n  json:\n    file: %s\n    pretty-level: 2\n  csv:\n    delimiter: '|'\n  sqlite:\n    table: cfgtbl\n", src, out)
	os.WriteFile(cfgPath, []byte(cfg), 0o644)

	code := runExit(t, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// Verify pretty-level=2 (2-space indent).
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "  \"k\":") {
		t.Fatalf("config pretty-level=2: expected 2-space indent, got:\n%s", string(data))
	}
	// Verify sort=desc: first key should be v (desc: v comes before k is wrong... actually desc sorts by key, so w > v > k?
	// Let's just check the order: desc means reverse alphabetical of k,v → v,k.
	keys := extractJSONObjectKeys(string(data))
	if !sameStringSlice(keys, []string{"v", "k"}) {
		t.Fatalf("config sort=desc: expected [v k], got %v", keys)
	}

	// Second: CLI flag overrides config.
	out2 := filepath.Join(dir, "out2.json")
	code = runExit(t, "--config", cfgPath, "--export-json-file", out2, "--export-sort", "asc", "--export-json-pretty-level", "0")
	if code != 0 {
		t.Fatalf("CLI override exit %d", code)
	}
	data2, _ := os.ReadFile(out2)
	// pretty-level=0: compact, no newlines.
	if strings.Contains(string(data2), "\n") {
		t.Fatalf("CLI pretty-level=0: expected compact (no newlines), got:\n%s", string(data2))
	}
	keys2 := extractJSONObjectKeys(string(data2))
	if !sameStringSlice(keys2, []string{"k", "v"}) {
		t.Fatalf("CLI sort=asc: expected [k v], got %v", keys2)
	}
}

// helpers -----------------------------------------------------------------

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustDecodeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.TrimSpace(string(data)) != want {
		t.Fatalf("file %s: expected %q, got %q", path, want, string(data))
	}
}

// TestSplitSCLFile splits the SCL file into blocks using regexp2 lookahead.
func TestSplitSCLFile(t *testing.T) {
	data, err := os.ReadFile(splitFile)
	if err != nil {
		t.Fatalf("read SCL file: %v", err)
	}
	text := string(data)

	// Uses positive lookahead at line start to stop before the next block-start keyword.
	const pattern = splitRegex
	re, err := regexp2.Compile(pattern, regexp2.None)
	if err != nil {
		t.Fatalf("compile SCL split regex: %v", err)
	}

	endKW := map[string]string{
		"TYPE":           "END_TYPE",
		"FUNCTION":       "END_FUNCTION",
		"FUNCTION_BLOCK": "END_FUNCTION_BLOCK",
		"DATA_BLOCK":     "END_DATA_BLOCK",
	}

	counts := map[string]int{}
	match, err := re.FindStringMatch(text)
	if err != nil {
		t.Fatalf("find match: %v", err)
	}
	for match != nil {
		kw := match.Groups()[1].String()
		block := match.String()
		counts[kw]++

		end := endKW[kw]
		if !strings.Contains(block, end) {
			tail := block
			if len(tail) > 100 {
				tail = "..." + tail[len(tail)-100:]
			}
			t.Fatalf("block #%d %s (len=%d) missing %s, tail: %s", counts[kw], kw, len(block), end, tail)
		}

		match, err = re.FindNextMatch(match)
		if err != nil {
			t.Fatalf("find next: %v", err)
		}
	}

	if counts["TYPE"] < 50 {
		t.Fatalf("expected >= 50 TYPE blocks, got %d", counts["TYPE"])
	}
	if counts["FUNCTION"] < 5 {
		t.Fatalf("expected >= 5 FUNCTION blocks, got %d", counts["FUNCTION"])
	}
	if counts["FUNCTION_BLOCK"] < 10 {
		t.Fatalf("expected >= 10 FUNCTION_BLOCK blocks, got %d", counts["FUNCTION_BLOCK"])
	}
	if counts["DATA_BLOCK"] < 10 {
		t.Fatalf("expected >= 10 DATA_BLOCK blocks, got %d", counts["DATA_BLOCK"])
	}
	t.Logf("Blocks: TYPE=%d FUNCTION=%d FUNCTION_BLOCK=%d DATA_BLOCK=%d",
		counts["TYPE"], counts["FUNCTION"], counts["FUNCTION_BLOCK"], counts["DATA_BLOCK"])
}

// ===========================================================================
// Issue #2: multi-regex parallel mode, split.output-dir template, and
// positional-argument config (drag-and-drop onto the executable).
// ===========================================================================

// Multi-regex mode: two tasks with independent outputs run in parallel.
func TestMultiRegexMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "ERR: boom\nERR: bang\nINFO: ok\nWARN: meh\n")

	cfgPath := filepath.Join(dir, "cfg.yaml")
	jsonOut := filepath.Join(dir, "errors.json")
	jsonCompact := filepath.Join(dir, "errors-compact.json")
	splitDir := filepath.Join(dir, "split")
	cfg := fmt.Sprintf(`input:
  file: %s
regexes:
  - name: errors
    pattern: '(?P<level>ERR): (?P<msg>.+)'
    export:
      json:
        file: %s
  - name: errors-compact
    pattern: '(?P<level>ERR): (?P<msg>.+)'
    export:
      json:
        file: %s
        pretty-level: 0
  - name: all-levels
    pattern: '(?P<level>\w+): (?P<msg>.+)'
    split:
      group: level
      output-dir: %s
`, src, jsonOut, jsonCompact, splitDir)
	mustWrite(t, cfgPath, cfg)

	code := runExit(t, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	// JSON export: 2 records with the default one-level pretty indent.
	var arr []map[string]any
	mustDecodeJSON(t, jsonOut, &arr)
	if len(arr) != 2 {
		t.Fatalf("expected 2 json records, got %d", len(arr))
	}
	data, _ := os.ReadFile(jsonOut)
	if !strings.Contains(string(data), "\n {") {
		t.Fatalf("expected default one-level indent, got:\n%s", string(data))
	}

	// Explicit pretty-level: 0 stays compact (defaults must not override it).
	dataCompact, _ := os.ReadFile(jsonCompact)
	if strings.Contains(string(dataCompact), "\n") {
		t.Fatalf("expected compact json, got:\n%s", string(dataCompact))
	}

	// Split output: one file per level value.
	for _, name := range []string{"ERR.txt", "INFO.txt", "WARN.txt"} {
		if _, err := os.Stat(filepath.Join(splitDir, name)); err != nil {
			t.Fatalf("split file %s: %v", name, err)
		}
	}
}

// Multi-regex dry-run works and produces no files beyond the inputs.
func TestMultiRegexDryRun(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "ERR: boom\nINFO: ok\n")

	cfgPath := filepath.Join(dir, "cfg.yaml")
	jsonOut := filepath.Join(dir, "errors.json")
	splitDir := filepath.Join(dir, "split")
	cfg := fmt.Sprintf(`input:
  file: %s
regexes:
  - pattern: '(?P<level>ERR): (?P<msg>.+)'
    export:
      json:
        file: %s
  - pattern: '(?P<level>\w+): (?P<msg>.+)'
    split:
      group: level
      output-dir: %s
`, src, jsonOut, splitDir)
	mustWrite(t, cfgPath, cfg)

	code := runExit(t, "--config", cfgPath, "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(jsonOut); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write files")
	}
	if _, err := os.Stat(splitDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create split dir")
	}
}

// regexes is mutually exclusive with the top-level regex/split/export config.
func TestMultiRegexMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")

	// Top-level regex alongside regexes.
	cfg := fmt.Sprintf("input:\n  file: %s\nregex:\n  pattern: 'x'\nregexes:\n  - pattern: 'y'\n", logFile)
	mustWrite(t, cfgPath, cfg)
	if code := runExit(t, "--config", cfgPath); code != 2 {
		t.Fatalf("top-level regex conflict: expected exit 2, got %d", code)
	}

	// CLI export flag alongside regexes.
	cfg2 := fmt.Sprintf("input:\n  file: %s\nregexes:\n  - pattern: 'x'\n", logFile)
	mustWrite(t, cfgPath, cfg2)
	if code := runExit(t, "--config", cfgPath, "--export-json-file", filepath.Join(dir, "o.json")); code != 2 {
		t.Fatalf("CLI flag conflict: expected exit 2, got %d", code)
	}
}

// Same-category export files may be shared across tasks: writes follow
// configuration order and each task's export.append decides overwrite vs
// append (task1 overwrite-creates, task2 appends → merged content).
func TestMultiRegexSharedExport(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "ERR: boom\nERR: bang\nINFO: ok\n")

	cfgPath := filepath.Join(dir, "cfg.yaml")
	out := filepath.Join(dir, "out.json")
	cfg := fmt.Sprintf(`input:
  file: %s
regexes:
  - name: errors
    pattern: '(?P<level>ERR): (?P<msg>.+)'
    export:
      append: false
      json:
        file: %s
  - name: info
    pattern: '(?P<level>INFO): (?P<msg>.+)'
    export:
      append: true
      json:
        file: %s
`, src, out, out)
	mustWrite(t, cfgPath, cfg)

	code := runExit(t, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	var arr []map[string]any
	mustDecodeJSON(t, out, &arr)
	if len(arr) != 3 {
		t.Fatalf("expected 3 merged records (2 overwrite + 1 append), got %d", len(arr))
	}
	if arr[2]["msg"] != "ok" {
		t.Fatalf("expected appended record to come last, got %v", arr[2])
	}
}

// Different export categories must not share the same file path (across tasks
// or within one task), and paths are compared normalized.
func TestMultiRegexCrossCategoryConflict(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "ERR: boom\n")

	// Across tasks: json vs csv on the same path.
	cfgPath := filepath.Join(dir, "cfg.yaml")
	out := filepath.Join(dir, "o")
	cfg := fmt.Sprintf(`input:
  file: %s
regexes:
  - pattern: '(?P<a>\w+)'
    export:
      json:
        file: %s.json
  - pattern: '(?P<b>\w+)'
    export:
      csv:
        file: %s.json
`, src, out, out)
	mustWrite(t, cfgPath, cfg)
	if code := runExit(t, "--config", cfgPath); code != 2 {
		t.Fatalf("cross-category across tasks: expected exit 2, got %d", code)
	}

	// Within one task: json and csv on the same path.
	cfg2 := fmt.Sprintf(`input:
  file: %s
regexes:
  - pattern: '(?P<a>\w+)'
    export:
      json:
        file: %s
      csv:
        file: %s
`, src, out, out)
	mustWrite(t, cfgPath, cfg2)
	if code := runExit(t, "--config", cfgPath); code != 2 {
		t.Fatalf("cross-category within task: expected exit 2, got %d", code)
	}

	// Normalization: "./o.json" vs "o.json" is the same file.
	cfg3 := fmt.Sprintf(`input:
  file: %s
regexes:
  - pattern: '(?P<a>\w+)'
    export:
      json:
        file: ./o.json
  - pattern: '(?P<b>\w+)'
    export:
      csv:
        file: o.json
`, src)
	mustWrite(t, cfgPath, cfg3)
	if code := runExit(t, "--config", cfgPath); code != 2 {
		t.Fatalf("normalized path conflict: expected exit 2, got %d", code)
	}
}

// On case-insensitive filesystems (Windows), path comparison folds case.
func TestMultiRegexPathCaseConflict(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case folding applies to Windows only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "ERR: boom\n")

	cfgPath := filepath.Join(dir, "cfg.yaml")
	cfg := fmt.Sprintf(`input:
  file: %s
regexes:
  - pattern: '(?P<a>\w+)'
    export:
      json:
        file: Out.JSON
  - pattern: '(?P<b>\w+)'
    export:
      csv:
        file: out.json
`, src)
	mustWrite(t, cfgPath, cfg)
	if code := runExit(t, "--config", cfgPath); code != 2 {
		t.Fatalf("case-variant path conflict: expected exit 2, got %d", code)
	}
}

// Split files with the same name produced by different tasks get unique
// _1/_2 suffixes instead of overwriting each other.
func TestMultiRegexSplitSharedDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "ERR: one\nINFO: two\n")

	cfgPath := filepath.Join(dir, "cfg.yaml")
	splitDir := filepath.Join(dir, "split")
	cfg := fmt.Sprintf(`input:
  file: %s
regexes:
  - name: errors
    pattern: '(?P<level>ERR): (?P<msg>.+)'
    split:
      group: level
      output-dir: %s
      file: 'same.txt'
  - name: info
    pattern: '(?P<level>INFO): (?P<msg>.+)'
    split:
      group: level
      output-dir: %s
      file: 'same.txt'
`, src, splitDir, splitDir)
	mustWrite(t, cfgPath, cfg)

	code := runExit(t, "--config", cfgPath)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	assertFileContent(t, filepath.Join(splitDir, "same.txt"), "ERR")
	assertFileContent(t, filepath.Join(splitDir, "same_1.txt"), "INFO")
}

// A task without a pattern is rejected.
func TestMultiRegexMissingPattern(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	cfg := fmt.Sprintf("input:\n  file: %s\nregexes:\n  - name: empty\n    flags: m\n", logFile)
	mustWrite(t, cfgPath, cfg)
	if code := runExit(t, "--config", cfgPath); code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

// A compile error in any task exits with code 4 and names the task.
func TestMultiRegexCompileError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	cfg := fmt.Sprintf(`input:
  file: %s
regexes:
  - name: good
    pattern: '(?P<a>\w+)'
  - name: bad
    pattern: '(?P<unclosed'
`, logFile)
	mustWrite(t, cfgPath, cfg)
	if code := runExit(t, "--config", cfgPath); code != 4 {
		t.Fatalf("expected exit 4, got %d", code)
	}
}

// A single no-match task only warns; all no-match tasks exit with code 3.
func TestMultiRegexNoMatches(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "ERR: boom\nINFO: ok\n")

	cfgPath := filepath.Join(dir, "cfg.yaml")
	out := filepath.Join(dir, "o.json")
	base := "input:\n  file: " + src + "\n"

	// One matching task + one no-match task: exit 0.
	cfg := base + fmt.Sprintf(`regexes:
  - pattern: '(?P<level>ERR): (?P<msg>.+)'
    export:
      json:
        file: %s
  - name: nomatch
    pattern: '(?P<level>DEBUG): (?P<msg>.+)'
    export:
      json:
        file: %s
`, out, filepath.Join(dir, "o2.json"))
	mustWrite(t, cfgPath, cfg)
	if code := runExit(t, "--config", cfgPath); code != 0 {
		t.Fatalf("partial no-match: expected exit 0, got %d", code)
	}
	var arr []map[string]any
	mustDecodeJSON(t, out, &arr)
	if len(arr) != 1 {
		t.Fatalf("expected 1 json record, got %d", len(arr))
	}

	// All tasks without matches: exit 3.
	cfg = base + `regexes:
  - pattern: '(?P<level>DEBUG): (?P<msg>.+)'
  - pattern: '(?P<level>TRACE): (?P<msg>.+)'
`
	mustWrite(t, cfgPath, cfg)
	if code := runExit(t, "--config", cfgPath); code != 3 {
		t.Fatalf("all no-match: expected exit 3, got %d", code)
	}
}

// split.output-dir supports {group_name} templates like split.file.
func TestSplitOutputDirTemplate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "ERR: boom\nINFO: ok\n")

	splitDir := filepath.Join(dir, "out")
	code := runExit(t, "-i", src,
		"--regex-pattern", `(?P<level>\w+): (?P<msg>.+)`,
		"--split-group", "level",
		"--split-output-dir", filepath.Join(splitDir, "{level}"),
		"--split-file", "{msg}.txt")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, p := range []string{
		filepath.Join(splitDir, "ERR", "boom.txt"),
		filepath.Join(splitDir, "INFO", "ok.txt"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
	}
}

// A single positional argument is treated as the config file path
// (drag-and-drop onto the executable).
func TestConfigPositionalArg(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	mustWrite(t, src, "a,1\n")

	cfgPath := filepath.Join(dir, "cfg.yaml")
	out := filepath.Join(dir, "o.json")
	cfg := fmt.Sprintf("input:\n  file: %s\nregex:\n  pattern: '(?P<k>\\w+),(?P<v>\\d)'\nexport:\n  json:\n    file: %s\n", src, out)
	mustWrite(t, cfgPath, cfg)

	code := runExit(t, cfgPath)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	var arr []map[string]any
	mustDecodeJSON(t, out, &arr)
	if len(arr) != 1 {
		t.Fatalf("expected 1 json record, got %d", len(arr))
	}
}

// The positional config argument conflicts with --config and with more than
// one positional argument.
func TestConfigPositionalConflict(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	mustWrite(t, cfgPath, "input:\n  file: x\n")

	if code := runExit(t, "--config", cfgPath, cfgPath); code != 2 {
		t.Fatalf("--config + positional: expected exit 2, got %d", code)
	}
	if code := runExit(t, cfgPath, cfgPath); code != 2 {
		t.Fatalf("two positionals: expected exit 2, got %d", code)
	}
}
