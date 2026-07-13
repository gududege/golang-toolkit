# cli-print

A test/debug tool that prints all CLI input it receives: arguments, environment variables, CWD, and exit code. Used as a target for testing argument forwarding, environment propagation, etc.

## Usage

```bash
cli-print [args...]
```

## Output

```
cli-print: /usr/local/bin/cli-print
cwd: /home/user
argc: 2
argv[0]: "hello"
argv[1]: "world"
env: 42 variables
env:HOME: "/home/user"
env:PATH: "/usr/bin:/bin"
exit_code: 2
```

- `cli-print` — full path of the running binary
- `cwd` — current working directory
- `argc` — number of arguments
- `argv[N]` — each argument, C-style escaped
- `env` — number of environment variables, then one `env:KEY: "value"` per variable, sorted case-insensitively
- `exit_code` — final exit code

## Exit code

By default, exit code = number of arguments (clamped to 0–127). Override with `--exit-code N`:

```bash
cli-print --exit-code 42
```

Custom exit code is clamped to 0–255.

## Escaping

| Character | Output |
|-----------|--------|
| `"` | `\"` |
| `\` | `\\` |
| newline | `\n` |
| carriage return | `\r` |
| tab | `\t` |
| empty string | `<empty>` |

## Version

```bash
cli-print -v
```
