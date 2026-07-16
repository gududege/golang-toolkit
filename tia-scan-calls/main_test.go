package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/spf13/pflag"
)

// ---------- detectBlockType ----------

func TestDetectBlockType(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"OB", `ORGANIZATION_BLOCK "Main"
BEGIN
END_ORGANIZATION_BLOCK`, "OB"},
		{"FB", `FUNCTION_BLOCK "Timer"
BEGIN
END_FUNCTION_BLOCK`, "FB"},
		{"FC", `FUNCTION "Calculate"
BEGIN
END_FUNCTION`, "FC"},
		{"DB with VAR", `DATA_BLOCK "Stats"
BEGIN
VAR
  x : INT;
END_VAR
BEGIN
END_DATA_BLOCK`, "DB"},
		{"DB with STRUCT", `DATA_BLOCK "Config"
BEGIN
STRUCT
  x : INT;
END_STRUCT
BEGIN
END_DATA_BLOCK`, "DB"},
		{"DI (no VAR/STRUCT)", `DATA_BLOCK "Instance"
BEGIN
END_DATA_BLOCK`, "DI"},
		{"DI with only BEGIN", `DATA_BLOCK "EmptyDB"
BEGIN`, "DI"},
		{"UDT", `TYPE "Material"
STRUCT
  id : INT;
END_STRUCT
END_TYPE`, "UDT"},
		{"UNKNOWN", `SOMETHING "Unknown"
BEGIN
END`, "UNKNOWN"},
		{"empty string", "", "UNKNOWN"},
		{"BOM prefix", "\uFEFF" + `ORGANIZATION_BLOCK "Main"
BEGIN
END_ORGANIZATION_BLOCK`, "OB"},
		{"CRLF line endings", "FUNCTION_BLOCK \"FB1\"\r\nBEGIN\r\nEND_FUNCTION_BLOCK", "FB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectBlockType(tt.content)
			if got != tt.expected {
				t.Errorf("detectBlockType() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ---------- isBlockFile ----------

func TestIsBlockFile(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"DB extension", "some/path/block.db", true},
		{"UDT extension", "block.udt", true},
		{"SCL extension", "source.scl", true},
		{"AWL extension", "code.awl", true},
		{"S7DCL extension", "declaration.s7dcl", true},
		{"uppercase DB", "BLOCK.DB", true},
		{"mixed case", "Block.Scl", true},
		{"no extension", "README", false},
		{"txt extension", "notes.txt", false},
		{"json extension", "out.json", false},
		{"hidden db file", ".hidden.db", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBlockFile(tt.path)
			if got != tt.expected {
				t.Errorf("isBlockFile(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

// ---------- collectBlockNames ----------

func TestCollectBlockNames(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		expected []string
	}{
		{
			"normal paths",
			[]string{"dir/Main.db", "dir/SubFB.scl", "dir/Config.udt"},
			[]string{"Main", "SubFB", "Config"},
		},
		{
			"nested directories",
			[]string{"a/b/c/Block.db"},
			[]string{"Block"},
		},
		{
			"no extension",
			[]string{"dir/SomeFile"},
			[]string{"SomeFile"},
		},
		{
			"multiple dots in name",
			[]string{"dir/my.block.name.db"},
			[]string{"my.block.name"},
		},
		{
			"empty list",
			[]string{},
			[]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectBlockNames(tt.files)
			if len(got) != len(tt.expected) {
				t.Fatalf("collectBlockNames() = %v (len=%d), want %v (len=%d)", got, len(got), tt.expected, len(tt.expected))
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("collectBlockNames()[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

// ---------- buildRefRegex ----------

func TestBuildRefRegex(t *testing.T) {
	tests := []struct {
		name       string
		blockNames []string
		input      string
		matches    []string
	}{
		{
			"single name",
			[]string{"Main"},
			"Main SomeOther",
			[]string{"Main"},
		},
		{
			"multiple names",
			[]string{"Main", "SubFB", "StatsDB"},
			"Main calls SubFB",
			[]string{"Main", "SubFB"},
		},
		{
			"substring protection (word boundary)",
			[]string{"Main"},
			"Mainline",
			nil,
		},
		{
			"underscore in name",
			[]string{"My_Block", "FC_100"},
			"caller My_Block callee FC_100",
			[]string{"My_Block", "FC_100"},
		},
		{
			"name with dots",
			[]string{"My.Var"},
			"data My.Var end",
			[]string{"My.Var"},
		},
		{
			"partial word no match",
			[]string{"ABC"},
			"ABCDEF",
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re := buildRefRegex(tt.blockNames)
			got := re.FindAllString(tt.input, -1)

			if len(got) != len(tt.matches) {
				t.Errorf("FindAllString() = %v, want %v", got, tt.matches)
				return
			}
			for i := range got {
				if got[i] != tt.matches[i] {
					t.Errorf("FindAllString()[%d] = %q, want %q", i, got[i], tt.matches[i])
				}
			}
		})
	}
}

func TestBuildRefRegex_Compiles(t *testing.T) {
	re := buildRefRegex([]string{"A", "B", "C"})
	if re == nil {
		t.Fatal("buildRefRegex returned nil")
	}
	if re.MatchString("nothing") {
		t.Error("expected no match on unrelated text")
	}
	if !re.MatchString("A B C") {
		t.Error("expected match on block names")
	}
}

// ---------- resolveRefType ----------

func TestResolveRefType(t *testing.T) {
	dir := t.TempDir()

	files := []string{
		writeBlock(t, dir, "Main.db", `ORGANIZATION_BLOCK "Main"
BEGIN
END_ORGANIZATION_BLOCK`),
		writeBlock(t, dir, "SubFB.scl", `FUNCTION_BLOCK "SubFB"
BEGIN
END_FUNCTION_BLOCK`),
		writeBlock(t, dir, "Calc.fc", `FUNCTION "Calc"
BEGIN
END_FUNCTION`),
		writeBlock(t, dir, "Stats.db", `DATA_BLOCK "Stats"
BEGIN
VAR
  x : INT;
END_VAR
END_DATA_BLOCK`),
		writeBlock(t, dir, "Instance.di", `DATA_BLOCK "Instance"
BEGIN
END_DATA_BLOCK`),
		writeBlock(t, dir, "Types.udt", `TYPE "Types"
STRUCT
  x : INT;
END_STRUCT
END_TYPE`),
	}

	tests := []struct {
		name     string
		match    string
		expected string
	}{
		{"OB type", "Main", "OB"},
		{"FB type from SCL", "SubFB", "FB"},
		{"FC type", "Calc", "FC"},
		{"DB type", "Stats", "DB"},
		{"DI type", "Instance", "DI"},
		{"UDT type", "Types", "UDT"},
		{"no matching file", "NonExistent", "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveRefType(files, tt.match)
			if got != tt.expected {
				t.Errorf("resolveRefType(_, %q) = %q, want %q", tt.match, got, tt.expected)
			}
		})
	}
}

// ---------- scanBlockRefs ----------

func TestScanBlockRefs(t *testing.T) {
	dir := t.TempDir()

	files := []string{
		writeBlock(t, dir, "Main.db", `ORGANIZATION_BLOCK "Main"
  "SubFB";
  "Calc";
END_ORGANIZATION_BLOCK`),
		writeBlock(t, dir, "SubFB.scl", `FUNCTION_BLOCK "SubFB"
  // SubFB in comment, silently skipped
  "Calc";
END_FUNCTION_BLOCK`),
		writeBlock(t, dir, "Calc.fc", `FUNCTION "Calc"
  // no refs
END_FUNCTION`),
		writeBlock(t, dir, "Stats.db", `DATA_BLOCK "Stats"
BEGIN
  x : INT;
END_DATA_BLOCK`),
	}

	names := collectBlockNames(files)
	re := buildRefRegex(names)
	results := scanBlockRefs(files, re)

	lookup := make(map[string]blockInfo)
	for _, r := range results {
		lookup[r.Name] = r
	}

	t.Run("Main references SubFB and Calc", func(t *testing.T) {
		bi, ok := lookup["Main"]
		if !ok {
			t.Fatal("Main not found in results")
		}
		if bi.Type != "OB" {
			t.Errorf("Main type = %q, want OB", bi.Type)
		}
		if len(bi.Use) != 2 {
			t.Fatalf("Main.Use count = %d, want 2", len(bi.Use))
		}
		refNames := make(map[string]string)
		for _, ref := range bi.Use {
			refNames[ref.Name] = ref.Type
		}
		if refNames["SubFB"] != "FB" {
			t.Errorf("Main ref SubFB type = %q, want FB", refNames["SubFB"])
		}
		if refNames["Calc"] != "FC" {
			t.Errorf("Main ref Calc type = %q, want FC", refNames["Calc"])
		}
	})

	t.Run("SubFB references Calc", func(t *testing.T) {
		bi, ok := lookup["SubFB"]
		if !ok {
			t.Fatal("SubFB not found in results")
		}
		if len(bi.Use) != 1 {
			t.Fatalf("SubFB.Use count = %d, want 1", len(bi.Use))
		}
		if bi.Use[0].Name != "Calc" {
			t.Errorf("SubFB.Use[0].Name = %q, want Calc", bi.Use[0].Name)
		}
	})

	t.Run("Calc has no refs", func(t *testing.T) {
		bi, ok := lookup["Calc"]
		if !ok {
			t.Fatal("Calc not found in results")
		}
		if len(bi.Use) != 0 {
			t.Errorf("Calc.Use count = %d, want 0", len(bi.Use))
		}
	})

	t.Run("Stats has no refs", func(t *testing.T) {
		bi, ok := lookup["Stats"]
		if !ok {
			t.Fatal("Stats not found in results")
		}
		if bi.Type != "DI" {
			t.Errorf("Stats type = %q, want DI", bi.Type)
		}
	})
}

func TestScanBlockRefs_Deduplicate(t *testing.T) {
	dir := t.TempDir()

	files := []string{
		writeBlock(t, dir, "Caller.scl", `FUNCTION "Caller"
  "Target";
  "Target";
  "Target";
END_FUNCTION`),
		writeBlock(t, dir, "Target.scl", `FUNCTION "Target"
END_FUNCTION`),
	}

	names := collectBlockNames(files)
	re := buildRefRegex(names)
	results := scanBlockRefs(files, re)

	for _, r := range results {
		if r.Name == "Caller" {
			if len(r.Use) != 1 {
				t.Errorf("Caller.Use should have 1 unique ref, got %d", len(r.Use))
			}
			return
		}
	}
	t.Error("Caller not found in results")
}

func TestScanBlockRefs_UnreadableFile(t *testing.T) {
	dir := t.TempDir()

	validPath := writeBlock(t, dir, "Good.scl", `FUNCTION "Good"
END_FUNCTION`)
	badPath := filepath.Join(dir, "Bad.scl")
	if err := os.Mkdir(badPath, 0644); err != nil {
		t.Fatal(err)
	}

	files := []string{validPath, badPath}
	names := collectBlockNames(files)
	re := buildRefRegex(names)

	results := scanBlockRefs(files, re)
	if len(results) != 1 {
		t.Errorf("expected 1 result (Good), got %d", len(results))
	}
}

func TestScanBlockRefs_SelfReferenceInCode(t *testing.T) {
	dir := t.TempDir()

	files := []string{
		writeBlock(t, dir, "SelfRef.scl", `FUNCTION "SelfRef"
  "SelfRef";
END_FUNCTION`),
	}

	names := collectBlockNames(files)
	re := buildRefRegex(names)
	results := scanBlockRefs(files, re)
	for _, r := range results {
		if r.Name == "SelfRef" {
			if len(r.Use) != 0 {
				t.Errorf("SelfRef.Use should be empty (self-ref skipped), got %d ref(s)", len(r.Use))
			}
			return
		}
	}
	t.Error("SelfRef not found in results")
}

func TestScanBlockRefs_SelfReferenceInComment(t *testing.T) {
	dir := t.TempDir()

	files := []string{
		writeBlock(t, dir, "A.scl", `FUNCTION "A"
  "B";
  // "A" in comment only
END_FUNCTION`),
		writeBlock(t, dir, "B.scl", `FUNCTION "B"
END_FUNCTION`),
	}

	names := collectBlockNames(files)
	re := buildRefRegex(names)
	results := scanBlockRefs(files, re)
	lookup := make(map[string]blockInfo)
	for _, r := range results {
		lookup[r.Name] = r
	}
	if bi, ok := lookup["A"]; ok {
		if len(bi.Use) != 1 || bi.Use[0].Name != "B" {
			t.Errorf("A.Use should have 1 ref (B), got %v", bi.Use)
		}
	} else {
		t.Error("A not found in results")
	}
}

// ---------- walkFiles ----------

func TestWalkFiles(t *testing.T) {
	dir := t.TempDir()

	writeBlock(t, dir, "Main.db", "")
	writeBlock(t, filepath.Join(dir, "sub"), "SubFB.scl", "")
	writeBlock(t, dir, "notes.txt", "")
	writeBlock(t, filepath.Join(dir, "sub"), "ignore.json", "")

	result, err := walkFiles([]string{dir})
	if err != nil {
		t.Fatalf("walkFiles() error: %v", err)
	}

	sort.Strings(result)

	expected := []string{
		filepath.Join(dir, "Main.db"),
		filepath.Join(dir, "sub", "SubFB.scl"),
	}
	sort.Strings(expected)

	if len(result) != len(expected) {
		t.Fatalf("walkFiles() returned %d files: %v, want %d: %v", len(result), result, len(expected), expected)
	}
	for i := range result {
		if result[i] != expected[i] {
			t.Errorf("walkFiles()[%d] = %q, want %q", i, result[i], expected[i])
		}
	}
}

func TestWalkFiles_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	result, err := walkFiles([]string{dir})
	if err != nil {
		t.Fatalf("walkFiles() error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 files in empty dir, got %d", len(result))
	}
}

func TestWalkFiles_NonExistentDirectory(t *testing.T) {
	_, err := walkFiles([]string{"/nonexistent/path"})
	if err == nil {
		t.Error("expected error for non-existent directory, got nil")
	}
}

func TestWalkFiles_MultipleDirectories(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	writeBlock(t, dir1, "A.db", "")
	writeBlock(t, dir2, "B.db", "")
	writeBlock(t, dir2, "C.udt", "")

	result, err := walkFiles([]string{dir1, dir2})
	if err != nil {
		t.Fatalf("walkFiles() error: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("expected 3 files from two dirs, got %d: %v", len(result), result)
	}
}

// ---------- parseFlags ----------

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		wantOutputFile        string
		wantScanPaths         []string
		wantHelp, wantVersion bool
	}{
		{
			name:           "all flags provided",
			args:           []string{"cmd", "-o", "out.json", "-p", "./blocks"},
			wantOutputFile: "out.json",
			wantScanPaths:  []string{"./blocks"},
		},
		{
			name:           "multiple scan paths",
			args:           []string{"cmd", "-o", "out.json", "-p", "./udts", "-p", "./fcs"},
			wantOutputFile: "out.json",
			wantScanPaths:  []string{"./udts", "./fcs"},
		},
		{
			name:           "long flags",
			args:           []string{"cmd", "--output-file", "out.json", "--scan-path", "./blocks"},
			wantOutputFile: "out.json",
			wantScanPaths:  []string{"./blocks"},
		},
		{
			name:     "help flag",
			args:     []string{"cmd", "-h"},
			wantHelp: true,
		},
		{
			name:        "version flag",
			args:        []string{"cmd", "-v"},
			wantVersion: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldArgs := os.Args
			defer func() { os.Args = oldArgs }()
			os.Args = tt.args

			// Reset pflag to prevent "flag redefined" error across sub-tests
			pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)

			outputFile, scanPaths, help, showVersion := parseFlags()

			if outputFile != tt.wantOutputFile {
				t.Errorf("outputFile = %q, want %q", outputFile, tt.wantOutputFile)
			}
			if !stringSliceEqual(scanPaths, tt.wantScanPaths) {
				t.Errorf("scanPaths = %v, want %v", scanPaths, tt.wantScanPaths)
			}
			if help != tt.wantHelp {
				t.Errorf("help = %v, want %v", help, tt.wantHelp)
			}
			if showVersion != tt.wantVersion {
				t.Errorf("version = %v, want %v", showVersion, tt.wantVersion)
			}
		})
	}
}

// ---------- Helpers ----------

func writeBlock(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func stringSliceEqual(a, b []string) bool {
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
