# gomcp: Compiler-Directive Awareness & Generated-File Read-Only Support

Status: implementation-ready, sharpened via `/grill-with-docs` interview. No code changed yet.
Re-verified against the codebase after the separate `internal/workspace` CRUD-verb refactor
(`PLAN-workspace-api.md`) — file:line citations below were replaced with symbol-name references,
since line numbers already went stale once mid-session and gomcp itself deliberately never
addresses by line for the same reason. A few Phase 3/4 specifics were also updated where that
refactor changed *where* the relevant logic actually lives, noted inline below.

## Context

gomcp's declaration-scoped AST model has no awareness of Go compiler directives (`//go:build`,
`//go:generate`, `//go:embed`) and no notion that a file might be machine-generated and therefore
off-limits to edit. This surfaced while scoping the Kubernetes benchmark (`benchmark/`, a separate
already-approved effort) and was worked through in detail via a structured interview. This document
is the settled result — what to build, in what shape, and why each shape was chosen over the
alternatives considered along the way.

**Directive scope, fixed early and holds throughout:** "directive" means `//go:build`, `//go:generate`,
`//go:embed` only — no space after `//`, matched by `^[a-z0-9]+:[a-z0-9]`. Legacy `// +build` and
Kubernetes-style `// +k8s:...` markers are explicitly out of scope everywhere in this plan: `+build`
is deprecated in favor of `//go:build` and not worth a second detection path, and `+`-marker comments
already work fine today (they have a leading space, so `go/ast.CommentGroup.Text()` never strips them).

**Scoped reload for codegen regeneration was considered and explicitly rejected.** The original draft
of this plan included a Phase 4 extending `Store.Reload`/`disk_reload` with a package-scoped variant,
reusing `recheckScopedLocked`'s carry-forward mechanism. Rejected because the justification doesn't
hold: `recheckScopedLocked` earns its complexity because type-checking runs after *every write* — at
that frequency, full-workspace rechecking would be untenable. Flush→regenerate→reload is nowhere near
that frequency; it's an occasional, deliberate step after a batch of edits, not a per-write tax. Paying
full-reload cost occasionally is a different bargain than paying it on every edit, and the mechanism
drags in real unresolved questions of its own (what `ReloadOutput`'s diagnostics scope should mean once
narrowed, whether to guard against reloading unflushed dirty state) for a saving that isn't needed at
this frequency. `Store.Reload`/`disk_reload` stay exactly as they are today.

## Phase 1 — First-class directive tracking (files and symbols)

Two things forced this into a real tracked field rather than a rendering-bug fix:

1. **`//go:build` requires isolation.** Go's grammar mandates a blank line between a leading directive
   block and whatever follows (doc comment or package clause), to distinguish a build constraint from
   documentation. Rendering a directive by simply blank-line-splitting an existing `doc` string would
   produce a comment group gomcp itself doesn't track — `ast.File.Doc` is only ever the group
   *immediately* preceding `package`, so the directive block would become invisible to `describe_files`
   forever, and (this is the concrete failure mode) unprotected against a later coarse-grained operation
   like `delete_files`+`create_files` that recreates the file with no way of knowing the directive was
   ever there.
