# AGENTS.md

Orientation for agents working on this codebase. The normative documentation
lives in the source files themselves — package and declaration doc comments,
never section-banner comments (see Conventions) — this file is the map, not
the territory. When this file and the code disagree, the code wins; update
whichever is stale.

## What this is

An MCP server exposing a Go workspace to coding agents through
declaration-scoped tools only — no file reads, no grep escape hatch. It
keeps the whole workspace in memory (source bytes + ASTs + type info via
`go/packages`) and answers every mutation with the diagnostics it caused.
Dependencies resolve through the same read tools by import path: exported
API only, lazily cached (`LoadExternal`), never mutable, reset with the
workspace snapshot. README.md explains the bet; ROADMAP.md tracks
agreed-but-deferred work.

## Pillars

Three words settle every design disagreement in this codebase, cited by
name when they do: **Consistency**, **Composition**, **Nomenclature**.
When a change trades one against another, say which one wins and why —
the trade is what's worth recording, not just the outcome.

- **Consistency.** The same concept gets the same word, and the same
  shape, everywhere it appears — even at the cost of an awkward fit in
  one spot. A field name, a tool description, or a JSON tag that says
  something the rest of the surface doesn't is a bug, not a style
  preference: fix the outlier, don't let the reader re-derive the
  exception.
- **Composition.** New capability is built by combining already-existing,
  already-tested primitives, never by duplicating their logic under a new
  name. `View`'s resolver→enumerator→scanner layering and `Tx`'s verb
  categories exist so the next verb composes on what's already proven,
  instead of re-deriving it beside it.
- **Nomenclature.** Names carry meaning before docs do (see "Nomenclature
  grammars" below). A symbol that needs its doc comment to be understood
  is a naming bug: rename it, then let the doc add only what the name
  genuinely cannot.

## Layout

    cmd/gomcp/          entrypoint: flags, workspace root, MCP stdio server
    internal/address/   shared leaf vocabulary (RelativePath, PkgPath,
                        CleanPath), depended on directly by workspace,
                        engine, and tools
    internal/engine/    the model's gates: View (reads) and Tx (writes),
                        each split one semantic category per file, plus
                        dto.go — engine's own public vocabulary, translated
                        from workspace's model at the gate
      workspace/        the trusted core: model vocabulary and the
                        Workspace, mutable only through its named
                        primitives, one concept per file
    internal/tools/     presentation layer: MCP tools, split the same way
                        as engine (read/write handlers, one category per
                        file, a shared.go for helpers called from both)
    testdata/sandbox/   fixture module for semantic and mutation tests

Every file's own doc comment gives the exact category breakdown —
restating it here would be one more place to go stale. No section-banner
comments exist anywhere in this codebase (see Conventions); a file's job is
either self-evident from its name or explained in its own doc comment.

## Core invariants

Three hold by construction — the workspace package owns the model, its hot
fields are unexported, and violating code does not build:
- **Canonical bytes.** A file's `Src()` is the source of truth and `Ast()`
  is a parse of exactly those bytes. Content enters through two doors only:
  `Workspace.SwapFile` (mutation path — goimports runs, the swap enforces
  the parse) and `Package.AddLoadedFile` (load path — the type checker's
  own AST). ASTs locate byte spans for splicing and are never re-printed.
- **Derived state is rebuilt, never patched.** The symbol index and init
  lists re-derive from files (`RebuildIndex`, inside every primitive);
  nothing is incrementally maintained, so nothing drifts.
- **Determinism.** Everything the workspace package enumerates is
  sorted-only (`Files()`, `Symbols()`, `UnitKeys()`, `Tombstones()`); any
  other map needs `sortedKeys` before it reaches an output.

Two are still discipline, not compiler-enforced — violating either is a bug:
- **Two doors for paths.** A string becomes a `RelativePath` only through
  `CleanPath` (untrusted input) or `Engine.relativePath` (absolute →
  workspace-relative).
- **Error ⇒ untouched.** Mutation verbs do all fallible work on candidate
  bytes before swapping; `Edit` discards its cloned workspace on error.
  Post-change problems are never errors — they're the echo's diagnostics
  delta, because broken code is a valid state.

**Pointers don't escape their gate** is both, depending which surface you're
on. `Read`/`Edit` hold the lock for the closure's lifetime; inside it,
View/Tx's private resolvers still hand back live `*workspace.X` pointers for
real work (splicing, type lookups) — must not outlive the closure, still
discipline. View's *public* methods return engine's own DTOs (dto.go): plain
copies with nothing to escape with, so for that surface the invariant holds
by construction.

**Never call an `Engine`-level accessor (`ModulePath`, `IsExternal`, ...)
from inside a `Read`/`Edit` closure.** `Read`/`Edit` already hold the lock
for the closure's lifetime; `sync.RWMutex` isn't reentrant, so an `Engine`
accessor's own lock acquisition inside that closure deadlocks the calling
goroutine against itself — no error, no panic, just a permanent hang. Not
compiler-enforced, not caught by `diagnostics()` (it's a runtime property,
not a type error), only caught by actually running the code. Resolve every
`packageArg`/`fileArg`/other `Engine`-touching argument *before* calling
`Edit`, and pass only the resolved values into the closure — the existing
single-statement tool handlers already do this; a batch handler that moves
that resolution inside the loop-inside-the-closure breaks it.

## Nomenclature grammars (keep new code inside them)

`move_symbol` makes the Pillars' naming-bug rule a one-call fix, not just
an aspiration. Prefer the domain vocabulary the headers already use
(splice, region, tombstone, fragment); never let two helpers share
permuted words for different domains.

Each layer has its own grammar: engine's View (X/Xs/XsWhere and the
resolver→enumerator→scanner layering, one file per category — see Layout)
and Tx (Creators/Editors/Deleters/Refactorings and the placement policy);
tools.go (tool naming and the DiagBlock output convention). Read View's
doc comment or the relevant file before adding a verb. Restating the
grammars here would just be one more place to go stale; what doesn't live
in any single file's doc:

