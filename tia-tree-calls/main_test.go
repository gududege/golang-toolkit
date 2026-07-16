package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// ---------- buildBlockMap ----------

func TestBuildBlockMap(t *testing.T) {
	blocks := []blockInfo{
		{Name: "OB_Main", Type: "OB", Use: []blockRef{{Name: "FB_Init", Type: "FB"}}},
		{Name: "FB_Init", Type: "FB", Use: []blockRef{{Name: "FC_Util", Type: "FC"}}},
		{Name: "FC_Util", Type: "FC", Use: nil},
		{Name: "DB_Data", Type: "DB", Use: nil},
	}

	m := buildBlockMap(blocks)

	if len(m) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(m))
	}

	tests := []struct {
		name string
		typ  string
		use  int
	}{
		{"OB_Main", "OB", 1},
		{"FB_Init", "FB", 1},
		{"FC_Util", "FC", 0},
		{"DB_Data", "DB", 0},
	}

	for _, tc := range tests {
		node, ok := m[tc.name]
		if !ok {
			t.Errorf("expected block %q to exist", tc.name)
			continue
		}
		if node.Type != tc.typ {
			t.Errorf("block %q type = %q, want %q", tc.name, node.Type, tc.typ)
		}
		if len(node.Use) != tc.use {
			t.Errorf("block %q Use count = %d, want %d", tc.name, len(node.Use), tc.use)
		}
	}

	// Verify that blockRef.Type is discarded in blockNode.Use
	if m["OB_Main"].Use[0] != "FB_Init" {
		t.Errorf("OB_Main Use[0] = %q, want %q", m["OB_Main"].Use[0], "FB_Init")
	}
}

func TestBuildBlockMap_Empty(t *testing.T) {
	m := buildBlockMap(nil)
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

// ---------- resolveEntries ----------

func TestResolveEntries_WithEntryFlag(t *testing.T) {
	blocks := []blockInfo{
		{Name: "OB_Main", Type: "OB", Use: []blockRef{{Name: "FB_Init", Type: "FB"}}},
		{Name: "FB_Init", Type: "FB", Use: nil},
	}
	m := buildBlockMap(blocks)

	entries := resolveEntries("OB_Main", blocks, m, nil)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(entries), entries)
	}
	if entries[0] != "OB_Main" {
		t.Errorf("entry = %q, want %q", entries[0], "OB_Main")
	}
}

func TestResolveEntries_WithMultipleEntries(t *testing.T) {
	blocks := []blockInfo{
		{Name: "OB_Main", Type: "OB", Use: nil},
		{Name: "OB_Cycle", Type: "OB", Use: nil},
	}
	m := buildBlockMap(blocks)

	entries := resolveEntries("OB_Main, OB_Cycle", blocks, m, nil)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}
	if entries[0] != "OB_Main" {
		t.Errorf("entries[0] = %q, want %q", entries[0], "OB_Main")
	}
	if entries[1] != "OB_Cycle" {
		t.Errorf("entries[1] = %q, want %q", entries[1], "OB_Cycle")
	}
}

func TestResolveEntries_WithDuplicates(t *testing.T) {
	blocks := []blockInfo{
		{Name: "OB_Main", Type: "OB", Use: nil},
	}
	m := buildBlockMap(blocks)

	entries := resolveEntries("OB_Main,OB_Main", blocks, m, nil)

	if len(entries) != 1 {
		t.Errorf("expected 1 unique entry, got %d: %v", len(entries), entries)
	}
}

func TestResolveEntries_TopLevel(t *testing.T) {
	blocks := []blockInfo{
		{Name: "OB_Main", Type: "OB", Use: []blockRef{{Name: "FB_Init", Type: "FB"}, {Name: "FC_Util", Type: "FC"}}},
		{Name: "FB_Init", Type: "FB", Use: []blockRef{{Name: "FC_Util", Type: "FC"}}},
		{Name: "FC_Util", Type: "FC", Use: nil},
	}
	m := buildBlockMap(blocks)

	entries := resolveEntries("", blocks, m, nil)

	// Only OB_Main is not called by anyone
	if len(entries) != 1 {
		t.Fatalf("expected 1 top-level entry, got %d: %v", len(entries), entries)
	}
	if entries[0] != "OB_Main" {
		t.Errorf("entry = %q, want %q", entries[0], "OB_Main")
	}
}

