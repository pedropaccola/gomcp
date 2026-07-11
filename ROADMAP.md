# Roadmap

Milestones we've agreed on but deliberately deferred, so they don't get lost.

## Priority: batch mutations (before the oracle)

Promoted by evidence: the state-extraction sessions burned quota on
sequential edits whose echoes shrank one diagnostic at a time (1st edit:
99 diagnostics, 2nd: 98, ...) — every intermediate recheck and its echo
paid for, when only the final state mattered.

Design, agreed:
- **Creators and Editors only.** Refactorings are excluded — their
  processing scope (multi-site, semantic scans) can't be known upfront.
  Creators and Editors already share MutationOutput, which keeps the
  batch's echo schema trivial.
- **Collision = address containment, not string equality.** Editors span
  scopes (edit_declaration is declaration-scoped; delete_file/package are
  structural), so two statements conflict when one's target contains the
  other's — delete_package(X) collides with any statement inside X. The
  address hierarchy (import path ⊃ file ⊃ key) makes containment a prefix
  check. Collision-free batches apply in submitted order (creators may
  depend on each other: create_package then create_declaration into it),
  type-check once at the end, echo one final delta/resolved.
- **Validate all, then apply any.** Every verb is precondition-checked;
  dry-run every statement's preconditions against the evolving Tx before
  the first splice. Safety is identical (the clone rolls back wholesale),
  but the agent gets every failure in one round-trip instead of fixing a
  batch one refusal at a time.
- **Make the categories architectural.** Creators/Editors/Refactorings
  are prose today; give every verb a typed descriptor —
  {category, scope: package|file|declaration, address} — so the batch
  picks up eligible verbs automatically, the collision check reads scope
  off the descriptor, and a future refactoring verb (no descriptor)
  *cannot* be wired into the batch by mistake. Same philosophy as the
  state extraction: agreements into architecture.

## Priority 2: describe_package (package-level docs)

Sequenced after batch mutations. Surfaced while auditing whether an agent
can read this codebase's own documentation through its own tools: it
cannot. No tool returns a package's doc comment — `list_packages` reports
name/path only — and today no package in `internal/engine` even has one
in the Go sense: engine.go/lookup.go/mutation.go's file-level overviews
sit after the import block, separated from the first declaration by a
blank line, so they're floating comments, attached neither to the package
clause nor to any Symbol.Doc. `describe_type`/`describe_function` can't
surface them for the same reason `DocOf` can't: nothing points at them.

Design sketch:
- New tool `describe_package(package)`: returns the package's godoc — the
  comment block directly above `package X`, concatenated across files per
  Go's own rule when it's split — plus the cheap metadata list_packages
  already computes. Companion to the doc argument sketched on create_file
  (Mutation follow-ups, below): write through create_file, read through
  describe_package.
