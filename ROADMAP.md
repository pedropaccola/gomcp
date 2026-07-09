# Roadmap

Milestones we've agreed on but deliberately deferred, so they don't get lost.

## Mutation follow-ups

- Recheck v2: the post-edit reload is currently the whole workspace —
  correct by construction, sub-second at POC scale. When logged reload
  durations demand it, narrow to dirty packages plus transitive dependents
  via a stored import graph, and budget FileSet compaction into that change
  (today the full reload is what resets FileSet growth).
- Interface-method rename does not chase implementors to preserve
  satisfaction (gopls does); broken satisfactions arrive in the echo.
  Upgrade when it earns its complexity.
- External test package (_test-suffixed) creation is unsupported:
  create_file targets the production package.
- Mid-Tx reads are parse-fresh but type-stale; becomes observable only if a
  batch tool (multiple verbs per call) ever exists.

## Field notes — first live mutation exercise

- **v2:** renames don't touch prose — doc comments still say "Point" after
  Point→Coord. Consider gopls-style rewriting of the doc comment's leading
  identifier.

## Field notes — self-hosted development (move_declaration built through the toolset)

- **Floating comments are unreachable.** The layer headers in mutation.go /
  lookup.go and the tools.go package doc (the tool-prefix conventions line)
  are not declaration-attached, so no tool can update them. The mutation.go
  header and tools package doc are now slightly behind the code (no mention
  of move semantics / `move_*` prefix) until edited directly.
- **Placement policy vs semantic sections.** `insertOffset` puts new Tx
  methods after the last existing Tx method, not under the intended
  Creators/Editors/Refactorings section banner — the server can't know the
  banners exist. `MoveSymbol`, `extractDecl`, and `groupUsesIota` need a
  manual reshuffle into their sections.
- **Two address styles for files.** `list_files`/`list_symbols` speak
  workspace-relative paths (`internal/engine/mutation.go`) while mutation
  verbs demand bare names (`mutation.go`). Both refusals steer correctly,
  but the round-trip from a read output into a mutation input needs a
  mental conversion — consider accepting either form.

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
