# AGENTS.md

Orientation for agents working on this codebase. The normative documentation
lives in the section-header comments of the source files themselves — this
file is the map, not the territory. When this file and a source header
disagree, the header wins; update whichever is stale.

## What this is

An MCP server exposing a Go workspace to coding agents through
declaration-scoped tools only — no file reads, no grep escape hatch. It
keeps the whole workspace in memory (source bytes + ASTs + type info via
`go/packages`) and answers every mutation with the diagnostics it caused.
Dependencies resolve through the same read tools by import path: exported
API only, lazily cached (`LoadExternal`), never mutable, reset with the
workspace snapshot. README.md explains the bet; ROADMAP.md tracks
agreed-but-deferred work.

## Layout

    cmd/gomcp/          entrypoint: flags, workspace root, MCP stdio server
    internal/engine/    the model's gates: lookups, mutations, load pipeline
      engine.go         state re-exports, path API, Bootstrap/load pipeline
      lookup.go         read layer (all methods on View)
      mutation.go       write layer (all verbs on Tx)
      state/            the trusted core: model vocabulary and the
                        Workspace, mutable only through its primitives
    internal/tools/     presentation layer: MCP tools
      tools.go          the declared surface: registration + I/O shapes
      read.go           read handlers + shared resolvers/renderers
      edit.go           mutation handlers, all flowing through runEdit
    testdata/sandbox/   fixture module for semantic and mutation tests

## Core invariants

The first three hold by construction: the state package owns the model,
its hot fields are unexported, and code violating them does not build —
know that they hold, not how to maintain them.

1. **Canonical bytes.** A file's `Src()` is the source of truth and its
   `Ast()` is a parse of exactly those bytes; positions convert to byte
   offsets and back losslessly. Content enters through two doors only:
   `Workspace.SwapFile` (mutation path — `reloadFile` runs goimports, the
   swap enforces the parse) and `Package.AddLoadedFile` (load path — the
   type checker's own AST). ASTs locate byte spans for splicing and are
   never re-printed.
2. **Derived state is rebuilt, never patched.** The symbol index and init
   lists re-derive from files (`RebuildIndex`, inside every primitive);
   nothing is incrementally maintained, so nothing drifts.
3. **Determinism.** Everything the state package enumerates is sorted-only
   (`Files()`, `Symbols()`, `UnitKeys()`, `Tombstones()`); the raw maps
   never leave it. Any other map needs `sortedKeys` before it reaches an
   output (see `sortMatches`).

The rest is still discipline — violating any of these is a bug:

4. **Two doors for paths.** A string becomes a `RelativePath` only through
   `CleanPath` (untrusted input; validates) or `Engine.relativePath`
   (absolute → workspace-relative). Map keys are always workspace-relative.
5. **Error ⇒ untouched.** Mutation verbs do all fallible work on candidate
   bytes before swapping; `Edit` runs fn on a cloned workspace it discards
   on error. Post-change problems are never errors — they are the echo's
   diagnostics delta, because broken code is a valid state (Bootstrap holds
   the same principle: per-package errors become Diagnostics, not failures).
6. **Pointers don't escape their gate.** `Read(fn(*View))` holds RLock,
   `Edit(fn(*Tx))` holds the write lock; `*Symbol`/`*Package`/`*File`
   obtained inside must not outlive the closure. `Tx` embeds `*View`, so all
   lookups compose in-transaction (parse-fresh, type-stale until the
   commit-time recheck).

## Nomenclature grammars (keep new code inside them)

Names carry meaning before docs do: a symbol that needs its doc comment to
be understood is a naming bug — rename it (rename_declaration makes that a
one-call fix), then let the doc add what the name cannot. Prefer the
domain vocabulary the headers already use (splice, region, tombstone,
fragment); never let two helpers share permuted words for different
domains.

lookup.go, mutation.go, and tools.go each open with their own layer's
grammar (X/Xs/XsWhere and the resolver→enumerator→scanner layering;
Creators/Editors/Refactorings and the placement policy; tool naming and
the DiagBlock output convention) — read the header before adding a verb.
Restating them here would just be one more place to go stale; what
doesn't live in any single header:

- Symbol keys are one address space across both layers: `"Name"`, methods
  `"Recv.Name"`.
- `reload` discards unflushed work — the recovery move when the
  filesystem changed behind the server (manual edits, git operations).
- Mutation echoes report files touched, diagnostics introduced, and
  diagnostics resolved — one layer up from the read-scoped `DiagBlock`
  a reader carries.
- Tool descriptions earn words only for what changes the agent's input or
  its reading of the output — server internals stay out of them.

Address convention (both directions, gated by `canonPkg`/`fileArg`):
`package` is the import path (`github.com/you/mod/internal/tools`) — the
type checker's identity, one grammar for workspace and dependency
packages (resolution order: workspace first, then the dependency cache,
lazily loaded); a bare workspace directory (`internal/tools`) is accepted
and gains the module prefix. A `*.go` name is never a package — refused, not
stripped. `file` is a bare name within its package (`read.go`); a path is
tolerated when its package agrees (workspace-relative or module-qualified
spelling), and contradictions are refused, never guessed. Outputs speak
import paths wherever a package is named: bare file names when the package
was the input, package-keyed maps (`{"example.com/mod/pkg": ["file.go"]}`)
when a result spans packages. Diagnostics strings stay `path:line:col` —
positional prose, not addresses. Engine-internal: `Packages` is keyed by
`PkgPath`, files stay `RelativePath` (disk truth for flush/reload/overlay),
and `dirOf`/`pkgAt` convert only at the disk boundary.

## Testing

- Everything runs against `testdata/sandbox` (bootstrapped in-memory;
  mutations never touch its disk). Tests that Flush must copy the sandbox
  to a temp dir first (`copySandbox`). Shared helpers live in
  `testutil_test.go` with `testing.TB` signatures so benchmarks reuse them.
- The sandbox exists to be broken: it deliberately covers grouped decls,
  iota, init funcs, generics, in-package and external test files, aliased
  imports, and a permanently type-broken package. When adding a feature,
  add the fixture shape that would have caught its absence.
- One deliberate exception: `TestBootstrapLiveRepo` self-hosts on this
  repository as a smoke check, skipped under `-short`.
- Benchmarks: `go test -bench . -benchtime 3x ./internal/engine` (see
  bench_test.go; current numbers recorded in ROADMAP.md). Per-phase load
  timing logs via the engine's `logf` (`-verbose` on the binary).
- Verify with: `gofmt -l internal cmd`, `go vet ./...`, `go test ./...`.
  Tests shell out to `go list` and type-check real modules — expect seconds,
  not milliseconds.

## Working on this repo from a connected gomcp session

If the gomcp server is connected, its instructions forbid raw file I/O on
.go files — but the server's own state goes stale the moment you edit its
source with other tools, and it serves the *running* binary's behavior, not
your working tree. A `reload` call refreshes the server's model after
direct edits or git operations; only *behavior and schema* changes to the
server itself still require a reconnect, since the running binary is the
running binary.

Plan for these tools cause-first, not site-first: file-editing habits say
inventory every affected site up front (grep, skim, list), but here you
plan only the declaration and signature changes in dependency order and
let the mutation echoes enumerate the consequence sites — exactly, per
transaction. Order edits so echoes stay interpretable (types before
consumers, helpers before callers); batch all changes to one declaration
into a single edit (replacement is whole-declaration); and always end with
the test suite — echoes referee only what the type system distinguishes.
