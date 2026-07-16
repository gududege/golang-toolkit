package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOwnerRepo(t *testing.T) {
	tests := []struct {
		name      string
		rawURL    string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{"standard https", "https://github.com/owner/repo", "owner", "repo", false},
		{"without scheme", "github.com/owner/repo", "owner", "repo", false},
		{"with .git suffix", "https://github.com/owner/repo.git", "owner", "repo", false},
		{"without scheme and .git", "github.com/owner/repo.git", "owner", "repo", false},
		{"nested path", "https://github.com/org/project/sub", "org", "project/sub", false},
		{"empty url", "", "", "", true},
		{"no repo path", "https://github.com", "", "", true},
		{"single segment", "https://github.com/onlyowner", "", "", true},
		{"with trailing slash", "https://github.com/owner/repo/", "owner", "repo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, name, err := parseOwnerRepo(tt.rawURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseOwnerRepo() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if owner != tt.wantOwner {
				t.Errorf("parseOwnerRepo() owner = %q, want %q", owner, tt.wantOwner)
			}
			if name != tt.wantName {
				t.Errorf("parseOwnerRepo() name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

func TestEscapeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello", "hello"},
		{"newline", "line1\nline2", "line1\\nline2"},
		{"carriage return", "line1\rline2", "line1\\rline2"},
		{"tab", "col1\tcol2", "col1\\tcol2"},
		{"double quote", `say "hello"`, `say \"hello\"`},
		{"backslash", "a\\b", "a\\\\b"},
		{"mixed", "a\nb\"c\\d\te", `a\nb\"c\\d\te`},
		{"unicode not control", "你好", "你好"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeJSON(tt.input); got != tt.want {
				t.Errorf("escapeJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInputRepoToOutputRepo(t *testing.T) {
	in := InputRepo{
		Name:     "test-repo",
		Url:      "https://github.com/owner/test-repo",
		Type:     []string{"sdk", "library"},
		Category: "Development/Testing",
		Tags:     []string{"go", "testing"},
	}
	out := in.toOutputRepo()
	if out.Name != in.Name {
		t.Errorf("Name = %q, want %q", out.Name, in.Name)
	}
	if out.Url != in.Url {
		t.Errorf("Url = %q, want %q", out.Url, in.Url)
	}
	if len(out.Type) != 2 || out.Type[0] != "sdk" {
		t.Errorf("Type = %v, want [sdk library]", out.Type)
	}
	if out.Category != in.Category {
		t.Errorf("Category = %q, want %q", out.Category, in.Category)
	}
	// enriched fields should be zero-valued
	if out.NameWithOwner != "" {
		t.Errorf("NameWithOwner should be empty, got %q", out.NameWithOwner)
	}
	if out.StargazerCount != 0 {
		t.Errorf("StargazerCount should be 0, got %d", out.StargazerCount)
	}
}

func TestGraphqlRepoFieldsToOutputRepo(t *testing.T) {
	input := InputRepo{
		Name:     "test-repo",
		Url:      "https://github.com/owner/test-repo",
		Type:     []string{"sdk", "library"},
		Category: "Development/Testing",
		Tags:     []string{"go"},
	}
	fields := graphqlRepoFields{
		NameWithOwner:  "owner/test-repo",
		Description:    "A test repo",
		StargazerCount: 100,
		ForkCount:      10,
		UpdatedAt:      "2024-01-01T00:00:00Z",
		CreatedAt:      "2020-01-01T00:00:00Z",
		PushedAt:       "2024-06-01T00:00:00Z",
		IsArchived:     false,
		Languages: struct {
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		}{
			Nodes: []struct {
				Name string `json:"name"`
			}{
				{Name: "Go"},
				{Name: "TypeScript"},
			},
		},
	}
	out := fields.toOutputRepo(input)

	if out.NameWithOwner != "owner/test-repo" {
		t.Errorf("NameWithOwner = %q, want %q", out.NameWithOwner, "owner/test-repo")
	}
	if out.Description != "A test repo" {
		t.Errorf("Description = %q, want %q", out.Description, "A test repo")
	}
	if out.StargazerCount != 100 {
		t.Errorf("StargazerCount = %d, want 100", out.StargazerCount)
	}
	if out.ForkCount != 10 {
		t.Errorf("ForkCount = %d, want 10", out.ForkCount)
	}
	if out.IsArchived {
		t.Errorf("IsArchived should be false")
	}
	if len(out.Languages) != 2 || out.Languages[0] != "Go" {
		t.Errorf("Languages = %v, want [Go TypeScript]", out.Languages)
	}
	// input fields preserved
	if out.Name != input.Name {
		t.Errorf("Name = %q, want %q", out.Name, input.Name)
	}
}

func TestBuildBatchQuery(t *testing.T) {
	targets := []batchTarget{
		{owner: "owner1", name: "repo1", idx: 0},
		{owner: "owner2", name: "repo2", idx: 5},
	}
	query := buildBatchQuery(targets)
	if !strings.Contains(query, "r0: repository") {
		t.Error("query missing r0 alias")
	}
	if !strings.Contains(query, "r5: repository") {
		t.Error("query missing r5 alias")
	}
	if !strings.Contains(query, `owner: "owner1"`) {
		t.Error("query missing owner1")
	}
	if !strings.Contains(query, `name: "repo2"`) {
		t.Error("query missing repo2")
	}
	if !strings.HasPrefix(query, "query {") {
		t.Error("query should start with 'query {'")
	}
	if !strings.HasSuffix(strings.TrimSpace(query), "}") {
		t.Error("query should end with '}'")
	}
}

func TestQueryFragmentCached(t *testing.T) {
	fragment := queryFragmentCached
	if !strings.Contains(fragment, "nameWithOwner") {
		t.Error("fragment missing nameWithOwner")
	}
	if !strings.Contains(fragment, "stargazerCount") {
		t.Error("fragment missing stargazerCount")
	}
	if !strings.Contains(fragment, "languages") {
		t.Error("fragment missing languages")
	}
}

func TestDoGraphQLQuery_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"r0":{"nameWithOwner":"o/r"}}}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	client := http.DefaultClient
	resp, err := doGraphQLQuery(context.Background(), client, "query { r0 }")
	if err != nil {
		t.Fatalf("doGraphQLQuery() error = %v", err)
	}
	if resp.Data == nil {
		t.Fatal("resp.Data is nil")
	}
	if _, ok := resp.Data["r0"]; !ok {
		t.Error("missing r0 in data")
	}
}

func TestDoGraphQLQuery_RetryOnServerError(t *testing.T) {
	attempt := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"server error"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"r0":{"nameWithOwner":"o/r"}}}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	client := http.DefaultClient
	resp, err := doGraphQLQuery(context.Background(), client, "query { r0 }")
	if err != nil {
		t.Fatalf("doGraphQLQuery() error = %v", err)
	}
	if resp.Data == nil {
		t.Fatal("resp.Data is nil")
	}
	if attempt != 3 {
		t.Errorf("attempts = %d, want 3", attempt)
	}
}

func TestDoGraphQLQuery_RetryOnRateLimit(t *testing.T) {
	attempt := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"message":"rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"r0":{"nameWithOwner":"o/r"}}}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	client := http.DefaultClient
	resp, err := doGraphQLQuery(context.Background(), client, "query { r0 }")
	if err != nil {
		t.Fatalf("doGraphQLQuery() error = %v", err)
	}
	if resp.Data == nil {
		t.Fatal("resp.Data is nil")
	}
	if attempt != 2 {
		t.Errorf("attempts = %d, want 2", attempt)
	}
}

func TestDoGraphQLQuery_MaxRetriesExceeded(t *testing.T) {
	attempt := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"always fail"}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	client := http.DefaultClient
	_, err := doGraphQLQuery(context.Background(), client, "query { r0 }")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if attempt != 3 {
		t.Errorf("attempts = %d, want 3", attempt)
	}
}

