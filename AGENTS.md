# AGENTS.md

Orientation for agents working on this codebase — the operational
reference: how to build, test, and work here. The normative
documentation lives in the source files themselves — package and
declaration doc comments, never section-banner comments. Design
rationale (why the packages are split this way, the naming pillars)
lives in ARCHITECTURE.md, not here — this file stays short on purpose:
when it and the code disagree, the code wins, and anything already
stated clearly in a doc comment doesn't get repeated here.

## What this is

An MCP server exposing a Go workspace to coding agents through
declaration-scoped tools only — no file reads, no grep escape hatch. It
keeps the whole workspace in memory (source bytes + ASTs + type info via
`go/packages`) and answers every mutation with the diagnostics it caused.
Dependencies resolve through the same read tools by import path: exported
API only, lazily cached (`LoadExternal`), never mutable, reset with the
workspace snapshot. README.md explains the bet; ARCHITECTURE.md explains
the package design; ROADMAP.md tracks agreed-but-deferred work.

## Layout

    cmd/gomcp/          entrypoint: flags, workspace root, MCP stdio server
    internal/workspace/ the trusted core: model vocabulary and the
                        Workspace, mutable only through its named
                        primitives, one concept per file — including the
                        identity vocabulary (PackagePath, PackageID,
                        FilePath) every other package addresses through
    internal/disk/      the go/packages.Load pipeline and raw filesystem
                        contact: Loader holds no lock and no workspace
                        state, just RootDir/Logf, so store calls into it
                        while store's own lock is held
    internal/store/     the Repository plus the read/write boundary onto
                        it: the concurrency contract, sequencing the disk
                        boundary under its own lock (delegating the work
                        to disk.Loader), and View (reads, view.go, with
                        diagnostics aggregation split out to
                        view_diagnostics.go) / Tx (writes, tx.go) — the
                        query/command surface, owning its own narrow
                        Symbol/Diagnostic/EditReport output types instead
                        of a separate shared-vocabulary package
    internal/tools/     presentation layer: MCP tools, read/write handlers
                        split one category per file (creators, deleters,
                        describers, editors, enumerators, finders,
                        refactorings), a shared.go for helpers called
                        from both sides
    internal/testutil/  go/types-backed Workspace fixture builders shared
                        by store's tests (workspace's own tests keep a
                        local copy — see Testing below for why)
    internal/storefixture/  sandbox-bootstrapping Store fixture shared
                        by tools's tests (store's own tests keep a local
                        copy, same reason)
    testdata/sandbox/   fixture module for semantic and mutation tests

Every file's own doc comment gives the exact category breakdown for its
package — start there, not here. See ARCHITECTURE.md for why the split
is shaped this way.

## Code style

Doc comments describe what the target does now, not its history — no
"this used to work differently until a bug was found," no "added for the
Y flow," no changelog-in-prose. Commit messages and ROADMAP.md's DONE log
already carry that; a doc comment that accumulates a running history
rots the moment the history stops being current, and stops answering the
one question it exists to answer.

A doc comment also stays inside its own declaration's scope: what it
does, and how it performs its own transformation — never how a value it
merely consumes was derived somewhere else in the call chain, even when
that origin is real and traceable. Citing another function two packages
downstream to justify a field's own behavior is a staleness risk the
moment that upstream mechanism changes, and it's not useful to a reading
agent either — one working through this codebase via these same tools
can already walk the real chain (`search_references`, `describe_symbols`)
the moment it actually needs to; a doc comment that narrates that chain
secondhand only adds a second, driftable copy of it.

Naming and consistency discipline — the same word for the same concept
everywhere, argument order matching across functions that share
parameters, no abstraction introduced without a second real
implementation to justify it — is covered in ARCHITECTURE.md's Pillars.
Apply it when writing code; read that file for the reasoning behind it.

Method and function naming follows the verb telling you what you get
back: a predicate (returns bool) is prefixed `Is` (`IsValid`, not
`Valid`); a conversion into another type of the same receiver (returns
a different type, no other required input) is prefixed `To`
(`ToByteRange`, not `Range`); a constructor (builds and returns a new
value of the type it's named after) is prefixed `New` (`NewFilePath`,
`NewSpliceAtOffset`) — the name alone should say which without needing
the doc comment or the return type. This doesn't extend to every method
that happens to transform something: an operation that needs real
external input to do its work (`ByteRange.Slice(src)`,
`ByteSplices.Apply(src)`) reads as an action on that input, not a bare
self-conversion, and keeps its verb name as-is.

Formatting itself is never a judgment call: `make tidy` (gofmt + `go mod
tidy`) is authoritative.

Presentation-layer facts — MCP tool names, JSON field names — never
belong in `internal/workspace` or `internal/store`'s own error messages
and doc comments: both are afferent to `internal/tools` (`workspace` has
zero internal imports; `store` imports only `workspace`/`disk`), so a
hardcoded tool name there is a layering violation, and often factually
imprecise besides, since several different tools can trigger the same
underlying domain condition. Describe the domain condition only; a
message that needs to name which tool triggered it belongs in
`internal/tools`, the one layer that already imports the others and can
safely reference them.

