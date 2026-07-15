# track-repository

Enrich a list of GitHub repositories with live metadata (stars, forks, languages, etc.) via the
GitHub GraphQL API, then embed the result into a searchable HTML page.

## Files

| File | Description |
|---|---|
| `main.go` | CLI entry point |
| `template.json` | Example input — classify your repos into this format |
| `template.html` | HTML template with built-in UI (search, TOC, advanced SQL filter) |
| `.goreleaser.yaml` | GoReleaser build config (3-platform, UPX) |
| `docs/github-repo-classifier.md` | AI prompt for classifying repos into the input format |

## Quick start

```bash
export USER_GITHUB_TOKEN="ghp_xxx"
cp template.json targets.json     # edit to list your repos
track-repository -i targets.json -o enriched.json \
    --html-template template.html --html-output index.html
```

Open `index.html` — you get a searchable, categorized catalog with live GitHub stats.

## Flags

| Short | Long              | Default | Description                                             |
|-------|-------------------|---------|---------------------------------------------------------|
| `-i`  | `--input`         |         | Input JSON file **(required)**                          |
| `-o`  | `--output`        |         | Output JSON file for enriched repos **(required)**      |
| `-t`  | `--token`         | env     | GitHub token (or `USER_GITHUB_TOKEN`)                   |
| `-h`  | `--html-template` |         | HTML template with `REPO_DATA` sentinel                 |
| `-H`  | `--html-output`   |         | Output HTML with embedded data (minified)               |
| `-v`  | `--version`       |         | Show version                                            |

## Input format

Input is a JSON array. Each entry specifies a repo and its classification:

```json
[
  {
    "name": "antlr4",
    "url": "https://github.com/antlr/antlr4",
    "type": ["release", "cli"],
    "category": "Development/Parsers",
    "tags": ["ANTLR", "Parsing"]
  }
]
```

| Field      | Type       | Description                              |
|------------|------------|------------------------------------------|
| `name`     | `string`   | Repository name                          |
| `url`      | `string`   | GitHub URL                               |
| `type`     | `[string]` | 2-element array: `[top_level, subtype]` |
| `category` | `string`   | Hierarchical path (`Area/Sub`)           |
| `tags`     | `[string]` | Technology/ecosystem keywords            |

See `template.json` for a complete example.

## Output format

Output includes all input fields plus enriched GitHub metadata:

```json
{
  "name": "antlr4",
  "url": "https://github.com/antlr/antlr4",
  "type": ["release", "cli"],
  "category": "Development/Parsers",
  "tags": ["ANTLR", "Parsing"],
  "name_with_owner": "antlr/antlr4",
  "description": "ANTLR (ANother Tool for Language Recognition)",
  "star_count": 17500,
  "fork_count": 3300,
  "updated_at": "2026-07-10T12:00:00Z",
  "created_at": "2011-04-01T00:00:00Z",
  "pushed_at": "2026-07-12T08:00:00Z",
  "is_archived": false,
  "languages": ["Java", "C#", "Python"]
}
```

| Enriched field     | Type       | Source                     |
|--------------------|------------|----------------------------|
| `name_with_owner`  | `string`   | GitHub GraphQL             |
| `description`      | `string`   | GitHub GraphQL             |
| `star_count`       | `int`      | GitHub GraphQL             |
| `fork_count`       | `int`      | GitHub GraphQL             |
| `updated_at`       | `string`   | ISO 8601                   |
| `created_at`       | `string`   | ISO 8601                   |
| `pushed_at`        | `string`   | ISO 8601                   |
| `is_archived`      | `bool`     | GitHub GraphQL             |
| `languages`        | `[string]` | Top 5 repo languages       |

## HTML template

The template uses sentinel markers in a `<script>` block:

```html
<script>
// __REPO_DATA_START__
// __REPO_DATA_END__
</script>
```

At build time, `main.go` replaces the markers with `const REPO_DATA = <minified JSON>;` and
minifies the entire HTML output via [`tdewolff/minify`](https://github.com/tdewolff/minify).

### UI features

- **Categorized sidebar** — hierarchical TOC built from the `category` field, with chevron
  expand/collapse and repo counts
- **Card / Simple views** — grid or single-column layout, toggled from the header
- **Type badge** — repo `type` (joined with `/`) shown in the top-right corner of each card
- **Archived badge** — shown next to the type badge when `is_archived` is true
- **Basic search** — filters repos by name, description, category, and tags in real time
- **Advanced SQL search** — uses [AlaSQL](https://github.com/alasql/alasql) to run arbitrary
  `SELECT` queries against the repo table; field list is dynamically reflected from the data
  (no hardcoded field definitions)

## Build & release

```bash
go build -ldflags="-X main.version=1.0.0" .
```

Cross-platform binaries are built via GoReleaser (see `.goreleaser.yaml`):

```bash
goreleaser build --snapshot --clean
```

## Classifier

The [classifier prompt](./docs/github-repo-classifier.md) helps AI assistants categorize
repositories into the input format compatible with this tool.