func TestResolveEntries_TypeFilter(t *testing.T) {
	blocks := []blockInfo{
		{Name: "OB_Main", Type: "OB", Use: nil},
		{Name: "DB_Data", Type: "DB", Use: nil},
	}
	m := buildBlockMap(blocks)

	entries := resolveEntries("OB_Main,DB_Data", blocks, m, []string{"OB"})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after type filter, got %d: %v", len(entries), entries)
	}
	if entries[0] != "OB_Main" {
		t.Errorf("entry = %q, want %q", entries[0], "OB_Main")
	}
}

func TestResolveEntries_TypeFilterEmptyResult(t *testing.T) {
	blocks := []blockInfo{
		{Name: "OB_Main", Type: "OB", Use: nil},
	}
	m := buildBlockMap(blocks)

	entries := resolveEntries("OB_Main", blocks, m, []string{"FC"})

	if len(entries) != 0 {
		t.Errorf("expected 0 entries after non-matching filter, got %d: %v", len(entries), entries)
	}
}

// resolveEntries returns entries not in blockMap as-is — main() handles the warning.
func TestResolveEntries_EntryNotInBlockMap(t *testing.T) {
	blocks := []blockInfo{
		{Name: "OB_Main", Type: "OB", Use: nil},
	}
	m := buildBlockMap(blocks)

	entries := resolveEntries("NonExistent", blocks, m, nil)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(entries), entries)
	}
	if entries[0] != "NonExistent" {
		t.Errorf("entry = %q, want %q", entries[0], "NonExistent")
	}
}

// ---------- buildTree ----------

func TestBuildTree_Simple(t *testing.T) {
	m := map[string]blockNode{
		"OB_Main": {Type: "OB", Use: []string{"FB_Init"}},
		"FB_Init": {Type: "FB", Use: nil},
	}

	lines := buildTree(treeParams{
		Name:       "OB_Main",
		Ancestors:  nil,
		Indent:     "",
		IsLast:     false,
		Depth:      0,
		MaxDepth:   0,
		TypeFilter: nil,
		BlockMap:   m,
	})

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "OB_Main" {
		t.Errorf("line[0] = %q, want %q", lines[0], "OB_Main")
	}
	if lines[1] != "└── FB_Init" {
		t.Errorf("line[1] = %q, want %q", lines[1], "└── FB_Init")
	}
}

func TestBuildTree_DepthLimit(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "OB", Use: []string{"B"}},
		"B": {Type: "FB", Use: []string{"C"}},
		"C": {Type: "FC", Use: nil},
	}

	lines := buildTree(treeParams{
		Name:       "A",
		Ancestors:  nil,
		Indent:     "",
		IsLast:     false,
		Depth:      0,
		MaxDepth:   1,
		TypeFilter: nil,
		BlockMap:   m,
	})

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (depth 1), got %d: %v", len(lines), lines)
	}
	if lines[0] != "A" {
		t.Errorf("line[0] = %q, want %q", lines[0], "A")
	}
	if lines[1] != "└── B" {
		t.Errorf("line[1] = %q, want %q", lines[1], "└── B")
	}
}

func TestBuildTree_TypeFilter(t *testing.T) {
	m := map[string]blockNode{
		"OB_Main": {Type: "OB", Use: []string{"FB_Init", "FC_Util"}},
		"FB_Init": {Type: "FB", Use: nil},
		"FC_Util": {Type: "FC", Use: nil},
	}

	lines := buildTree(treeParams{
		Name:       "OB_Main",
		Ancestors:  nil,
		Indent:     "",
		IsLast:     false,
		Depth:      0,
		MaxDepth:   0,
		TypeFilter: []string{"FB"},
		BlockMap:   m,
	})

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (FB only), got %d: %v", len(lines), lines)
	}
	if lines[1] != "└── FB_Init" {
		t.Errorf("line[1] = %q, want %q", lines[1], "└── FB_Init")
	}
}