- Symbol keys are one address space across both layers: `"Name"`, methods
  `"Recv.Name"`.
- A rename's prose fix stops at the boundary Go's own tooling already
  checks (`go vet`'s comment convention: symbol docs open with the bare
  name, package docs open with `"Package name"`) — the renamed entity's
  *own* doc comment, its leading line only, never a scan of other
  declarations' docs for mentions (a bare-word scan would corrupt any doc
  using the identifier's text as an ordinary word: `Add`, `Set`, a package
  named `io`). General rule for this class of automation: match an
  existing, tool-checked convention; don't invent a heuristic where none
  exists.
- `reload` discards unflushed work — the recovery move when the
  filesystem changed behind the server (manual edits, git operations).
- Mutation echoes report files touched, diagnostics introduced, and
  diagnostics resolved — one layer up from the read-scoped `DiagBlock`
  a reader carries.
- Tool descriptions earn words only for what changes the agent's input or
  its reading of the output — server internals stay out of them.
- A new verb belongs in Refactorings only if the edit has exactly one
  mechanically correct resolution everywhere it applies; otherwise it's
  an Editor, however tempting the automation looks. `move_symbol`'s
  rename qualifies (the object, and only that object, renamed everywhere
  it's used); renaming an interface's required method doesn't (each
  implementor may need a different fix — rename to match, add a
  delegating method, or drop conformance on purpose), so that stays
  `edit_symbol`'s job. Ask this question before adding the next verb,
  not which category feels more capable.
- Deletion is idempotent, not fail-if-absent like Editors:
  `delete_symbol`/`delete_file`/`delete_package` noop when the target's
  already gone — "gone" is deletion's success condition, whoever caused
  it, the mirror of Creators' "fail if exists, can't destroy code." This
  reaches even a name sharing a multi-name spec (`var a, b = f()`): it's
  trimmed or blanked to `_` rather than refused — see `DeleteSymbol`'s
  own doc for the two deterministic cases.

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
and `dirOf`/`pkgAt` convert only at the disk boundary. `RelativePath` and
`PkgPath` themselves live in `internal/address`, not workspace or engine —
workspace, engine, and tools each depend on that shared leaf package
directly, so the address vocabulary has no re-export chain to leak
through.

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

**Always `flush` at the end of a turn** — this repo's own tools/schema
change often and reconnects discard any unflushed edit silently, same as
`reload`. Two consequences: `go test`/`gofmt`/`go vet` via a shell read
disk, not the in-memory model — flush before trusting their output. And
the connected server's tool schema reflects the *running binary*, not
source you just edited — parameter names can be stale until reconnect.
