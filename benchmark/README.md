# gomcp vs. Vanilla Benchmark: Kubernetes Feature Re-Implementation

## Goal

`gomcp`'s `README.md` makes bold, quantifiable-sounding claims about being more token-efficient
and safer than raw file-editing tools for Go coding agents. Those claims have never been tested
against a baseline agent using plain `Read`/`Write`/`Edit`/`Grep` tools. This benchmark validates
or falsifies each claim (see `claims.md`) with a real, big, high-blast-radius Go feature,
implemented twice — once by an agent wired to `gomcp`, once by an agent with only vanilla file
tools — from the same implementation plan, then compares token usage, tool-call shape, and (as a
non-negotiable correctness gate) whether the resulting code actually passes the target feature's
own tests.

## Shape

- **Two arms**, same model/config: Arm A = `gomcp` MCP server + `Bash` scoped to `go*` subcommands
  only (`go build`/`test`/`vet`/`generate`, no general shell — gomcp isn't meant to replace the Go
  toolchain, only raw file editing). Arm B = `Read`/`Write`/`Edit`/`Grep`/`Glob`/`Bash` (`git diff`
  additionally allowed, unscoped since Arm B has no structured alternative to shell-based
  inspection). Both arms have symmetric access to the acceptance criteria (see the correctness
  gate below) — this benchmark compares tool efficiency on a fixed, shared task, not agent
  design-skill on an unseen one, so exposing what "done" means identically to both arms doesn't
  bias the comparison; it's necessary for the comparison to mean anything.
- **3 Kubernetes features × 2 arms × 2 reruns = 12 runs** (see `features/`).
- **Correctness gate (primary, binary)**: the feature's own in-tree tests must pass against each
  arm's resulting diff, checked *after* a fixture-integrity check confirms neither the pre-applied
  test files nor the pre-applied generated files were touched.
- **Tests and generated code are pre-applied, never authored by the agent.** The real upstream
  files from the actual historical PR, copied in verbatim before either agent starts.
- **Secondary, non-gating check**: an LLM judge compares each produced diff against the real
  historical diff, to explain *why* a run failed or whether a passing run took a shortcut.

Full mechanics — worktree setup, invocation, measurement, grading, confound control — are in the
original plan document (`/home/me/.claude/plans/here-s-the-planning-goal-breezy-robin.md`); this
directory is that plan's implementation, not a re-statement of it. Read `claims.md` for exactly
what's under test and why each citation is current, not assumed.

## Layout

```
benchmark/
  README.md                — this file
  claims.md                — the 10 claims, each with an exact, re-verified citation
  configs/
    gomcp-mcp-config.json  — template; -cwd filled in per worktree at run time
    empty-mcp-config.json  — {"mcpServers": {}} for Arm B
  features/
    backofflimit/           — KEP-3850, kubernetes/kubernetes#118009
    schedulinggates/        — KEP-3521, kubernetes/kubernetes#113275 (+#113442)
    vap/                     — KEP-3488, kubernetes/kubernetes#113314 (scoped down — see its own README)
    each with:
      implementation_plan.md, grading.md, reference-tests/, reference-generated/, MANIFEST.sha256
  scripts/
    setup_worktrees.sh      — creates the 4 worktrees per feature off the pinned pre-feature commit
    run_arm.sh              — single invocation wrapper (feature, arm, run-index) -> claude -p ...
    extract_metrics.py      — parses a session JSONL transcript into the token/tool-call breakdown
    grade.sh                — freezes diff, checks disk_flush occurred, runs target tests + go vet
  results/
    results_template.md     — the empty 18-row reporting table (12 runs + 6 per-feature aggregates)
    runs/                   — raw transcripts, diffs, per-run metrics json land here (gitignored)
```

The Kubernetes clone and its worktrees are **not** nested under `benchmark/` —
`scripts/setup_worktrees.sh` takes an external path via `$BENCH_K8S_ROOT` (default `~/k8s-bench/`)
so this repo never carries a multi-GB checkout.

## Setup

One-time, before running any feature. Everything below assumes Claude Code with Sonnet 5
(`claude-sonnet-5`), which is what `scripts/run_arm.sh` pins via `--model`.

