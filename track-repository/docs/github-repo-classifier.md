---
name: github-repo-classifier
description: Classify GitHub repositories for a repo tracker by fetching live info from GitHub (README, releases, language, topics) and producing a structured entry with "type", "category", and "tags" fields. Use this whenever the user pastes one or more GitHub URLs and asks to "classify", "分类", "总结", "整理", "add to my repo tracker/list", or asks what type/category/tags a repo should have. Also trigger when the user gives a repo name without a URL and asks to categorize it — search for the repo first. Works for a single repo or a batch/list of repos pasted together.
---

# GitHub Repo Classifier

Classifies GitHub repos into a repo-tracker entry with three fields:

- **type** — how the repo is *used* (form)
- **category** — what domain/problem it solves (a `Area/Subarea` path)
- **tags** — free-form supplementary labels (specific tech/ecosystem keywords)

`type` and `category` are independent axes — do not conflate them. A repo's type never determines its category and vice versa.

`type` is always a **fixed 2-element array**: `[top_level, subtype]`. Never output a bare string or an array of any other length.

## Workflow

For each repo the user provides:

1. **Get the URL.** If the user only gave a name (no URL), use `web_search` to find the canonical GitHub URL first (e.g. `"<name>" github`). Don't guess a URL — a wrong repo produces a wrong classification.
2. **Fetch the repo page** with `web_fetch` on the GitHub URL. This gives you: description, primary language, topics/tags GitHub itself assigned, star count, and a preview of the README.
3. **If the README preview is truncated or unclear about usage**, `web_fetch` the raw README directly (`https://raw.githubusercontent.com/<owner>/<repo>/<branch>/README.md` — try `main` then `master` if the first 404s) to check for install instructions, "Releases" section, or "go get"/"npm install"/"pip install"/"cargo add"/`import` style usage.
4. **Check the Releases tab signal**: if the repo page shows a "Releases" section with downloadable binaries/installers and the README's primary instructions are "download and run" rather than "add as a dependency", that's a strong `release` signal.
5. Determine `type`, `category`, `tags` using the rules below.
6. Output the JSON entry (see Output Format).

Batch multiple repos in the same turn — fetch each one, then output a JSON array. Don't ask the user to confirm between repos unless a repo is genuinely ambiguous (e.g. it's both a CLI you install and a library you import — see Ambiguous cases below).

## Classifying `type`

This is a **two-level decision**. First decide the top level, then pick the matching subtype. Output as `["top_level", "subtype"]`.

### Step 1 — top level: is it directly usable, or does it require the user to develop against it?

| top_level | Definition |
|---|---|
| `release` | You use it as-is — download/install/deploy it and it works. No code of yours goes into it. |
| `sdk` | You build something — it becomes a dependency, base, or extension point inside code the user writes. |

### Step 2 — subtype, based on the top level chosen

**If `release`:**

| subtype | Definition | Signals |
|---|---|---|
| `app` | An end-user application — GUI, desktop, mobile, game. | Releases page with `.exe`/`.dmg`/`.AppImage`/installer assets; README shows screenshots and "Download from Releases" |
| `cli` | A command-line tool: install once, then invoke by name from the shell. No code written against it. | `brew install`, `npm install -g`, `cargo install`, `go install`, followed by usage as `toolname <args>` in a terminal |
| `config` | Not executed at all — a configuration/resource/data file consumed by other software. | Syntax-highlighting defs, dotfiles, IDE themes/snippets, language-definition files, dictionaries |
| `service` | Deployed and run as a standalone server/daemon rather than invoked once. | README centers on `docker run`, `docker-compose up`, exposing a port; databases, message brokers, backend services |

**If `sdk`:**

| subtype | Definition | Signals |
|---|---|---|
| `library` | A general-purpose importable package used inside a larger codebase for one job (parsing, charts, config, HTTP, etc). | `import`/`require`/`go get`/`pip install`/`npm install <lib>`/`cargo add`; the README's usage section is a code snippet calling functions/classes |
| `framework` | Opinionated, structural — the user's project is built *on top of* it, not just calling into it. | README talks about project scaffolding, "convention over configuration," app structure, lifecycle hooks |
| `plugin` | Extends or integrates with another specific host platform/tool rather than standing alone. | README says "a plugin for X" / "extension for X"; only useful inside that host |
| `api-client` | A thin wrapper whose whole purpose is talking to one specific remote API/service. | README is mostly authentication + endpoint-calling examples for one named external API |