func TestDoGraphQLQuery_ResponseWithErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"data": {"r0": null},
			"errors": [{"message": "Not found", "type": "NOT_FOUND", "path": ["r0"]}]
		}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	client := http.DefaultClient
	resp, err := doGraphQLQuery(context.Background(), client, "query { r0 }")
	if err != nil {
		t.Fatalf("doGraphQLQuery() error = %v", err)
	}
	if len(resp.Errors) == 0 {
		t.Fatal("expected errors, got none")
	}
	if resp.Errors[0].Message != "Not found" {
		t.Errorf("error message = %q, want %q", resp.Errors[0].Message, "Not found")
	}
}

func TestWriteHTML(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "template.html")
	outPath := filepath.Join(dir, "output.html")

	tmpl := `<!DOCTYPE html><html><body>
<script>
// __REPO_DATA_START__
// __REPO_DATA_END__
</script>
</body></html>`
	if err := os.WriteFile(tmplPath, []byte(tmpl), 0644); err != nil {
		t.Fatal(err)
	}

	outputs := []OutputRepo{
		{
			Name:           "test-repo",
			Url:            "https://github.com/o/r",
			NameWithOwner:  "o/r",
			StargazerCount: 100,
		},
	}

	if err := writeHTML(tmplPath, outPath, outputs); err != nil {
		t.Fatalf("writeHTML() error = %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "REPO_DATA") {
		t.Error("output missing REPO_DATA marker")
	}
	if !strings.Contains(content, "test-repo") {
		t.Error("output missing repo name")
	}
	if !strings.Contains(content, "100") {
		t.Error("output missing star count")
	}
}

