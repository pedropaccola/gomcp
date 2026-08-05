# Architecture

The *why* behind this codebase's shape. AGENTS.md is the operational
reference — how to build, test, and work here; read that first if you
just need to make a change. Come here when you need to know why
something is built the way it is, or before proposing a structural
change of your own.

## Pillars

Three words settle every design disagreement in this codebase, cited by
name when they do: **Consistency**, **Composition**, **Nomenclature**.
When a change trades one against another, say which one wins and why —
the trade is what's worth recording, not just the outcome.

- **Consistency.** The same concept gets the same word, and the same
  shape, everywhere it appears — even at the cost of an awkward fit in
  one spot. A field name, a tool description, a JSON tag, or an argument
  order that says something the rest of the surface doesn't is a bug,
  not a style preference.
- **Composition.** New capability is built by combining already-existing,
  already-tested primitives, never by duplicating their logic under a new
  name. `View`'s resolver→enumerator→scanner layering and `Tx`'s verb
  categories exist so the next verb composes on what's already proven.
  An abstraction (an interface, a new shared type) earns its place when a
  second real implementation needs it — not before.
- **Nomenclature.** Names carry meaning before docs do. A symbol that
  needs its doc comment to be understood is a naming bug: rename it, then
  let the doc add only what the name genuinely cannot.

## Package responsibilities

The Layout table in AGENTS.md names what's where; this names *why*, and
which known pattern each package's job is an instance of — recognizing
the pattern matters more than memorizing the mechanism, which is
documented on the type that actually implements it.

- **`workspace`** is the DDD **Aggregate Root**: the only package that
  mutates the Entity graph (`Package`/`File`/`Symbol`, addressed by
  `PackagePath`) directly, and the only one trusted to decide whether a
  mutation is consistent —
  `MoveConflicts`, `QualifierFixups`, and its other business-rule methods
  exist so no client ever re-derives an invariant `workspace` already
  owns. Its concurrency primitive is **copy-on-write**: `Workspace.
  Clone()` shares every `Package`/`Unit` until something inside it is
  actually touched, forking lazily per generation rather than copying
  eagerly.
- **`store`** is the **Repository** (the seam between the in-memory
  Aggregate and disk) plus the query/command boundary onto it:
  `Store.ws` is a plain `*workspace.Workspace` guarded by `Store.mu`, a
  `sync.RWMutex`. `Read` holds the read lock for its whole call — any
  number of Reads run concurrently with each other, but wait out an
  in-flight writer — while `Edit`/`Flush`/`Bootstrap`/`Reload` hold the
  write lock and serialize as the sole writer, building the next version
  off to the side (via `workspace`'s own copy-on-write clone) before
  installing it with one assignment. `Read`/`Edit` construct and scope
  the actual query/command objects: `View` is a query object (one read
  snapshot, scoped to a single `Read` call), `Tx` is a unit of work (one
  write transaction, scoped to a single `Edit` call, embedding `View` so
  writes compose on the same reads). Neither owns a consistency rule
  itself — each resolves an address, asks `workspace` for the decision,
  and applies what it returns. A post-edit recheck is scoped to dirty
  packages and their transitive importers (`Workspace.RecheckScope`),
  not the whole module. `Store` never touches the filesystem or
  `go/packages` itself — it sequences the disk boundary under its own
  lock and delegates the actual work to `disk.Loader`. Full mechanism on
  `Store`'s own doc comment. `store` also owns the Value Object
  vocabulary that used to live in a separate `internal/dto` package:
  `store.Symbol`/`Diagnostic`/`EditReport` are `store`'s own
  declarations, each narrowed to what an actual `tools` call site reads
  rather than mirroring `workspace`'s field list wholesale. `Symbol.Owner`
  (a `workspace.PackageID`) carries a scan hit's real resolved identity,
  closing the gap that once made a separate `store.Match` type necessary.
- **`disk`** is the go/packages.Load pipeline and the filesystem's other
  door: `Loader` holds no lock and no workspace state of its own — just
  `RootDir`/`Logf` — so `store` calls into it while `store`'s own lock is
  held, the same way `store` calls into `workspace`'s primitives. Nothing
  about this seam changes *when* the lock is held, only *where* the code
  that runs while holding it lives.

## Where the invariants live

The load-bearing invariants (canonical bytes, derived state that's rebuilt
rather than patched, sorted-only enumeration, error ⇒ untouched, pointers
scoped to their call, position resolution kept out of the value itself)
are documented on the types and methods that hold them, not here —
`workspace.File`, `Package.RebuildIndex`, `store.View`, `store.Tx`,
`Store.Edit` are the ones worth reading first. The last of those is a
recognized convention, not an invented one: a `File`'s own methods that
resolve a `token.Pos` into a byte offset (`EditHeader` is the current
example — most position/span computation stayed on `Workspace`, see its
own `declSpan`/`specSpan`/`ExtractDeclaration`, after an explicit
complexity review found splitting them out to `File` cost more in
cross-file indirection than it earned) take `*token.FileSet` as an
explicit parameter rather than storing one, exactly the way
`go/printer.Fprint`, `go/format.Node`, and `token.FileSet.Position`
itself do in the standard library — `go/ast` values are never tied to
the specific `FileSet` that produced them. It's why `Workspace.Rebuild`
can swap `w.fset` wholesale via `AddExistingFiles` without touching a
single `File`: nothing owns the `FileSet`, so nothing goes stale when
it's replaced. Methods that only touch bytes/AST structure (`Symbols`,
`DiffDirectives`) take no `FileSet` at all — the split is exact, not a
default. Same for the
per-layer naming grammars: `Tx`'s verb categories and `View`'s narrow
identity-keyed accessors versus its whole-workspace scanners are on their
own type docs; the identity vocabulary itself lives beside each type's
own siblings rather than in a dedicated file — `PackagePath`/`PackageID`
(and `NewPackageID`'s agent-facing `_test`-suffix parsing) on
`internal/workspace/package.go`, `FilePath` on `internal/workspace/
file.go`; the tools-layer resolution seam (`package` vs `file`
arguments, import-path vs workspace-relative spelling, dependency
addresses excluded from writes)
is on `writeWorkspacePkg`/`readPackage` (`internal/tools/edit.go`,
`internal/tools/read.go`). Read the relevant type's doc comment before
extending it, rather than working from a second-hand summary that can go
stale on its own.