2. **`//go:generate` has no placement constraint at all** (`go help generate`: it's a literal text scan,
   "does not parse the file," can sit anywhere). Left unconstrained, it can float free mid-file — the
   same general "floating comment can silently vanish" limitation gomcp already documents and accepts
   for any comment. This plan does not attempt to solve general floating-comment tracking (a separate,
   much larger initiative). Instead: **gomcp's own canonical convention is that any file-scoped
   directive lives in one ordered block at the top of the file, next to `//go:build`.** A `go:generate`
   the agent wants attached to one specific symbol still goes directly above that symbol — already fully
   tracked today via the existing `Doc`/`declSpan` mechanism, no change needed there, since a
   symbol-adjacent directive and its doc-comment prose are allowed to share one contiguous comment group
   (no blank-line isolation rule at the symbol level — that's only a file/`go:build` requirement).
   Genuinely mid-file, unattached directives remain the pre-existing, accepted limitation, now scoped
   out by explicit design rather than by accident.

**Shape:**
- `workspace.File` gains a tracked, ordered `[]string` of leading (pre-package) directive lines —
  not a single string, since a file can legitimately carry more than one (`//go:build` and a
  file-scoped `//go:generate` together).
- `Tx.CreateFile`/`Tx.EditFile` (`internal/store/tx.go`) gain a `directives []string` parameter
  alongside `doc`. Both still call `Workspace.SwapFile` directly (that hasn't changed), so the new
  parameter's rendering logic lives at the same call site the `doc`/`RenderDocComment` handling
  already does today.
- `Tx.EditFile`'s `doc` parameter must stop being collapsed through `optStr` before reaching this layer
  (`internal/tools/editors.go`'s `editFile` handler currently does `optStr(entry.Doc)`, discarding
  nil-vs-empty). Now that `directives` is independently settable, "touch only directives, leave doc
  alone" is a real case that needs `nil` (no change) distinguishable from an empty-but-present value
  (clear) all the way down, not just at the tool-input struct.
- Rendering rule (superseding the original "blank line after every directive line" idea, which was
  designed for a since-abandoned single-string-mixing model): directive lines render consecutively, no
  blank line between them, then exactly one blank line, then the doc comment (if any), then the
  `package` clause.
- Loading: detect existing leading directives at load time by scanning `File.Comments` — already parsed
  by `go/parser` regardless of AST attachment — for comment groups positioned before `Package.Pos()`
  matching the directive regex. No raw-text re-scan needed.

**Symbols get the same tracked-field treatment, symmetric with files — not just a write-side
parameter.** `workspace.Symbol` gains its own tracked `directives []string` field, populated at index
time (`IndexAST`/`RebuildIndex`, wherever `Symbol.Kind`/`Recv` are already computed) by scanning the
symbol's own leading comment group for directive-shaped lines — the same detection regex Phase 1 uses
for files, just applied to a different span. Refreshed any time the symbol's declaration is
reprocessed: initial load, or after `CreateSymbol`/`EditSymbol` installs new content. This is not
redundant with the write-side `Directives` parameter on `CreateSymbolEntry`/`EditSymbolEntry` (Phase 2)
— that parameter is the agent's *stated intent*, validated against `Source`; this tracked field is
gomcp's own *observed* record of what's actually in the declaration, independent of whether the agent
declared it. Phase 3's heuristic diff (the "omitted" case) compares against precisely this tracked
field's pre-edit value, rather than re-deriving "previous state" some other way.

## Phase 2 — Presentation layer (full blast radius, verified against current structs)

| Tool struct | File | Change |
|---|---|---|
| `CreateFileEntry` | `internal/tools/creators.go:12-16` | add `Directives []string \`json:"directives,omitempty"\`` (`Doc *string` already optional) |
| `EditFileEntry` | `internal/tools/editors.go:10-14` | same addition (`Doc *string` already optional) |
| `DescribeFileResult` | `internal/tools/describers.go:69-71` | add `Directives []string \`json:"directives,omitempty"\`` |
| `CreateSymbolEntry` | `internal/tools/creators.go:37-41` | add `Directives []string \`json:"directives,omitempty"\`` — no `Doc` field exists here today (doc is embedded inline in `Source`); this is a deliberate asymmetry, not a gap: see Phase 3 for the field's mechanical role |
| `EditSymbolEntry` | `internal/tools/editors.go:25-29` | same addition, same justification |
| `DescribeSymbolResult` | `internal/tools/describers.go:56-61` | add `Directives []string \`json:"directives,omitempty"\`` — reads the symbol's tracked `directives` field (see Phase 1) directly, not derived fresh per call |
| `DescribePackageResult` | `internal/tools/describers.go:80-83` | add `ExcludedFiles []string \`json:"excluded_files,omitempty"\`` |

**`ExcludedFiles` is free; the *reason* for exclusion isn't.** `packages.NeedFiles` is already set in
both of gomcp's `packages.Config` load-mode expressions (`internal/disk/disk.go:46,218`), which means
`Package.IgnoredFiles` — files excluded from the current build by their own build constraint — is
already computed on every load. It's simply never read anywhere in the codebase today
(`grep -rn "IgnoredFiles"`: zero matches). No load-mode change, no directory re-scan: just surface
`pkg.IgnoredFiles` directly. But `IgnoredFiles` is only ever a bare path list — `go/packages` never
computes *why* a file was excluded. Reporting the actual reason (e.g. "excluded by `//go:build
windows`") means reading that one leading-comment line directly out of each ignored file — a small,
bounded extra cost (one directive-line read per excluded file, not a full parse), layered on top of
the free win, not part of it. This is a distinct concept from `FileKind` (package role: Prod/XTest/
External) or Phase 4's `IsGenerated` marker — neither encodes a build-constraint reason, so the reason
has to come from the excluded file's own directive line, not from an existing field.

**Why symbols get `Directives` as a write-side field despite `Doc` not being separately parameterized:**
unlike files, a symbol's directive and its doc prose can coexist in one contiguous comment group with
no isolation requirement — so nothing *forces* a structural split the way `//go:build` does for files.
The field earns its place for a different reason (see Phase 3): when supplied, it lets gomcp validate
the agent's stated intent deterministically instead of guessing, and it's mirrored by a tracked
`workspace.Symbol.directives` field (Phase 1) recording what's actually observed in the declaration.

## Phase 3 — Dropped-directive protection, symbols only

Files are no longer at risk of silently losing a directive to an edit: directives live in their own
field now, never inferred from or spliced out of a replaceable `doc` span. The risk that motivated this
phase originally is specific to symbols, where the directive line still lives inline inside `Source`.

**Where this actually gets implemented, updated post-refactor:** `EditSymbol`'s real placement/
replacement logic no longer lives in `store`'s `Tx.EditSymbol` — it moved to `Workspace.EditSymbol`
(`internal/workspace/editing.go`) as part of the separate CRUD-verb redesign (`PLAN-workspace-api.md`).
`Tx.EditSymbol` is now a thin pass-through (calls `tx.ws.EditSymbol`, then `tx.markChanged`). This is
a better fit for this phase, not a complication: the validation described below needs the parsed
`Fragment`/`Source` comment data that `Workspace.EditSymbol` already has in hand at the exact point
it computes the replacement, rather than that data needing to cross the store/workspace boundary.
Implement the check inside `Workspace.EditSymbol` itself, and have it return the dropped-lines
result alongside its existing `(FilePath, error)` so `Tx.EditSymbol` can pass it through to
`WriteOutput` the same way it already passes through the touched path.

**Mechanical role of `EditSymbolEntry.Directives`:**
- **Supplied:** gomcp validates that each listed line appears verbatim in the corresponding leading
  comment of `Source`. Missing one is refused outright — deterministic, not a heuristic, since the
  agent made an explicit claim gomcp can just check.
- **Omitted:** heuristic fallback. Diff directive-shaped lines detected in the symbol's *previous*
  comment block against the new `Source`; anything present before and missing now is reported, never
  blocking, never auto-restored (an agent may deliberately mean to remove one). This is exactly what
  the symbol's tracked `directives` field (Phase 1) exists to make cheap — the "previous" state is
  already sitting on the `Symbol` being replaced, no separate lookup needed.

**Shape of the report:** reuse the existing `DiagnosticEntry`/`DiagnosticsTruncated` idiom
(`internal/tools/shared.go`) rather than a bare `[]string` — pkg/file/symbol-scoped entries plus a
truncation cap, consistent with how `WriteOutput` already reports everything else that "might be a lot."
Add e.g. `DroppedDirectives *DiagnosticsTruncated` to `WriteOutput` (`internal/tools/edit.go`),
populated only when non-empty, same `omitempty` discipline the rest of the struct already follows.

## Phase 4 — Generated files are read-only

New scope, motivated by the same read-only/replaceable relationship gomcp already has with dependency
packages (`workspace.KindExternal`), adapted to file granularity since "generated" doesn't work at
package granularity — a real package can hold both hand-written and generated files side by side.

**Detection:** a file is generated if it carries a line matching `^// Code generated .* DO NOT EDIT\.$`
before its first non-comment, non-blank content — the standard convention `go help generate` documents
and every codegen tool (including Kubernetes' own) already emits. Per the settled invariant: **this is
checked fresh at the moment of mutation, not cached as workspace state** — no persistent field to keep
in sync across reloads.

**Enforcement points — three, not one, and now cleaner post-refactor.** Investigated directly rather
than assumed: there is a true single convergence point for *installing new content* —
`Workspace.SwapFile` (`internal/workspace/primitives.go`) — reached by every Create/Edit/Delete-symbol,
Create/Delete-package, cross-package Move, rename, and relocate, either directly or via
`ApplyFileSplices`/`RelocateDeclaration`/`RelocateFile`/`MovePackage`. Two more paths never touch it:
`Workspace.DropFile` and `Workspace.DropPackage` (both now real `workspace`-level verbs as of the
CRUD-verb redesign — `DropPackage` used to be raw `Tombstone`+`RemoveUnit` composition living directly
in `Tx.DeletePackage`, which would have made this a fourth, messier site; it no longer is), which
tombstone rather than swap content, and same-package `Workspace.MoveFile`, which just renames the
existing `*File` in place with no content swap either. So the guard belongs at exactly three
`workspace`-level primitives — `SwapFile`, `DropFile`/`DropPackage`, and same-package `MoveFile`'s
rename path — not scattered across `Tx`'s functions at all anymore (a real improvement the CRUD-verb
redesign delivered as a side effect: every one of these is now a clean `workspace` verb, not `Tx`
composing raw primitives), and still not a single true choke-point; that's the separate, larger
architectural question about write-path indirection this plan continues to park, not resolve.

**Refusal shape:** mirror `KindExternal`'s existing pattern exactly
(`internal/tools/edit.go:57-66`: `"%q is a dependency: writes stay scoped to the workspace"`) — same
posture, same tone, applied to a file instead of a package: `"%q is generated: regenerate it
externally, then disk_reload"`.

## Critical files

Referenced by symbol, not file:line — line numbers already went stale once mid-plan from an unrelated
refactor, and gomcp's own tools deliberately never address by line for the same reason.

- `workspace.File` (`internal/workspace/file.go`) — where the new tracked directive-lines field and
  `IsGenerated`-style detection belong.
- `workspace.Symbol` (`internal/workspace/symbol.go`) — where the symbol-level tracked `directives`
  field belongs.
- `Tx.CreateFile`/`Tx.EditFile` (`internal/store/tx.go`) — new `directives` parameter, `doc`'s
  nil-vs-empty fix. Unchanged by the CRUD-verb refactor — both still call `Workspace.SwapFile` directly.
- `Workspace.EditSymbol` (`internal/workspace/editing.go`) — where Phase 3's validation/drop-detection
  actually belongs post-refactor (moved here from what used to be `Tx.EditSymbol`'s own body); returns
  the dropped-lines result alongside its existing `(FilePath, error)`.
- `Workspace.CreateSymbol` (`internal/workspace/placement.go`) — the equivalent home for any
  Phase 1 directive-population logic on the create path.
- `internal/workspace/fragments.go` — `RenderDocComment` (moved here from `internal/store` by the
  CRUD-verb refactor, now exported); still relevant for rendering `doc` itself, but no longer
  responsible for directive-grammar correctness now that directives are their own field.
- `Workspace.SwapFile`, `Workspace.DropFile`, `Workspace.DropPackage`, same-package `Workspace.MoveFile`
  (`internal/workspace/primitives.go`) — Phase 4's three enforcement points, all now real `workspace`
  verbs (see Phase 4's updated note on `DropPackage`).
- `internal/tools/creators.go`, `editors.go`, `describers.go`, `edit.go` — Phase 2/3's full presentation
  blast radius, table above. Unchanged by the CRUD-verb refactor (tools layer wasn't touched).
- `internal/disk/disk.go` (`LoadInto`, `FetchExternal`) — confirms `NeedFiles`/`IgnoredFiles` already
  available, no load-mode change needed for Phase 2's `ExcludedFiles`.
- `workspace.KindExternal` (`internal/workspace/package.go`) — the precedent Phase 4's refusal shape
  and posture are modeled on.
- `AGENTS.md`, `ROADMAP.md` (Known Limitations section) — where this capability set gets documented/
  logged once shipped.

## Verification

1. Phase 1: create a file with both a `//go:build` line and a file-scoped `//go:generate` line via the
   new `directives` parameter; confirm the rendered bytes have no blank line between the two directive
   lines, exactly one blank line before the doc comment, and that `go/parser`/`go/build` recognizes the
   result as a real build constraint on re-parse. Confirm loading an existing file with a leading
   directive block populates the tracked field correctly (scan `File.Comments`, not raw text).
2. Phase 2: `describe_files`/`describe_symbols`/`describe_packages` against fixtures exercising each new
   field; confirm `ExcludedFiles` reports a real GOOS-excluded file with zero change to load mode.
3. Phase 3: `EditSymbolEntry` with `Directives` supplied but a `Source` that omits one of them — confirm
   refusal. Same edit with `Directives` omitted entirely — confirm the heuristic fallback reports the
   drop non-blockingly in `WriteOutput.DroppedDirectives`, and that the edit still succeeds.
4. Phase 4: attempt each of `create_symbols`/`edit_symbols`/`delete_symbols`/`edit_files`/`delete_files`/
   `refactor_move_file`/`refactor_move_symbol` (both directions) against a fixture file carrying the
   `// Code generated ... DO NOT EDIT.` marker; confirm every one refuses with the documented message,
   and that no persistent "generated" flag is left behind between calls (detection is re-checked fresh
   each time, per the settled invariant).

## Explicitly out of scope

- Legacy `// +build`, Kubernetes `+`-marker comments (already work, not touched).
- General floating-comment tracking (mid-file, unattached to any file-leading position or symbol) —
  same accepted limitation as today, just now an explicit design choice rather than an oversight.
- Scoped `disk_reload` for codegen regeneration — considered, rejected, see Context.
- The broader question of write-path indirection through `internal/workspace` (multiple entrypoints for
  what's conceptually one operation) — real, parked for a separate investigation.