- Needs `Package.Doc()` derived the way everything else in the model is
  derived (RebuildIndex's philosophy): scan `Files()` for the file whose
  package clause carries a doc comment, recomputed on every rebuild,
  never stored — no new mutable state for one more derived value.
- Once this exists, the field note below ("layer headers stay
  category-generic until they migrate into package docs") becomes
  actionable: state.go's and tools.go's headers are already real package
  docs and become addressable immediately; engine.go/lookup.go/
  mutation.go's overviews are still floating and would need to move above
  their package clause to actually become one — a decision for when this
  lands, not before.

## Diagnostics presentation

- **Idea written by the user: optionality of scoping diagnostics().**.
  If the agent requests for additional diagnostics, we can leverage
  optional output scope. That move speak towards the same "do not drown
  the agent in diagnostic messages" philosophy. Low cost, must validate
  interface.
- **Optional: speak addresses, not positions.** Clients shouldn't need
  line numbers — a diagnostic's natural coordinates in this interface are
  (package, file, declaration, message), resolved via SymbolAt the way
  search_references already does. Open question: whether a
  declaration-relative line adds anything once the declaration is named.

- Companion (after batch): **the equivalence oracle** (test-side only).
  `assertModelEqualsDisk(t, e)` flushes to a temp copy, bootstraps a fresh
  engine on it, and diffs the two worlds — symbols, sources, diagnostics.
  Mutation tests call it last; a FuzzVerbs target feeds it random verb
  sequences. One property subsumes the structural invariants: the
  in-memory model must be indistinguishable from a cold bootstrap of its
  own flushed state. Construction (the state package) prevents; the
  oracle catches the rest.

## Known bugs

- **edit_declaration on an iota group's first member collapses the group.**
  Replacing the spec that carries `T = iota` makes every bare sibling
  inherit the new expression — all constants silently equal one value.
  iota groups are *partially* supported (move_declaration refuses them,
  replacement doesn't), which makes this a bug, not a gap. Hit live
  during the state extraction; the delta echo caught the duplicate-case
  breakage immediately and finishing the group healed it. Fix alongside
  the broader additional-Go-syntax expansion: extend the same
  position-dependence guard to spec replacement.

## Mutation follow-ups

All of the below were sequenced after the state extraction, now landed
(see Fixed) — every one is unblocked and will be written against the
state primitives.

- Package-level documentation through the file verbs. Go concatenates
  every file's package doc; today no doc comment is agent-addressable, so
  packages can never be documented through the server. Sketch: an
  optional doc argument on create_file (and maybe rename_file) that
  renders as the file's package doc comment — giving the agent exactly
  one sanctioned door into floating-comment space without making
  comments addressable in general. Read-side companion: `describe_package`
  (Priority 2, above).

- Recheck v2: the post-edit reload is currently the whole workspace —
  correct by construction, sub-second at POC scale. When logged reload
  durations demand it, narrow to dirty packages plus transitive dependents
  via a stored import graph, and budget FileSet compaction into that change
  (today the full reload is what resets FileSet growth). Implemented
  inside the state package, behind the same accessors — verbs and lookups
  never learn which recheck ran.
- Batch mutations: promoted to Priority above (design agreed there).
  The engine already amortizes — Edit rechecks once per Tx, not per verb —
  so batching collapses N tool-call rechecks into one. Preferred over
  incremental invalidation for as long as it holds; makes mid-Tx
  type-staleness observable (below).
- Safe-move vocabulary — the refactoring-browser game: make big changes
  using only behavior-preserving moves, refereed by an empty diagnostics
  delta. In rough order of fit: `move_declaration` across packages
  (qualifier rewrites via `gatherUses`, imports via the self-repair pass);
  `extract_interface` from a type's method set (purely additive);
  `inline_function` / `inline_constant` with refuse-on-doubt guards;
  `change_signature` driven by an explicit argument mapping (refuses
  functions used as values — no call site to rewrite). Parked:
  `extract_function`/`extract_variable` operate on statement ranges inside
  bodies, breaking declaration-scoped addressing.
- Fine-grained modification: whole-declaration replacement is the dominant
  token cost of self-hosted work — a one-line change to a 190-line
  declaration costs the whole declaration in the request. Sub-declaration
  addressing doesn't fit the current model, but if the weight keeps
  bearing, find a way. Candidates: anchored splices within a declaration,
  or statement-range addressing bridged from SymbolAt.
- Interface-method rename does not chase implementors to preserve
  satisfaction (gopls does); broken satisfactions arrive in the echo.
  Upgrade when it earns its complexity.
- External test package (_test-suffixed) creation is unsupported:
  create_file targets the production package.
- Mid-Tx reads are parse-fresh but type-stale; becomes observable once the
  batch tool exists.

## Field notes — first live mutation exercise

- **v2:** renames don't touch prose — doc comments still say "Point" after
  Point→Coord. Consider gopls-style rewriting of the doc comment's leading
  identifier.

## Field notes — self-hosted development

The server now routinely builds itself (move_declaration, the address
uniformization, and reload were all written through its own tools). What
that practice taught:

- **The delta echoes carry the work.** Changing an output type reported the
  exact handler lines it broke; each fix came back in `resolved`. Whole
  migrations ran without a single build to check compile status.
- **Floating comments are an architectural stance, not a gap.** The
  root incompatibility: a floating comment's meaning *is* its position —
  it annotates whatever happens to sit near it — and position is exactly
  what this server owns and rearranges (the placement policy positions
  declarations semantically, moves and creations reorder files). Position-
  dependent semantics cannot be preserved by semantic positioning; a
  floating comment under this engine is prose waiting to silently detach
  from its subject. So our own code commits to not needing them:
  organization belongs in structure (packages, typed verb descriptors
  once the batch work lands), prose belongs in package docs and
  declaration doc comments — the two comment forms whose attachment is
  structural, which is why they survive every mutation. If code wants a
  floating comment, the code isn't structured correctly. Consequences:
  section banners retire as the category subdivision becomes typed; the
  placement policy's ordering is accepted as given (no more by-hand
  reshuffle once banners go); layer headers stay category-generic until
  they migrate into package docs. Supporting floating comments as
  addressable objects remains possible future work, explicitly low
  priority — it would require giving them an address, i.e. an anchor,
  i.e. making them not float.

## Housekeeping

- Flush is not atomic across files: a mid-flush I/O error leaves a partial
  write on disk (in-memory state stays consistent; re-flush recovers).
- One live-repo self-hosting smoke test remains (TestBootstrapLiveRepo),
  deliberately, behind `-short`. Everything else runs on fixtures.

## Fixed

- **Diagnostics pagination.** Every scoped `DiagBlock` — list_*/describe_*
  output and mutation echoes — is capped at `diagLimit` (`diagStrings` in
  read.go) and closed with "+N more diagnostics: run the diagnostics tool
  for the full inventory" when truncated; the `diagnostics` tool builds
  its own list directly from `AllDiagnostics` and never goes through
  `diagStrings`, so it stays the uncapped inventory by construction, not
  by a separate no-cap branch. The cap is a process-wide flag,
  `-diagnostics-limit` (default 20), set once via `tools.SetDiagLimit`
  ahead of `Register` in cmd/gomcp/main.go — deliberately a package
  var-and-setter rather than a parameter threaded through every one of
  the ~8 call sites, since it's genuinely singleton config (one server
  process, one cap) and not per-instance state like `Engine`. Negative
  values are ignored; 0 is legal and shows only the "+N more" counter.

- **State extraction.** The informal agreements became architectural: the
  trusted core lives in `internal/engine/state`, where a Workspace with
  unexported fields owns units, tombstones, both FileSets, the dependency
  cache, and workspace diags, and File/Package seal their hot fields
  (src/ast/dirty, files/symbols) behind sorted-only accessors. Every
  write is a named primitive: SwapFile (parse-enforcing,
  external-refusing — the mutation path's only content door, which
  RenamePackage re-enters via CloneShell) and AddLoadedFile (the load
  path's, storing the type checker's own ASTs clean), DropFile/MoveFile
  and load-side PruneFile, InstallUnit/RemoveUnit, the Tombstone family,
  MarkFlushed/Unit.MarkDirty for the dirty lifecycle, Clone, Reset,
  SwapLoaded. Canonical bytes, rebuild-never-patch, the reload choke
  point, and determinism are now properties of what compiles — a new verb
  cannot violate the grammar and still build. The primitives are the
  narrow waist: verbs, batching, and granular addressing grow above them;
  invalidation strategy and FileSet management evolve below them (recheck
  v2 lands inside the state package, generalizing the external cache's
  own-FileSet pattern). state_test.go white-box-tests the primitives;
  engine tests read only the public accessors.

- **Two address styles for files.** Resolved by the interface
  uniformization, then completed by the identity re-key: package addresses
  are import paths everywhere — the type checker's identity, one grammar
  for workspace and (future) external packages — with bare workspace
  directories accepted and module-prefixed at the gate (`canonPkg`); `*.go`
  names are never packages (refused, not stripped). File arguments are bare
  names within their package (a path is tolerated when its package agrees;
  contradictions refused), and outputs mirror the split — bare names when
  the package was the input, package-keyed maps
  (`{"example.com/mod/pkg": ["file.go"]}`) across packages. Diagnostics
  strings stay `path:line:col`: positional prose, not addresses. The engine
  is keyed by `PkgPath`; files stay `RelativePath` (disk truth), converting
  only at the disk boundary (`dirOf`/`pkgAt`).
- **Reconnect-to-refresh.** `reload` is flush's inverse: rebuild from disk,
  discarding unflushed work (reported per package). Manual edits and git
  operations no longer force a reconnect; only behavior/schema changes to
  the server binary do.
- **Read-only inspection of dependencies by import path.** The last escape
  hatch, closed: any importable package — third-party or stdlib — answers
  `list_*`/`describe_*` by import path (verified live on the go-sdk's own
  `mcp.Tool`). Exported API only, lazily cached with its own FileSet (a
  recheck cannot invalidate cached positions), negative results cached,
  everything reset with the workspace snapshot. Mutations refuse
  dependencies by name; semantic finders stay in the workspace — a
  dependency's type universe cannot be matched exactly against the
  workspace's, and approximation is never the fallback.