1. **Prerequisites on `$PATH`**: `claude` (Claude Code CLI), `go` (matching this repo's
   `go.mod` version — check `go version` against it), `git` ≥ 2.25 (worktree support), `gh`
   (authenticated: `gh auth status`; used only by feature-*building* scripts, not by the run
   scripts themselves, but `setup_worktrees.sh`'s first-time clone needs plain `git`/network
   access to GitHub), `python3` (for `extract_metrics.py`), `sha256sum`.
2. **Pin the CLI version once, before any real run** — auto-update mid-benchmark would silently
   change what's being measured:
   ```bash
   claude --version   # record this; don't let it drift across the 12 runs
   ```
3. **Build the `gomcp` binary once** — the Arm A runs use the built binary, never `go run`, since
   the server's behavior is tied to what's actually running, not to source that might have moved
   on:
   ```bash
   cd /home/me/pedro/gomcp
   go build -o gomcp-binary ./cmd/gomcp
   # scripts/run_arm.sh looks for it at $GOMCP_BINARY, default ../gomcp-binary
   # relative to benchmark/scripts/ — i.e. exactly this path. Override with
   # GOMCP_BINARY=/abs/path if you build it elsewhere.
   ```
4. **Point `BENCH_K8S_ROOT` somewhere with room for a multi-GB checkout plus 4 worktrees per
   feature** (12 worktrees total across all 3 features, sharing one blobless object store):
   ```bash
   export BENCH_K8S_ROOT=~/k8s-bench   # add this to your shell profile for the session
   ```
5. **Create the worktrees for a feature** (clones `kubernetes/kubernetes` blobless into
   `$BENCH_K8S_ROOT/kubernetes` on first call, then adds 4 worktrees off that feature's
   `PINNED_SHA` and commits the `BASELINE-FIXTURES` layer into each):
   ```bash
   ./scripts/setup_worktrees.sh backofflimit
   ./scripts/setup_worktrees.sh schedulinggates
   ./scripts/setup_worktrees.sh vap
   ```
   Sanity-check each feature once before running any agent against it — the printed command at
   the end of `setup_worktrees.sh`'s output (`go build ./...` in one of the fresh worktrees)
   should succeed; if it doesn't, the pinned commit or fixture copy is broken, not the agent.

## Verification

