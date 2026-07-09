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
README.md explains the bet; ROADMAP.md tracks agreed-but-deferred work.

## Layout

    cmd/mcpgo/          entrypoint: flags, workspace root, MCP stdio server
    internal/engine/    the model: state, lookups, mutations
      engine.go         data structures, path API, Bootstrap/load pipeline
      lookup.go         read layer (all methods on View)
      mutation.go       write layer (all verbs on Tx)
    internal/tools/     presentation layer: MCP tools
      tools.go          the declared surface: registration + I/O shapes
      read.go           read handlers + shared resolvers/renderers
      edit.go           mutation handlers, all flowing through runEdit
    testdata/sandbox/   fixture module for semantic and mutation tests

## Core invariants (violating any of these is a bug)

1. **`File.Src` is canonical; `File.Ast` is a parse of exactly `Src`.**
   Positions convert to byte offsets and back losslessly. Never re-print an
   AST to produce source — mutations splice byte spans located via the AST,
   then reparse (`reloadFile` in mutation.go is the only choke point).
2. **Everything else is derived and rebuilt, never patched.** Symbol tables,
   inits, diagnostics: rebuild from `Files` (`RebuildIndex`), don't edit in
   place. Nothing can drift because nothing is incrementally maintained.
3. **Two doors for paths.** A string becomes a `RelativePath` only through
   `CleanPath` (untrusted input; validates) or `Engine.relativePath`
   (absolute → workspace-relative). Map keys are always workspace-relative.
4. **Error ⇒ untouched.** Mutation verbs do all fallible work on candidate
   bytes before swapping; `Edit` runs fn on a cloned workspace it discards
   on error. Post-change problems are never errors — they are the echo's
   diagnostics delta, because broken code is a valid state (Bootstrap holds
   the same principle: per-package errors become Diagnostics, not failures).
5. **Pointers don't escape their gate.** `Read(fn(*View))` holds RLock,
   `Edit(fn(*Tx))` holds the write lock; `*Symbol`/`*Package`/`*File`
   obtained inside must not outlive the closure. `Tx` embeds `*View`, so all
   lookups compose in-transaction (parse-fresh, type-stale until the
   commit-time recheck).
6. **Determinism.** Anything that enumerates is sorted; map iteration order
   must never reach an output (see `sortedKeys`, `sortMatches`).

## Nomenclature grammars (keep new code inside them)

**lookup.go** (sections: Resolvers / Enumerators / Scanners / Source /
Diagnostics):
- `X(addr) (..., bool)` — resolve one resource; comma-ok, never error.
- `Xs(scope)` — enumerate a scope's resources; sorted. Addresses derive from
  resources (`pkg.Path`, `sym.Key()`), never returned separately.
- `XsLike/XsWhere/XsRegexp` — workspace scans returning `[]Match` (scans
  cross packages, so hits carry their owner). Semantic scanners
  (`SymbolsImplementing`, `SymbolsReferencing`) return an error when type
  info can't answer exactly — approximation is never the fallback.
- Everything composes downward: scanners iterate enumerators, enumerators
  use resolvers. New lookups keep to that layering.

**mutation.go** (sections: Creators / Editors / Refactorings):
- Creators fail if the address exists (can never destroy). Editors fail if
  it doesn't. Refactorings are structure-preserving: multi-site renames
  driven by the semantic scanners, and moves that refuse anything whose
  meaning depends on its surroundings (iota groups, shared specs, the test
  build boundary).
- New declarations land at canonical positions: const/var top, types next,
  funcs bottom, methods right after their receiver's group (`insertOffset`).
- The server owns import blocks: goimports runs in every `reloadFile`, and
  imports of in-memory-only packages (invisible to goimports, which scans
  disk) self-repair between rechecks (`repairMissingImports` — one bounded,
  best-effort pass that refuses ambiguous names).
- Symbol keys: `"Name"`, methods `"Recv.Name"`. Same address space as reads.

**tools/** — `list_*` enumerate, `describe_*` render one address, `search_*`
scan, `diagnostics` reports, `create_*`/`edit_*`/`delete_*`/`move_*`/`rename_*`
mutate, `flush` writes to disk. tools.go holds the entire declared surface
(names, descriptions, schemas); handlers hold no presentation decisions the
shapes don't show. Every reader output may carry a `DiagBlock` scoped to
exactly what was read — a view, never the inventory (`diagnostics` is the
inventory). Every mutation echo reports files touched, diagnostics
introduced, and diagnostics resolved. Tool descriptions earn words only for
what changes the agent's input or its reading of the output — server
internals stay out of them.

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
your working tree. When developing the server itself, prefer direct file
tools plus the test suite; use the MCP connection for end-to-end
verification after a reconnect.