// A type filter silently drops children not found in BlockMap.
func TestBuildTree_TypeFilterChildMissing(t *testing.T) {
	m := map[string]blockNode{
		"OB_Main": {Type: "OB", Use: []string{"FB_Init", "Missing"}},
		"FB_Init": {Type: "FB", Use: nil},
	}

	lines := buildTree(treeParams{
		Name:       "OB_Main",
		Ancestors:  nil,
		Indent:     "",
		IsLast:     false,
		Depth:      0,
		MaxDepth:   0,
		TypeFilter: []string{"FB"},
		BlockMap:   m,
	})

	// "Missing" is not in BlockMap and has no type — it should be excluded by the filter
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[1] != "└── FB_Init" {
		t.Errorf("line[1] = %q, want %q", lines[1], "└── FB_Init")
	}
}

func TestBuildTree_MultipleChildren(t *testing.T) {
	m := map[string]blockNode{
		"OB_Main": {Type: "OB", Use: []string{"FB_Init", "FC_Util"}},
		"FB_Init": {Type: "FB", Use: nil},
		"FC_Util": {Type: "FC", Use: nil},
	}

	lines := buildTree(treeParams{
		Name:       "OB_Main",
		Ancestors:  nil,
		Indent:     "",
		IsLast:     false,
		Depth:      0,
		MaxDepth:   0,
		TypeFilter: nil,
		BlockMap:   m,
	})

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[1] != "├── FB_Init" {
		t.Errorf("line[1] = %q, want %q", lines[1], "├── FB_Init")
	}
	if lines[2] != "└── FC_Util" {
		t.Errorf("line[2] = %q, want %q", lines[2], "└── FC_Util")
	}
}

func TestBuildTree_MissingBlock(t *testing.T) {
	m := map[string]blockNode{
		"OB_Main": {Type: "OB", Use: []string{"FB_Missing"}},
	}

	lines := buildTree(treeParams{
		Name:       "OB_Main",
		Ancestors:  nil,
		Indent:     "",
		IsLast:     false,
		Depth:      0,
		MaxDepth:   0,
		TypeFilter: nil,
		BlockMap:   m,
	})

	// Missing child is still rendered as a leaf (no expansion)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[1] != "└── FB_Missing" {
		t.Errorf("line[1] = %q, want %q", lines[1], "└── FB_Missing")
	}
}

// ---------- Circular reference tests ----------

func TestBuildTree_SelfReference(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "FB", Use: []string{"A"}},
	}

	var refs []string
	lines := buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   nil,
		BlockMap:     m,
		CircularRefs: &refs,
	})

	if len(lines) != 1 {
		t.Fatalf("expected 1 line (root only), got %d: %v", len(lines), lines)
	}
	if lines[0] != "A" {
		t.Errorf("line[0] = %q, want %q", lines[0], "A")
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 circular ref warning, got %d: %v", len(refs), refs)
	}
	if refs[0] != "A -> A" {
		t.Errorf("ref[0] = %q, want %q", refs[0], "A -> A")
	}
}

func TestBuildTree_ParentReference(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "OB", Use: []string{"B"}},
		"B": {Type: "FB", Use: []string{"A"}},
	}

	var refs []string
	lines := buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   nil,
		BlockMap:     m,
		CircularRefs: &refs,
	})

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (A, └── B), got %d: %v", len(lines), lines)
	}
	if lines[0] != "A" {
		t.Errorf("line[0] = %q, want %q", lines[0], "A")
	}
	if lines[1] != "└── B" {
		t.Errorf("line[1] = %q, want %q", lines[1], "└── B")
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 circular ref warning, got %d: %v", len(refs), refs)
	}
	if refs[0] != "A -> B -> A" {
		t.Errorf("ref[0] = %q, want %q", refs[0], "A -> B -> A")
	}
}

func TestBuildTree_GrandparentReference(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "OB", Use: []string{"B"}},
		"B": {Type: "FB", Use: []string{"C"}},
		"C": {Type: "FC", Use: []string{"A"}},
	}

	var refs []string
	lines := buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   nil,
		BlockMap:     m,
		CircularRefs: &refs,
	})

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (A, └── B,     └── C), got %d: %v", len(lines), lines)
	}
	if lines[0] != "A" {
		t.Errorf("line[0] = %q, want %q", lines[0], "A")
	}
	if lines[1] != "└── B" {
		t.Errorf("line[1] = %q, want %q", lines[1], "└── B")
	}
	if lines[2] != "    └── C" {
		t.Errorf("line[2] = %q, want %q", lines[2], "    └── C")
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 circular ref warning, got %d: %v", len(refs), refs)
	}
	if refs[0] != "A -> B -> C -> A" {
		t.Errorf("ref[0] = %q, want %q", refs[0], "A -> B -> C -> A")
	}
}