func TestWriteHTML_MissingSentinel(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "template.html")

	tmpl := `<!DOCTYPE html><html><body>no sentinel here</body></html>`
	if err := os.WriteFile(tmplPath, []byte(tmpl), 0644); err != nil {
		t.Fatal(err)
	}

	err := writeHTML(tmplPath, "out.html", nil)
	if err == nil {
		t.Fatal("expected error for missing sentinel, got nil")
	}
}

func TestWriteHTML_InvalidTmplPath(t *testing.T) {
	err := writeHTML("/nonexistent/path", "out.html", nil)
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

func TestQueryAllRepos_EmptyInput(t *testing.T) {
	outputs, err := queryAllRepos(context.Background(), "token", nil)
	if err != nil {
		t.Fatalf("queryAllRepos() error = %v", err)
	}
	if len(outputs) != 0 {
		t.Errorf("outputs = %d, want 0", len(outputs))
	}
}

// queryAllRepos integration test with mocked HTTP
func TestQueryAllRepos_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"r0":{"nameWithOwner":"o/r","description":"desc","url":"https://github.com/o/r","stargazerCount":10,"forkCount":2,"updatedAt":"2024-01-01T00:00:00Z","createdAt":"2020-01-01T00:00:00Z","pushedAt":"2024-06-01T00:00:00Z","isArchived":false,"languages":{"nodes":[{"name":"Go"}]}}}}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	inputs := []InputRepo{
		{
			Name:     "test",
			Url:      "https://github.com/o/r",
			Type:     []string{"sdk", "library"},
			Category: "Dev",
			Tags:     []string{"go"},
		},
	}

	outputs, err := queryAllRepos(context.Background(), "token", inputs)
	if err != nil {
		t.Fatalf("queryAllRepos() error = %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("outputs = %d, want 1", len(outputs))
	}
	if outputs[0].NameWithOwner != "o/r" {
		t.Errorf("NameWithOwner = %q, want %q", outputs[0].NameWithOwner, "o/r")
	}
	if outputs[0].StargazerCount != 10 {
		t.Errorf("StargazerCount = %d, want 10", outputs[0].StargazerCount)
	}
	if len(outputs[0].Languages) != 1 || outputs[0].Languages[0] != "Go" {
		t.Errorf("Languages = %v, want [Go]", outputs[0].Languages)
	}
	// input fields preserved
	if outputs[0].Name != "test" {
		t.Errorf("Name = %q, want %q", outputs[0].Name, "test")
	}
}

func TestQueryAllRepos_InvalidURL(t *testing.T) {
	inputs := []InputRepo{
		{Name: "bad", Url: "://invalid"},
	}
	outputs, err := queryAllRepos(context.Background(), "token", inputs)
	if err != nil {
		t.Fatalf("queryAllRepos() error = %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("outputs = %d, want 1", len(outputs))
	}
	// should have been skipped, so enriched fields should be zero
	if outputs[0].StargazerCount != 0 {
		t.Errorf("StargazerCount = %d, want 0 (repo was invalid)", outputs[0].StargazerCount)
	}
}

func TestJsonRoundTrip(t *testing.T) {
	original := []InputRepo{
		{Name: "repo1", Url: "https://github.com/o/r1", Type: []string{"sdk", "library"}, Category: "Dev", Tags: []string{"go"}},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []InputRepo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || decoded[0].Name != "repo1" {
		t.Errorf("round trip failed: %+v", decoded)
	}
}

func TestEscapeJSON_ControlChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"null byte", "a\x00b", "a\\u0000b"},
		{"unit separator", "a\x1fb", "a\\u001fb"},
		{"bell", "a\x07b", "a\\u0007b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeJSON(tt.input); got != tt.want {
				t.Errorf("escapeJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDoGraphQLQuery_NonRetryHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"not allowed"}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	_, err := doGraphQLQuery(context.Background(), http.DefaultClient, "query { r0 }")
	if err == nil {
		t.Fatal("expected error for 403, got nil")
	}
}

func TestQueryAllRepos_BatchQueryFailure(t *testing.T) {
	attempt := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"server error"}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	inputs := []InputRepo{
		{Name: "r1", Url: "https://github.com/o/r1"},
		{Name: "r2", Url: "https://github.com/o/r2"},
	}
	outputs, err := queryAllRepos(context.Background(), "token", inputs)
	if err != nil {
		t.Fatalf("queryAllRepos() error = %v", err)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs = %d, want 2", len(outputs))
	}
	// should degrade gracefully: outputs exist but with zero enriched fields
	if outputs[0].NameWithOwner != "" {
		t.Errorf("NameWithOwner = %q, want empty (batch failed)", outputs[0].NameWithOwner)
	}
}

func TestQueryAllRepos_NullRepoWarning(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"r0":null,"r1":{"nameWithOwner":"o/r1","description":"","url":"","stargazerCount":0,"forkCount":0,"updatedAt":"","createdAt":"","pushedAt":"","isArchived":false,"languages":{"nodes":[]}}}}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	inputs := []InputRepo{
		{Name: "missing", Url: "https://github.com/o/missing"},
		{Name: "exists", Url: "https://github.com/o/r1"},
	}
	outputs, err := queryAllRepos(context.Background(), "token", inputs)
	if err != nil {
		t.Fatalf("queryAllRepos() error = %v", err)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs = %d, want 2", len(outputs))
	}
	// null repo should have zero enriched fields
	if outputs[0].NameWithOwner != "" {
		t.Errorf("NameWithOwner for missing repo = %q, want empty", outputs[0].NameWithOwner)
	}
	// valid repo should be enriched
	if outputs[1].NameWithOwner != "o/r1" {
		t.Errorf("NameWithOwner = %q, want %q", outputs[1].NameWithOwner, "o/r1")
	}
}