### Rules

- `type` is exactly 2 elements: `[top_level, subtype]`. No exceptions, no bare strings, no extra elements.
- If a repo genuinely spans two subtypes (e.g. ships both a CLI binary in Releases *and* an importable package), that's two entries worth of information — pick the **primary/intended** use from the README's own framing (what does the README lead with?) and note the secondary use to the user in one line, rather than inventing a 3+ element array.
- If you truly cannot tell after fetching the README, default `top_level` to `sdk`/`library` for anything language-package-shaped, and `release`/`app` for anything shaped like installable software — and flag the uncertainty to the user in one line rather than guessing silently.

## Classifying `category`

Format: `"Area/Subarea"` (matches the existing tracker convention, e.g. `Data Visualization/Chart Libraries`, `Development/Parsers`, `AI/Speech`).

- Base the Area on the repo's actual subject matter (from its description + README + GitHub topics), not on its `type`.
- Reuse an existing Area/Subarea from the user's tracker if the repo clearly fits one they already have (ask the user to paste their existing category list if you don't have it in context — otherwise infer sensible ones from common conventions below).
- If nothing existing fits, propose a new, sensibly-scoped `Area/Subarea` — don't invent an overly narrow one-off category for a single repo unless the domain is genuinely unique.

Common Areas to reuse/extend as a starting vocabulary (not exhaustive — expand as needed): `AI` (Speech, Vision, NLP, LLM Tooling), `Development` (Parsers, Build Tools, Testing, Editors & IDEs), `Data Visualization` (Chart Libraries, Dashboards), `Networking` (Remote Access, Protocols), `Database`, `DevOps` (Containers, CI/CD), `Media` (Audio, Video, Image Processing), `Productivity` (Notes, Automation), `Security` (Auth, Encryption), `Editors & IDEs` (grouped by the specific editor, e.g. `Editors & IDEs/Notepad++`).

## Classifying `tags`

- Free-form, short, specific keywords — not a repeat of the category.
- Include: the specific technology/protocol/format the repo centers on, notable ecosystem it belongs to (e.g. `"Notepad++"`, `"ANTLR"`), and any especially distinguishing capability.
- 1–3 tags is typical; don't pad with generic words already implied by the category (e.g. don't tag `"Data Visualization"` on a repo already categorized under `Data Visualization/...`).

## Output Format

Return each repo as a JSON object matching the tracker's existing schema, with `type` added:

```json
{
  "name": "<repo name>",
  "url": "<canonical github url>",
  "type": ["sdk", "library"],
  "category": "Development/Parsers",
  "tags": ["ANTLR", "Parsing"]
}
```

More examples:
- RustDesk (download the app and run it) → `["release", "app"]`
- A CLI tool installed via `cargo install` → `["release", "cli"]`
- Notepad++ user-defined-language config files → `["release", "config"]`
- A self-hosted database like a Redis-alternative → `["release", "service"]`
- Viper (Go config library you `import`) → `["sdk", "library"]`
- A web framework you scaffold a project on top of → `["sdk", "framework"]`
- A VS Code extension → `["sdk", "plugin"]`
- A generated SDK that only wraps one company's REST API → `["sdk", "api-client"]`

For multiple repos, return a JSON array of these objects. After the JSON, add one short line per repo only if something needs the user's judgment (ambiguous type, a brand-new category being proposed, or a repo that couldn't be fetched) — otherwise no extra commentary is needed.

## Notes

- Never fabricate star counts, license, or last-updated info you didn't actually fetch — pull them from the live page if the user wants them included, otherwise omit.
- If `web_fetch` fails (private repo, 404, deleted), say so plainly and don't guess a classification from the name alone.
- If the user hasn't shared their existing category taxonomy in this conversation, it's fine to ask once up front ("要不要贴一下你现有的 category 列表，方便我复用而不是造新的分类？") rather than inventing a parallel taxonomy — but still proceed with best-effort categories if they don't have one handy.