func TestBuildTree_MultipleCircularRefs(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "OB", Use: []string{"B", "C"}},
		"B": {Type: "FB", Use: []string{"A"}},
		"C": {Type: "FC", Use: []string{"C"}},
	}

	var refs []string
	lines := buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   nil,
		BlockMap:     m,
		CircularRefs: &refs,
	})

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 circular ref warnings, got %d: %v", len(refs), refs)
	}
}

func TestBuildTree_NoWarningsWhenNil(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "FB", Use: []string{"A"}},
	}

	lines := buildTree(treeParams{
		Name:       "A",
		Ancestors:  nil,
		Indent:     "",
		IsLast:     false,
		Depth:      0,
		MaxDepth:   0,
		TypeFilter: nil,
		BlockMap:   m,
	})

	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(lines), lines)
	}
}

func TestBuildTree_SiblingNotAffectedByCircularRef(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "OB", Use: []string{"B", "D"}},
		"B": {Type: "FB", Use: []string{"C"}},
		"C": {Type: "FC", Use: []string{"B"}},
		"D": {Type: "FC", Use: nil},
	}

	var refs []string
	lines := buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   nil,
		BlockMap:     m,
		CircularRefs: &refs,
	})

	if len(lines) < 4 {
		t.Fatalf("expected >=4 lines, got %d: %v", len(lines), lines)
	}
	if lines[3] != "└── D" {
		t.Errorf("last line = %q, want %q", lines[3], "└── D")
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 circular ref warning, got %d: %v", len(refs), refs)
	}
}

func TestBuildTree_CircularRefDeduplicated(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "OB", Use: []string{"B"}},
		"B": {Type: "FB", Use: []string{"A"}},
	}

	var refs []string
	buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   nil,
		BlockMap:     m,
		CircularRefs: &refs,
	})
	buildTree(treeParams{
		Name:         "B",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   nil,
		BlockMap:     m,
		CircularRefs: &refs,
	})

	if len(refs) != 2 {
		t.Fatalf("expected 2 circular ref warnings, got %d: %v", len(refs), refs)
	}
}

// ---------- Type filter + Circular reference ----------

func TestBuildTree_TypeFilterWithCircularRef(t *testing.T) {
	// A(FB) -> B(FB, circular B->A, type filter FB matches both)
	// Type filter = FB, so A's children = [B], B's children = [A] (type OB would be filtered, but here both are FB)
	m := map[string]blockNode{
		"A": {Type: "FB", Use: []string{"B"}},
		"B": {Type: "FB", Use: []string{"A"}},
	}

	var refs []string
	lines := buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   []string{"FB"},
		BlockMap:     m,
		CircularRefs: &refs,
	})

	// A, then B (both pass FB filter), B->A is circular, so B has no children
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (A, B), got %d: %v", len(lines), lines)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 circular ref warning, got %d: %v", len(refs), refs)
	}
	if refs[0] != "A -> B -> A" {
		t.Errorf("ref[0] = %q, want %q", refs[0], "A -> B -> A")
	}
}

func TestBuildTree_TypeFilterExcludesCircularNode(t *testing.T) {
	m := map[string]blockNode{
		"A": {Type: "OB", Use: []string{"B", "C"}},
		"B": {Type: "FB", Use: []string{"A"}},
		"C": {Type: "FC", Use: nil},
	}

	var refs []string
	lines := buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     0,
		TypeFilter:   []string{"FC"},
		BlockMap:     m,
		CircularRefs: &refs,
	})

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (A, C), got %d: %v", len(lines), lines)
	}
	if lines[1] != "└── C" {
		t.Errorf("line[1] = %q, want %q", lines[1], "└── C")
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 circular refs (B excluded by filter), got %d: %v", len(refs), refs)
	}
}

