package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	flag "github.com/spf13/pflag"
	"github.com/tdewolff/minify/v2"
	mincss "github.com/tdewolff/minify/v2/css"
	minhtml "github.com/tdewolff/minify/v2/html"
	minjs "github.com/tdewolff/minify/v2/js"
	minjson "github.com/tdewolff/minify/v2/json"

	"golang.org/x/oauth2"
)

const batchSize = 50

var version = "dev"

var reRepoData = regexp.MustCompile(`(?s)// __REPO_DATA_START__.*?// __REPO_DATA_END__`)

var apiBase = "https://api.github.com/graphql"

type batchTarget struct {
	owner string
	name  string
	idx   int
}

type InputRepo struct {
	Name     string   `json:"name"`
	Url      string   `json:"url"`
	Type     []string `json:"type"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
}

func (in InputRepo) toOutputRepo() OutputRepo {
	return OutputRepo{
		Name:     in.Name,
		Url:      in.Url,
		Type:     in.Type,
		Category: in.Category,
		Tags:     in.Tags,
	}
}

type OutputRepo struct {
	Name           string   `json:"name"`
	Url            string   `json:"url"`
	Type           []string `json:"type"`
	Category       string   `json:"category"`
	Tags           []string `json:"tags"`
	NameWithOwner  string   `json:"name_with_owner"`
	Description    string   `json:"description"`
	StargazerCount int      `json:"star_count"`
	ForkCount      int      `json:"fork_count"`
	UpdatedAt      string   `json:"updated_at"`
	CreatedAt      string   `json:"created_at"`
	PushedAt       string   `json:"pushed_at"`
	IsArchived     bool     `json:"is_archived"`
	Languages      []string `json:"languages"`
}

type graphqlRepoFields struct {
	NameWithOwner  string `json:"nameWithOwner"`
	Description    string `json:"description"`
	Url            string `json:"url"`
	StargazerCount int    `json:"stargazerCount"`
	ForkCount      int    `json:"forkCount"`
	UpdatedAt      string `json:"updatedAt"`
	CreatedAt      string `json:"createdAt"`
	PushedAt       string `json:"pushedAt"`
	IsArchived     bool   `json:"isArchived"`
	Languages      struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"languages"`
}

func (graphqlRepoFields) queryFragment() string {
	return `    nameWithOwner
    description
    url
    stargazerCount
    forkCount
    updatedAt
    createdAt
    pushedAt
    isArchived
    languages(first: 5) {
      nodes {
        name
      }
    }`
}

func (f graphqlRepoFields) toOutputRepo(input InputRepo) OutputRepo {
	out := input.toOutputRepo()
	out.NameWithOwner = f.NameWithOwner
	out.Description = f.Description
	out.StargazerCount = f.StargazerCount
	out.ForkCount = f.ForkCount
	out.UpdatedAt = f.UpdatedAt
	out.CreatedAt = f.CreatedAt
	out.PushedAt = f.PushedAt
	out.IsArchived = f.IsArchived
	out.Languages = make([]string, 0, len(f.Languages.Nodes))
	for _, n := range f.Languages.Nodes {
		out.Languages = append(out.Languages, n.Name)
	}
	return out
}

var queryFragmentCached = graphqlRepoFields{}.queryFragment()

func parseOwnerRepo(rawURL string) (owner, name string, err error) {
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("parsing url: %w", err)
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("url does not contain owner/repo: %s", rawURL)
	}
	owner = parts[0]
	name = strings.TrimSuffix(parts[1], ".git")
	name = strings.TrimRight(name, "/")
	return
}

type graphQLError struct {
	Message string   `json:"message"`
	Type    string   `json:"type"`
	Path    []string `json:"path"`
}

type graphQLResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []graphQLError             `json:"errors"`
}

func doGraphQLQuery(ctx context.Context, client *http.Client, query string) (*graphQLResponse, error) {
	const maxRetries = 3
	for i := range maxRetries {
		payload := fmt.Sprintf(`{"query":"%s"}`, escapeJSON(query))
		req, err := http.NewRequestWithContext(ctx, "POST", apiBase, strings.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			if i < maxRetries-1 {
				time.Sleep(time.Duration(1<<i) * time.Second)
				continue
			}
			return nil, fmt.Errorf("sending request: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading response: %w", err)
		}

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			if i < maxRetries-1 {
				time.Sleep(time.Duration(2<<i) * time.Second)
				continue
			}
			return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
		}

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
		}

		var result graphQLResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("unmarshaling response: %w", err)
		}
		return &result, nil
	}
	return nil, fmt.Errorf("max retries exceeded")
}

