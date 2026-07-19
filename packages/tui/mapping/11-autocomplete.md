# 11 — Autocomplete (slash commands + file paths)

- **TS source:** [`autocomplete.ts`](../../reference/tui/src/autocomplete.ts) (786 LOC)
- **Go target:** `autocomplete.go`
- **Package:** `tui`
- **Depends on:** `fuzzy.go` ([12-fuzzy.md](12-fuzzy.md)). Used by `Editor`.
- **I/O:** `readdir` for path completion; spawns **`fd`** for fuzzy `@file` search.

---

## API

| TS | Go |
|---|---|
| `interface AutocompleteItem { value, label, description? }` | `type AutocompleteItem struct{ Value, Label, Description string }` |
| `interface SlashCommand { name, description?, argumentHint?, getArgumentCompletions? }` | `type SlashCommand struct{ Name, Description, ArgumentHint string; GetArgumentCompletions func(prefix string) ([]AutocompleteItem, bool) }` |
| `interface AutocompleteSuggestions { items, prefix }` | `type AutocompleteSuggestions struct{ Items []AutocompleteItem; Prefix string }` |
| `interface AutocompleteProvider { triggerCharacters?, getSuggestions, applyCompletion, shouldTriggerFileCompletion? }` | `type AutocompleteProvider interface{ TriggerCharacters() []string; GetSuggestions(...) (*AutocompleteSuggestions, bool); ApplyCompletion(...) CompletionResult; ShouldTriggerFileCompletion(...) bool }` |
| `class CombinedAutocompleteProvider` | `type CombinedAutocompleteProvider struct{...}` |
| `new CombinedAutocompleteProvider(commands, basePath, fdPath?)` | `NewCombinedAutocompleteProvider(commands []any, basePath string, fdPath string) *CombinedAutocompleteProvider` |

**Provider interface methods (async in TS):**

| TS method | Go signature |
|---|---|
| `getSuggestions(lines, line, col, {signal, force})` | `GetSuggestions(lines []string, line, col int, signal context.Context, force bool) (*AutocompleteSuggestions, bool)` |
| `applyCompletion(lines, line, col, item, prefix)` | `ApplyCompletion(...) (lines []string, line, col int)` |
| `shouldTriggerFileCompletion(lines, line, col)` | `ShouldTriggerFileCompletion(...) bool` |

`commands` is `(SlashCommand | AutocompleteItem)[]` — Go: `[]any` or a sum type;
recommend `type CommandSource struct{ Slash *SlashCommand; Item *AutocompleteItem }`.

## `getSuggestions` dispatch (port the precedence exactly)

1. **`@` prefix** (`extractAtPrefix`) → fuzzy file suggestions via `fd`
   (`getFuzzyFileSuggestions`). Handles `@"..."` quoted, `@`-bare. Returns null if
   none.
2. **`/command`** at line start (no space) → fuzzy-filter `commands` by name; build
   description from `argumentHint`/`description`.
3. **`/command <arg>`** (space present) → `command.getArgumentCompletions(argText)`.
4. **Path prefix** (`extractPathPrefix`, forced on Tab) → `getFileSuggestions`
   (readdir-based). Natural-trigger only for `./`, `~/`, `/`, or trailing `/`.

## Path handling helpers (port verbatim)

| TS | Go |
|---|---|
| `toDisplayPath` (backslash→`/`) | `toDisplayPath` |
| `escapeRegex`, `buildFdPathQuery`, `buildTriggerPattern`, `buildDebouncePattern` | same |
| `findLastDelimiter`, `findUnclosedQuoteStart`, `isTokenStart`, `extractQuotedPrefix` | same |
| `parsePathPrefix` → `{rawPrefix, isAtPrefix, isQuotedPrefix}` | same |
| `buildCompletionValue` | quote logic (needs quotes if quoted prefix or contains space) |
| `expandHomePath`, `resolveScopedFuzzyQuery`, `scopedPathForDisplay` | `~/` expansion via `os.UserHomeDir`; `filepath.Join` |
| `walkDirectoryWithFd` | spawn `fd` with `--base-directory --max-results 100 --type f --type d --follow --hidden --exclude .git [--full-path] [query]`; abort via killed process on context cancel |
| `getFileSuggestions` | `os.ReadDir` + directory-first sort; symlink→dir resolution via `os.Stat` |
| `scoreEntry` | filename match scoring (exact 100 / prefix 80 / substring 50 / path-substring 30) + dir bonus +10 |

**`fd` dependency:** TS detects `fd` via `which`/`where` and passes the path; if
absent, `@file` fuzzy search is disabled (returns empty). Go: same — `exec.LookPath("fd")`.
The readdir path-completion works without `fd`.

## `applyCompletion` (cursor math)

Inserts the chosen `item.value` at the prefix, computing the new cursor column.
Branches on context: slash-command (adds `/value ` + space), `@`-attachment (no
trailing space for directories), command-argument, plain path. Handles quote
de-duplication (don't double the closing quote). All pinned by tests.

## Tests to port
- **`autocomplete.test.ts` (542)** — `extractPathPrefix` cases, `fd`-based `@` file
  search (skipped if fd absent: `{skip: !isFdInstalled}`), `./` and quoted path
  completion, quote de-dup. Uses temp dirs + `os` fixtures; port with `t.TempDir()`.
  The fd suite spawns real `fd` — keep the skip guard in Go (`t.Skip` if `fd` missing).
