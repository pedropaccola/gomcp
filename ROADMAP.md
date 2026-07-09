# Roadmap

Milestones we've agreed on but deliberately deferred, so they don't get lost.

## Mutation follow-ups

- Recheck v2: the post-edit reload is currently the whole workspace —
  correct by construction, sub-second at POC scale. When logged reload
  durations demand it, narrow to dirty packages plus transitive dependents
  via a stored import graph, and budget FileSet compaction into that change
  (today the full reload is what resets FileSet growth).
- Batch mutations: one tool call carrying several verbs in one transaction.
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
- **Placement policy vs semantic sections** (standing workflow note):
  `insertOffset` places new declarations by kind and receiver, not under
  the section banners — the server can't know banners exist. Finish
  self-hosted sessions with a by-hand reshuffle into sections.
- **Floating comments are unreachable, now deliberately.** Layer headers
  and package docs describe categories, never individual verbs, so adding
  a tool doesn't require touching them; verb-level documentation lives in
  doc comments (addressable) and README/AGENTS.md.

## Gaps

- **Read-only inspection of external packages by import path.** Everything
  outside the workspace root is invisible today (CleanPath rejects it by
  design), so an agent needing a dependency's API — the go-sdk, x/tools —
  must escape to raw file reads against the module cache, exactly the
  fallback this server exists to remove (it happened repeatedly while
  building this very project). Sketch: address dependencies by import path
  where workspace tools take a relative dir (e.g.
  describe_type("golang.org/x/tools/go/packages", "Config")); load them
  on demand with syntax via a targeted packages.Load; strictly Enumerators,
  Describers, and Finders — mutation verbs never resolve an import path.

## Housekeeping

- Flush is not atomic across files: a mid-flush I/O error leaves a partial
  write on disk (in-memory state stays consistent; re-flush recovers).
- One live-repo self-hosting smoke test remains (TestBootstrapLiveRepo),
  deliberately, behind `-short`. Everything else runs on fixtures.

## Fixed

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