func escapeJSON(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if unicode.IsControl(r) {
				b.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func buildBatchQuery(targets []batchTarget) string {
	var b strings.Builder
	for _, t := range targets {
		fmt.Fprintf(&b, `    r%d: repository(owner: "%s", name: "%s") {
%s
    }
`, t.idx, t.owner, t.name, queryFragmentCached)
	}
	return fmt.Sprintf(`query {
%s
}`, b.String())
}

func queryAllRepos(ctx context.Context, token string, inputs []InputRepo) ([]OutputRepo, error) {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	client := oauth2.NewClient(ctx, src)

	var targets []batchTarget
	validSet := make([]bool, len(inputs))
	for i, in := range inputs {
		owner, name, err := parseOwnerRepo(in.Url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: skipping %s: %v\n", in.Url, err)
			continue
		}
		validSet[i] = true
		targets = append(targets, batchTarget{owner: owner, name: name, idx: i})
	}

	outputs := make([]OutputRepo, len(inputs))
	for i, in := range inputs {
		outputs[i] = in.toOutputRepo()
	}

	for batchStart := 0; batchStart < len(targets); batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > len(targets) {
			batchEnd = len(targets)
		}
		batch := targets[batchStart:batchEnd]

		query := buildBatchQuery(batch)
		resp, err := doGraphQLQuery(ctx, client, query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: batch query error: %v\n", err)
			continue
		}

		for _, e := range resp.Errors {
			fmt.Fprintf(os.Stderr, "  Warning: GraphQL error for %v: %s\n", e.Path, e.Message)
		}

		for _, t := range batch {
			if !validSet[t.idx] {
				continue
			}
			key := fmt.Sprintf("r%d", t.idx)
			raw, ok := resp.Data[key]
			if !ok || string(raw) == "null" {
				fmt.Fprintf(os.Stderr, "  Warning: repo not found or no access: %s\n", inputs[t.idx].Url)
				continue
			}
			var fields graphqlRepoFields
			if err := json.Unmarshal(raw, &fields); err != nil {
				continue
			}
			outputs[t.idx] = fields.toOutputRepo(inputs[t.idx])
		}
	}

	return outputs, nil
}

func writeHTML(templatePath string, outputPath string, outputs []OutputRepo) error {
	htmlBytes, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("reading html template: %w", err)
	}

	if !reRepoData.Match(htmlBytes) {
		return fmt.Errorf("REPO_DATA sentinel markers not found in %s", templatePath)
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return fmt.Errorf("marshaling json for html: %w", err)
	}

	replacement := fmt.Sprintf("// __REPO_DATA_START__\n    const REPO_DATA = %s;\n    // __REPO_DATA_END__", data)
	updated := reRepoData.ReplaceAll(htmlBytes, []byte(replacement))

	var buf bytes.Buffer
	m := minify.New()
	m.AddFunc("text/html", minhtml.Minify)
	m.AddFunc("text/css", mincss.Minify)
	m.AddFunc("application/javascript", minjs.Minify)
	m.AddFunc("text/javascript", minjs.Minify)
	m.AddFunc("application/json", minjson.Minify)
	if err := m.Minify("text/html", &buf, bytes.NewReader(updated)); err != nil {
		return fmt.Errorf("minifying html: %w", err)
	}

	if err := os.WriteFile(outputPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing html file: %w", err)
	}
	fmt.Println("  HTML data embedded successfully.")
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("track-repository", flag.ContinueOnError)
	inputPath := fs.StringP("input", "i", "", "Path to input JSON file containing repos")
	outputPath := fs.StringP("output", "o", "", "Path to output JSON file for enriched repos")
	htmlTemplate := fs.StringP("html-template", "h", "", "Path to HTML template file with REPO_DATA sentinel")
	htmlOutput := fs.StringP("html-output", "H", "", "Path to write HTML file with embedded repo data (requires --html-template)")
	token := fs.StringP("token", "t", "", "GitHub personal access token")
	showVersion := fs.BoolP("version", "v", false, "Show version")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		fmt.Println("track-repository", version)
		return nil
	}

	if *inputPath == "" {
		return fmt.Errorf("--input/-i is required")
	}
	if *outputPath == "" {
		return fmt.Errorf("--output/-o is required")
	}
	if *htmlOutput != "" && *htmlTemplate == "" {
		return fmt.Errorf("--html-template is required when --html-output is set")
	}

	if *token == "" {
		*token = os.Getenv("USER_GITHUB_TOKEN")
	}

	fmt.Println("Step 1 - Parse input JSON file:", *inputPath)
	inputBytes, err := os.ReadFile(*inputPath)
	if err != nil {
		return fmt.Errorf("reading input file: %w", err)
	}
	var inputs []InputRepo
	if err := json.Unmarshal(inputBytes, &inputs); err != nil {
		return fmt.Errorf("parsing input JSON: %w", err)
	}
	fmt.Printf("  Read %d repos from input file.\n", len(inputs))

	fmt.Println("Step 2 - Query GitHub GraphQL API...")
	ctx := context.Background()
	outputs, err := queryAllRepos(ctx, *token, inputs)
	if err != nil {
		return fmt.Errorf("querying GitHub: %w", err)
	}

	fmt.Println("Step 3 - Write output JSON file:", *outputPath)
	outputBytes, err := json.MarshalIndent(outputs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling output: %w", err)
	}
	if err := os.WriteFile(*outputPath, outputBytes, 0644); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}
	fmt.Printf("  Written %d repos to output file.\n", len(outputs))

	if *htmlOutput != "" {
		if err := writeHTML(*htmlTemplate, *htmlOutput, outputs); err != nil {
			return fmt.Errorf("writing output html: %w", err)
		}
	} else {
		fmt.Println("Step 4 - Skipped (no --html-output specified)")
	}

	fmt.Println("Finished!")
	return nil
}