func TestQueryAllRepos_GraphQLErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"data": {"r0": {"nameWithOwner":"o/r0","description":"","url":"","stargazerCount":0,"forkCount":0,"updatedAt":"","createdAt":"","pushedAt":"","isArchived":false,"languages":{"nodes":[]}}},
			"errors": [{"message":"r1 not found","type":"NOT_FOUND","path":["r1"]}]
		}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	inputs := []InputRepo{
		{Name: "r0", Url: "https://github.com/o/r0"},
	}
	outputs, err := queryAllRepos(context.Background(), "token", inputs)
	if err != nil {
		t.Fatalf("queryAllRepos() error = %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("outputs = %d, want 1", len(outputs))
	}
	if outputs[0].NameWithOwner != "o/r0" {
		t.Errorf("NameWithOwner = %q, want %q", outputs[0].NameWithOwner, "o/r0")
	}
}

func TestRun_Version(t *testing.T) {
	err := run([]string{"--version"})
	if err != nil {
		t.Fatalf("run(--version) error = %v", err)
	}
}

func TestRun_MissingInput(t *testing.T) {
	err := run([]string{"-o", "out.json"})
	if err == nil {
		t.Fatal("expected error for missing --input, got nil")
	}
}

func TestRun_MissingOutput(t *testing.T) {
	err := run([]string{"-i", "in.json"})
	if err == nil {
		t.Fatal("expected error for missing --output, got nil")
	}
}

func TestRun_HtmlOutputWithoutTemplate(t *testing.T) {
	err := run([]string{"-i", "in.json", "-o", "out.json", "-H", "index.html"})
	if err == nil {
		t.Fatal("expected error for --html-output without --html-template, got nil")
	}
}

func TestRun_FullFlow(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.json")
	outputPath := filepath.Join(dir, "output.json")
	tmplPath := filepath.Join(dir, "template.html")
	htmlOutPath := filepath.Join(dir, "index.html")

	if err := os.WriteFile(inputPath, []byte(`[{"name":"test","url":"https://github.com/o/r","type":["sdk","library"],"category":"Dev","tags":["go"]}]`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmplPath, []byte(`<script>// __REPO_DATA_START__\n// __REPO_DATA_END__</script>`), 0644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"r0":{"nameWithOwner":"o/r","description":"desc","url":"https://github.com/o/r","stargazerCount":10,"forkCount":2,"updatedAt":"2024-01-01T00:00:00Z","createdAt":"2020-01-01T00:00:00Z","pushedAt":"2024-06-01T00:00:00Z","isArchived":false,"languages":{"nodes":[{"name":"Go"}]}}}}`)
	}))
	defer ts.Close()

	orig := apiBase
	apiBase = ts.URL
	defer func() { apiBase = orig }()

	err := run([]string{"-i", inputPath, "-o", outputPath, "-h", tmplPath, "-H", htmlOutPath})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name_with_owner": "o/r"`) {
		t.Error("output JSON missing enriched data")
	}
}