func TestBuildTree_CircularRefWithDepthLimit(t *testing.T) {
	// A -> B -> C -> A (circular at grandparent)
	// Depth limit = 2, so A(depth0), B(depth1), C(depth2) are printed.
	// C's children are not expanded (depth >= maxDepth).
	m := map[string]blockNode{
		"A": {Type: "OB", Use: []string{"B"}},
		"B": {Type: "FB", Use: []string{"C"}},
		"C": {Type: "FC", Use: []string{"A"}},
	}

	var refs []string
	lines := buildTree(treeParams{
		Name:         "A",
		Ancestors:    nil,
		Indent:       "",
		IsLast:       false,
		Depth:        0,
		MaxDepth:     2,
		TypeFilter:   nil,
		BlockMap:     m,
		CircularRefs: &refs,
	})

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (A, B, C at depth 2), got %d: %v", len(lines), lines)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 circular refs (depth limit prevents reaching it), got %d: %v", len(refs), refs)
	}
}

// ---------- readBlocks ----------

func TestReadBlocks_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	data := `[
		{"name": "OB_Main", "type": "OB", "use": [{"name": "FB_Init", "type": "FB"}]}
	]`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	blocks, err := readBlocks(path)
	if err != nil {
		t.Fatalf("readBlocks failed: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Name != "OB_Main" {
		t.Errorf("block.Name = %q, want %q", blocks[0].Name, "OB_Main")
	}
	if len(blocks[0].Use) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(blocks[0].Use))
	}
	if blocks[0].Use[0].Name != "FB_Init" {
		t.Errorf("ref.Name = %q, want %q", blocks[0].Use[0].Name, "FB_Init")
	}
}

func TestReadBlocks_InvalidFile(t *testing.T) {
	_, err := readBlocks(" nonexistent file that won't exist ")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestReadBlocks_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{bad json}"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := readBlocks(path)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestReadBlocks_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte("[]"), 0644); err != nil {
		t.Fatal(err)
	}

	blocks, err := readBlocks(path)
	if err != nil {
		t.Fatalf("readBlocks failed: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
}

// ---------- parseFlags ----------

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name                  string
		args                  []string
		wantInputFile         string
		wantEntry             string
		wantOutputFile        string
		wantTypeFilter        string
		wantLevel             int
		wantHelp, wantVersion bool
	}{
		{
			name:          "input file only",
			args:          []string{"tia-tree-calls", "-i", "in.json"},
			wantInputFile: "in.json",
		},
		{
			name:          "entry flag",
			args:          []string{"tia-tree-calls", "-i", "in.json", "-e", "OB_Main"},
			wantInputFile: "in.json",
			wantEntry:     "OB_Main",
		},
		{
			name:           "output file",
			args:           []string{"tia-tree-calls", "-i", "in.json", "-o", "tree.txt"},
			wantInputFile:  "in.json",
			wantOutputFile: "tree.txt",
		},
		{
			name:          "level flag",
			args:          []string{"tia-tree-calls", "-i", "in.json", "-l", "2"},
			wantInputFile: "in.json",
			wantLevel:     2,
		},
		{
			name:           "type filter",
			args:           []string{"tia-tree-calls", "-i", "in.json", "-t", "FB,FC"},
			wantInputFile:  "in.json",
			wantTypeFilter: "FB,FC",
		},
		{
			name:            "all flags",
			args:            []string{"prog", "-i", "data.json", "-e", "OB_Main", "-o", "out.txt", "-t", "FB", "-l", "3"},
			wantInputFile:   "data.json",
			wantEntry:       "OB_Main",
			wantOutputFile:  "out.txt",
			wantTypeFilter:  "FB",
			wantLevel:       3,
		},
		{
			name:        "help flag",
			args:        []string{"cmd", "-h"},
			wantHelp:    true,
		},
		{
			name:        "version flag",
			args:        []string{"cmd", "-v"},
			wantVersion: true,
		},
		{
			name:            "long flags",
			args:            []string{"app", "--input-file", "in.json", "--entry", "OB_Main", "--output-file", "out.txt", "--type", "FC", "--level", "1"},
			wantInputFile:   "in.json",
			wantEntry:       "OB_Main",
			wantOutputFile:  "out.txt",
			wantTypeFilter:  "FC",
			wantLevel:       1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldArgs := os.Args
			defer func() { os.Args = oldArgs }()
			os.Args = tt.args

			pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)

			f := parseFlags()

			if f.inputFile != tt.wantInputFile {
				t.Errorf("inputFile = %q, want %q", f.inputFile, tt.wantInputFile)
			}
			if f.entry != tt.wantEntry {
				t.Errorf("entry = %q, want %q", f.entry, tt.wantEntry)
			}
			if f.outputFile != tt.wantOutputFile {
				t.Errorf("outputFile = %q, want %q", f.outputFile, tt.wantOutputFile)
			}
			if f.typeFilter != tt.wantTypeFilter {
				t.Errorf("typeFilter = %q, want %q", f.typeFilter, tt.wantTypeFilter)
			}
			if f.level != tt.wantLevel {
				t.Errorf("level = %d, want %d", f.level, tt.wantLevel)
			}
			if f.help != tt.wantHelp {
				t.Errorf("help = %v, want %v", f.help, tt.wantHelp)
			}
			if f.showVersion != tt.wantVersion {
				t.Errorf("version = %v, want %v", f.showVersion, tt.wantVersion)
			}
		})
	}
}

