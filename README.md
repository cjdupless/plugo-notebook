# Plugo Notebook

**Plugo** is a lightweight, Go-native notebook application served by a single Go binary with an embedded web UI. Combine **Markdown** notes and **compiled Go** cells powered directly by your local Go toolchain.

## Highlights

- **Zero boilerplate**: write Go snippets directly without wrapping every cell in `package main` and `func main()` (full `package main` cells work too).
- **Shared package scope**: `type`, `func`, methods, `const`, leading `var`, and `import` declarations in earlier cells are automatically shared with cells below.
- **Smart stdlib auto-imports & unused-import pruning**: use Go standard library packages like `fmt`, `strings`, `slices`, `sync`, or `time` immediately without manual import boilerplate.
- **Native Go ergonomics**: exact cell & line error mapping via `//line` directives, per-cell `gofmt` formatting (`Alt+Shift+F`), live Shared Scope inspector, Generated Go preview, and one-click Export to `.go`.
- **Markdown cells**: render headings, lists, task lists, tables, blockquotes, links, and Go-highlighted fenced code blocks (`Shift+Enter` to render, double-click to edit).
- **Single binary, zero external dependencies**: built purely on the Go standard library with embedded web assets (`//go:embed`).

## Quick Start

Run directly with Go:

```bash
go run .
```

Or build the standalone binary:

```bash
go build -o plugo .
./plugo
```

Plugo starts a local server (default `http://127.0.0.1:8080`), opens in your browser, and creates a starter `welcome.plugo` notebook inside `./notebooks`.

### CLI Flags

| Flag | Default | Description |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | HTTP listen address |
| `-dir` | `./notebooks` | Directory where `.plugo` notebook files are stored |
| `-open` | `true` | Automatically open Plugo in your default browser |
| `-timeout` | `15s` | Maximum execution time per Go cell |

## Keyboard Shortcuts

- `Shift+Enter` — Run Go cell (or render Markdown) and advance to the next cell
- `Ctrl+Enter` / `Cmd+Enter` — Run Go cell (or render Markdown) in place
- `Alt+Shift+F` — Format active Go cell with `gofmt`
- `Ctrl+S` / `Cmd+S` — Save notebook immediately
- `Tab` / `Shift+Tab` — Insert or remove Go tab indentation
