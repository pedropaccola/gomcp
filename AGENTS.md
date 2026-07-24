# AGENTS.md

Orientation for agents working on this codebase. The normative documentation
lives in the source files themselves — package and declaration doc comments,
never section-banner comments — this file is the map, not the territory,
and stays short on purpose: when this file and the code disagree, the code
wins, and anything already stated clearly in a doc comment doesn't get
repeated here.

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
  preference.
- **Composition.** New capability is built by combining already-existing,
  already-tested primitives, never by duplicating their logic under a new
  name. `View`'s resolver→enumerator→scanner layering and `Tx`'s verb
  categories exist so the next verb composes on what's already proven.
- **Nomenclature.** Names carry meaning before docs do. A symbol that
  needs its doc comment to be understood is a naming bug: rename it, then
  let the doc add only what the name genuinely cannot.

## Layout

    cmd/gomcp/          entrypoint: flags, workspace root, MCP stdio server
    internal/address/   shared leaf vocabulary (RelativePath, PkgPath,
                        CleanPath), depended on directly by workspace,
                        engine, and tools
    internal/workspace/ the trusted core: model vocabulary and the
                        Workspace, mutable only through its named
                        primitives, one concept per file
    internal/engine/    the model's gates: View (reads) and Tx (writes),
                        each split one semantic category per file, plus
                        dto.go
    internal/tools/     presentation layer: MCP tools, split the same way
                        as engine (read/write handlers, one category per
                        file, a shared.go for helpers called from both)
    testdata/sandbox/   fixture module for semantic and mutation tests

Every file's own doc comment gives the exact category breakdown for its
package — start there, not here.

## Where the invariants live

The load-bearing invariants (canonical bytes, derived state that's rebuilt
rather than patched, sorted-only enumeration, error ⇒ untouched, pointers
scoped to their gate) are documented on the types and methods that hold
them, not here — `workspace.File`, `Package.RebuildIndex`, `View`, `Tx`,
`Engine.Edit` are the ones worth reading first. Same for the per-layer
naming grammars: `Tx`'s verb categories and `View`'s
resolver→enumerator→scanner layering are on their own type docs; the
address conventions (`package` vs `file` arguments, import-path vs
workspace-relative spelling) are on `canonPkg`/`fileArg`
(`internal/tools/shared.go`). Read the relevant type's doc comment before
extending it, rather than working from a second-hand summary that can go
stale on its own.

## Testing

- Everything runs against `testdata/sandbox` (bootstrapped in-memory;
  mutations never touch its disk). Tests that Flush must copy the sandbox
  to a temp dir first (`copySandbox`). Shared helpers live in
  `testutil_test.go` with `testing.TB` signatures so benchmarks reuse them.
- The sandbox exists to be broken: it deliberately covers grouped decls,
  iota, init funcs, generics, in-package and external test files, aliased
  imports, and a permanently type-broken package. When adding a feature,
  add the fixture shape that would have caught its absence — that's this
  project's actual coverage discipline, not a percentage target.
- One deliberate exception: `TestBootstrapLiveRepo` self-hosts on this
  repository as a smoke check, skipped under `-short`.
- Benchmarks: `go test -bench . -benchtime 3x ./internal/engine` (see
  bench_test.go; current numbers recorded in ROADMAP.md). Per-phase load
  timing logs via the engine's `logf` (`-verbose` on the binary).
- Verify with `gofmt -l internal cmd`, `go vet ./...`, and
  `go test ./...` before calling anything done; add `-race` whenever a
  change touches `Engine`'s lock or anything concurrent. Tests shell out
  to `go list` and type-check real modules — expect seconds, not
  milliseconds.

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