// ---------- run() ----------

func TestRun_LevelBelowZero(t *testing.T) {
	err := run(cliFlags{level: -1})
	if err == nil {
		t.Error("expected error for level < 0, got nil")
	}
}

func TestRun_MissingInputFile(t *testing.T) {
	err := run(cliFlags{inputFile: ""})
	if err == nil {
		t.Error("expected error for empty input file, got nil")
	}
}

func TestRun_ReadFileError(t *testing.T) {
	err := run(cliFlags{inputFile: "nonexistent_file.json"})
	if err == nil {
		t.Error("expected error for nonexistent input file, got nil")
	}
}

func TestRun_TypeFilterNoMatch(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "data.json")
	data := `[
		{"name": "OB_Main", "type": "OB", "use": [{"name": "FB_Init", "type": "FB"}]}
	]`
	if err := os.WriteFile(input, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	err := run(cliFlags{inputFile: input, typeFilter: "FC"})
	if err == nil {
		t.Error("expected error for type filter with no match, got nil")
	}
}

func TestRun_EntryNotInBlockMap(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "data.json")
	data := `[
		{"name": "OB_Main", "type": "OB", "use": []}
	]`
	if err := os.WriteFile(input, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	_, stderr := captureOutput(t, func() {
		err := run(cliFlags{inputFile: input, entry: "NonExistent"})
		if err != nil {
			t.Errorf("run() should not return error for missing entry, got: %v", err)
		}
	})

	if !strings.Contains(stderr, "entry block not found") {
		t.Errorf("expected warning on stderr, got: %s", stderr)
	}
}

func TestRun_OutputFile(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "data.json")
	output := filepath.Join(dir, "out.txt")
	data := `[
		{"name": "OB_Main", "type": "OB", "use": [{"name": "FB_Init", "type": "FB"}]},
		{"name": "FB_Init", "type": "FB", "use": []}
	]`
	if err := os.WriteFile(input, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureOutput(t, func() {
		err := run(cliFlags{inputFile: input, outputFile: output})
		if err != nil {
			t.Errorf("run() unexpected error: %v", err)
		}
	})

	if !strings.Contains(stdout, "Written to") {
		t.Errorf("expected 'Written to' on stdout, got: %s", stdout)
	}

	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "OB_Main") {
		t.Errorf("output file missing OB_Main, got: %s", string(content))
	}
	if !strings.Contains(string(content), "FB_Init") {
		t.Errorf("output file missing FB_Init, got: %s", string(content))
	}
}

func TestRun_StdoutOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "data.json")
	data := `[
		{"name": "OB_Main", "type": "OB", "use": []}
	]`
	if err := os.WriteFile(input, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureOutput(t, func() {
		err := run(cliFlags{inputFile: input})
		if err != nil {
			t.Errorf("run() unexpected error: %v", err)
		}
	})

	if !strings.Contains(stdout, "OB_Main") {
		t.Errorf("expected OB_Main in output, got: %s", stdout)
	}
}

// ---------- errPrinted ----------

func TestErrPrintedSentinel(t *testing.T) {
	if errPrinted == nil {
		t.Error("errPrinted should not be nil")
	}
	if errPrinted.Error() != "" {
		t.Errorf("errPrinted.Error() = %q, want empty string", errPrinted.Error())
	}
}

// ---------- captureOutput ----------

func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	oldOut := os.Stdout
	oldErr := os.Stderr

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = outW
	os.Stderr = errW

	fn()

	outW.Close()
	errW.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	var outBuf, errBuf bytes.Buffer
	io.Copy(&outBuf, outR)
	io.Copy(&errBuf, errR)

	return outBuf.String(), errBuf.String()
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