Do this once per feature (or once total for steps 2-3, which aren't feature-specific) before
trusting any of the 12 real runs. It catches a broken pinned commit, a stale CLI, a broken metrics
extractor, or a broken grading gate before blaming an agent's diff for any of those.

1. **Worktree setup sanity check** — confirms the pinned commit itself isn't broken before it's
   used as a baseline:
   ```bash
   ./scripts/setup_worktrees.sh backofflimit
   cd $BENCH_K8S_ROOT/backofflimit-gomcp-run1 && go build ./...
   ```
   (Already folded into step 5 of Setup above — this is the same check, called out again here
   because skipping it is the single most likely way to waste a real run on a broken baseline.)

2. **Throwaway `-p` invocation per arm** — no real task, just pins CLI behavior before spending a
   real run on it:
   ```bash
   claude --version   # record this; don't let it auto-update mid-benchmark
   claude -p "say hello" --mcp-config configs/empty-mcp-config.json --strict-mcp-config \
     --model claude-sonnet-5 --permission-mode bypassPermissions --output-format json
   ```
   Confirms the `--output-format json` shape is what `extract_metrics.py` expects, on both the
   `gomcp` config and the empty one.

3. **Sanity-check the metrics extractor against one real transcript**:
   ```bash
   ./scripts/run_arm.sh backofflimit gomcp 1
   # copy the transcript per run_arm.sh's printed reminder, then:
   python3 scripts/extract_metrics.py results/runs/backofflimit-gomcp-1/transcript.jsonl
   ```
   Manually eyeball the four token numbers and tool-call breakdown against what the transcript
   actually shows before trusting the extractor across all 12 runs.

4. **Validate `grade.sh` against ground truth** — confirms the grading gate itself isn't broken,
   before it's used to judge either agent arm:
   ```bash
   # in a scratch worktree off the same PINNED_SHA, apply the real historical PR's production
   # diff (fetched separately, not an agent's output), then:
   ./scripts/grade.sh backofflimit gomcp 1
   ```
   The real upstream diff should pass `TEST_CMD` and `go vet` cleanly. If it doesn't, the grading
   command in that feature's `grading.md` is wrong — fix it there, not by excusing an agent's
   later failure against the same gate. No `claude` call needed for this step.

5. **Confirm the fixture-integrity check actually catches tampering**:
   ```bash
   # in a scratch worktree, hand-edit one file under reference-tests/ and one under
   # reference-generated/ (e.g. add a blank line to each), then:
   ./scripts/grade.sh backofflimit gomcp 1
   ```
   Should report `fixture_integrity: VIOLATED` and route straight to manual review, without
   running any tests. No `claude` call needed for this step either.

Cost note: step 1 is disk/network only (the clone). Steps 4-5 need no `claude` API calls at all —
only 2 (one small throwaway call) and 3 (one real feature run) spend real API cost, and 3 doubles
as the first real data point rather than being pure overhead.

## Running a session

```bash
./scripts/run_arm.sh backofflimit gomcp 1
./scripts/run_arm.sh backofflimit vanilla 1
./scripts/run_arm.sh backofflimit gomcp 2
./scripts/run_arm.sh backofflimit vanilla 2
# ... repeat for schedulinggates and vap: 3 features x 2 arms x 2 reruns = 12 invocations total.
# Interleave (feature,A,1)(feature,B,1)(feature,A,2)(feature,B,2) across features rather than
# blocking all-A-then-all-B, so any mid-benchmark environment drift lands on both arms.
```

Each invocation runs one non-interactive `claude -p` session (`--permission-mode bypassPermissions`,
`--output-format json`, arm-scoped `--allowedTools`, `--strict-mcp-config` so no stray
`.mcp.json`/global server registration leaks into either arm) against one worktree, writing
`results/runs/<feature>-<arm>-<run>/result.json` and `stderr.log`. A run has no built-in timeout in
the script itself — wrap it yourself if you want a safety net (60-90 min is reasonable; a run that
hits it should be voided and rerun at the same slot, not scored as a loss):
```bash
timeout 90m ./scripts/run_arm.sh backofflimit gomcp 1
```

**Immediately after each run**, copy its transcript out of Claude Code's session log before
running the same feature/arm combination again — the log path is keyed by working directory, so a
second run against the same worktree overwrites the first run's transcript:
```bash
cp ~/.claude/projects/-<escaped-worktree-path>/*.jsonl \
   results/runs/backofflimit-gomcp-1/transcript.jsonl
```
(`run_arm.sh` prints the exact reminder with the real path at the end of each invocation.)

## Grading a run

```bash
./scripts/grade.sh backofflimit gomcp 1
python3 scripts/extract_metrics.py results/runs/backofflimit-gomcp-1/transcript.jsonl
```

`grade.sh` freezes the diff against that worktree's `BASELINE-FIXTURES` commit, checks fixture
integrity first, confirms `disk_flush` was called (Arm A only), runs the feature's `TEST_CMD` (from
`grading.md`) plus `go vet` on touched packages, and writes `results/runs/<cell>/verdict.txt`. It
also prints the separate, manual LLM-judge invocation — run that identically regardless of the
binary gate's outcome.

## Teardown

Worktrees are disposable once graded and their transcripts/diffs are copied out — nothing under
`$BENCH_K8S_ROOT` needs to survive between benchmark sessions:
```bash
# per worktree, once you're done with it:
git -C "$BENCH_K8S_ROOT/kubernetes" worktree remove "$BENCH_K8S_ROOT/backofflimit-gomcp-run1"
# or, to remove every worktree for a feature in one pass:
for d in "$BENCH_K8S_ROOT"/backofflimit-*-run*; do
  git -C "$BENCH_K8S_ROOT/kubernetes" worktree remove "$d"
done
# the shared clone (and its object store, reused by every feature/arm/run) can stay for the
# next benchmark session, or be removed entirely if you're done:
rm -rf "$BENCH_K8S_ROOT"
```
`results/runs/` is gitignored (see `.gitignore`) — nothing there needs manual cleanup before a
commit, but it isn't cleaned up automatically either; remove it yourself once results are recorded
in `results_template.md` if you want the disk back.

## How to read results

Fill `results/results_template.md`'s 12 run-rows first, from `grade.sh`'s and
`extract_metrics.py`'s output, *before* looking back at the qualitative failure-mode notes — write
those from the transcript/diff alone, so the prose isn't unconsciously shaped by a number already
seen. Then the 6 per-feature aggregate rows. A `fixture_integrity: VIOLATED` run is never averaged
into an aggregate; it goes to manual review instead.