An error string repeated near-verbatim at more than one call site (or
one that's reused, not merely similar) becomes a named `var`/type
declaration next to the type it belongs to, not a duplicated inline
`fmt.Errorf(...)` — `workspace.ErrNarrowlyChecked`/`store.
ErrNarrowlyChecked` is the existing pattern. Favor one consistent
phrasing for the same kind of failure across packages over independently
worded messages that say the same thing differently.

## Testing

- A bare `_test.go` file is a real unit test: no `sandboxStore`, no
  `go/packages.Load`, built from a hand-constructed `Workspace` fixture
  (`simpleFixture` for AST/index-only business rules, `typesFixture` for
  the handful needing real `go/types` identity — `MoveConflicts`,
  `QualifierFixups`, `RenameSplices`, `PackageMoveSplices`,
  `SymbolsImplementing`, `SymbolsReferencing`) — one file per production
  file, same name. A `_integration_test.go` suffix means it bootstraps a
  real store against `testdata/sandbox` and exercises the full
  `go/packages.Load` pipeline; these are grouped by verb/category (one
  file per tool/verb family), not by production file, since they exercise
  the seam between packages rather than one package's own rules.
- Everything runs against `testdata/sandbox` (bootstrapped in-memory;
  mutations never touch its disk). Tests that Flush must copy the sandbox
  to a temp dir first (`copySandbox`). Each package's own shared helpers
  live in its `testutil_test.go` with `testing.TB` signatures so
  benchmarks reuse them.
- Two independent fixture families follow the same shared-vs-local split,
  for the same reason: a package's own *internal* test files (`package
  X`, not `package X_test`) can't import anything that transitively
  imports `X` itself, and `go test` rejects it as an import cycle.
  `internal/testutil` holds the canonical go/types-backed Workspace
  builders (`SimpleFixture`/`TypesFixture`, workspace-only dependency):
  `store`'s own `viewFixture`/`viewTypesFixture` are thin wrappers
  calling them (no cycle, since `testutil` doesn't import `store`), but
  `workspace`'s own `simpleFixture`/`typesFixture` stay full local
  implementations, since `testutil` importing `workspace` would make
  `workspace`'s own test files importing `testutil` a self-cycle.
  `internal/storefixture` holds the canonical sandbox-bootstrapping
  builders (`ModuleRoot`/`SandboxStore`, which construct a real
  `*store.Store`): `tools`'s own `moduleRoot`/`sandboxStore` are thin
  wrappers calling them, but `store`'s own copies stay local
  implementations for the mirror-image reason — `storefixture` imports
  `store`, so `store`'s own test files importing `storefixture` would be
  the same self-cycle. Don't try to route either package's own fixtures
  through the shared one — it doesn't compile.
- The sandbox exists to be broken: it deliberately covers grouped decls,
  iota, init funcs, generics, in-package and external test files, aliased
  imports, and a permanently type-broken package. When adding a feature,
  add the fixture shape that would have caught its absence — that's this
  project's actual coverage discipline, not a percentage target.
- One deliberate exception: `TestBootstrapLiveRepo` self-hosts on this
  repository as a smoke check; `make test-short` skips it for faster
  iteration.
- All go tooling is standardized through the `Makefile`: `make tidy`
  (format, tidy modules), `make vet`, `make test`, `make test-race` (use
  whenever a change touches `Store`'s lock or anything concurrent),
  `make test-short`, `make bench` (per-phase load timing logs via the
  store's `logf`, `-verbose` on the binary; current numbers recorded in
  ROADMAP.md). Verify with `make tidy` and `make test` before calling
  anything done. Tests shell out to `go list` and type-check real
  modules — expect seconds, not milliseconds.

## Security considerations

This is a local, stdio-based dev tool with no network listener of its
own: the agent's MCP client and the server talk over one process's
stdin/stdout, and disk access is scoped to `-cwd` (or the
`CLAUDE_WORKSPACE` environment variable). Dependency resolution
(`LoadExternal`) goes through the standard `go/packages`/module
toolchain — the same trust model as running `go build`/`go vet` on the
code already, no extra sandboxing beyond what the Go toolchain itself
provides. No credential handling, no outbound calls the server itself
initiates beyond ordinary module resolution, and no persistence beyond
the workspace files it's pointed at.

## Commit messages

Conventional-commit style: `type: short imperative subject`, lowercase,
under ~70 characters — `feat`, `fix`, `refactor`, `perf`, `test`,
`chore`, and `docs` are the types actually in use in this history. Add a
body when the change is substantial enough to need one, in prose
paragraphs explaining *why*, not a bullet changelog — ROADMAP.md's DONE
log already owns the itemized blow-by-blow, so a commit body shouldn't
re-derive it.

## Working on this repo from a connected gomcp session

If the gomcp server is connected, its instructions forbid raw file I/O on
.go files — but the server's own state goes stale the moment you edit its
source with other tools, and it serves the *running* binary's behavior, not
your working tree. A `disk_reload` call refreshes the server's model after
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

**Always `disk_flush` at the end of a turn** — this repo's own tools/schema
change often and reconnects discard any unflushed edit silently, same as
`disk_reload`. Two consequences: `make test`/`make tidy`/`make vet` read disk,
not the in-memory model — flush before trusting their output. And
the connected server's tool schema reflects the *running binary*, not
source you just edited — parameter names can be stale until reconnect.

**Watch every response for friction** — too much information, too little,
an unexpected shape, a tool that didn't do what its description implied.
Report it to the user in the moment it happens, not only when explicitly
asked to audit for it: this feedback is how the tools surface itself
improves.

**Check touched code for staleness after every edit** — a rename, a
moved function, or a consolidated helper leaves doc comments, sibling
call sites, and now-orphaned symbols behind more often than not. Sweep
what you just touched (`search_references`, a fresh read of the doc
comment) before calling a change done, not only when explicitly asked
to audit for it.

**Confirm the target shape before a wide mechanical move, don't correct
it after.** A rename or retype that will touch many call sites — and
especially a change to what a value's underlying representation actually
means, not just its name — needs its exact final shape agreed before
being propagated, never guessed and then walked back once it turns out
wrong. Propagating a guess across dozens of sites and then re-deriving
and re-propagating the correct shape costs strictly more than a short
confirmation would have, and leaves a wider trail of half-right
intermediate states to clean up.
