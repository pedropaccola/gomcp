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
