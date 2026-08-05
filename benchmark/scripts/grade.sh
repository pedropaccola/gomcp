#!/usr/bin/env bash
# grade.sh <feature> <gomcp|vanilla> <run-index>
#
# Grading pipeline for one run, in the order that matters:
#   1. Freeze the diff against the baseline-fixtures commit.
#   2. Fixture-integrity check FIRST - recompute checksums over
#      reference-tests/ and reference-generated/ against MANIFEST.sha256.
#      A mismatch routes straight to manual review; no pass/fail is
#      reported for this run until then.
#   3. For the gomcp arm, confirm the transcript shows a final disk_flush
#      call - if missing, the worktree may be stale relative to what the
#      agent believes it produced (a real, scoreable failure per this
#      repo's own AGENTS.md discipline, not a harness bug).
#   4. Run the feature's exact target test command (grading.md) plus
#      `go vet` on touched packages, recorded separately.
#   5. Binary pass/fail = fixture-integrity clean AND target tests pass.
#      This is the primary, non-negotiable correctness gate - see
#      benchmark/README.md for why it's restored to binary/pre-applied
#      rather than a hidden or self-authored check.
#   6. The LLM-judge pass (real historical diff vs. produced diff) is
#      NOT run by this script - it's a separate, manual step (or a
#      follow-on script invoking `claude -p` with a comparison prompt),
#      since it needs an actual model call, not shell logic. Run it
#      identically regardless of this script's pass/fail result.
set -euo pipefail

FEATURE="${1:?usage: grade.sh <feature> <gomcp|vanilla> <run-index>}"
ARM="${2:?usage: grade.sh <feature> <gomcp|vanilla> <run-index>}"
RUN="${3:?usage: grade.sh <feature> <gomcp|vanilla> <run-index>}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FEATURE_DIR="$BENCH_DIR/features/$FEATURE"
K8S_ROOT="${BENCH_K8S_ROOT:-$HOME/k8s-bench}"
WT_PATH="$K8S_ROOT/${FEATURE}-${ARM}-run${RUN}"
OUT_DIR="$BENCH_DIR/results/runs/${FEATURE}-${ARM}-${RUN}"

[[ -d "$WT_PATH" ]] || { echo "worktree missing: $WT_PATH" >&2; exit 1; }
[[ -f "$FEATURE_DIR/grading.md" ]] || { echo "missing $FEATURE_DIR/grading.md" >&2; exit 1; }
[[ -f "$FEATURE_DIR/MANIFEST.sha256" ]] || { echo "missing $FEATURE_DIR/MANIFEST.sha256" >&2; exit 1; }
mkdir -p "$OUT_DIR"

BASELINE_SHA="$(git -C "$WT_PATH" log --grep='^BASELINE-FIXTURES' --format=%H -1)"
[[ -n "$BASELINE_SHA" ]] || { echo "could not find BASELINE-FIXTURES commit in $WT_PATH" >&2; exit 1; }

echo "=== 1. freezing diff against baseline $BASELINE_SHA ==="
git -C "$WT_PATH" add -A
git -C "$WT_PATH" diff --cached "$BASELINE_SHA" > "$OUT_DIR/produced.diff"
echo "diff frozen: $OUT_DIR/produced.diff ($(wc -l < "$OUT_DIR/produced.diff") lines)"

echo "=== 2. fixture-integrity check ==="
INTEGRITY_OK=true
( cd "$WT_PATH" && sha256sum --check "$FEATURE_DIR/MANIFEST.sha256" ) \
  > "$OUT_DIR/fixture_integrity.log" 2>&1 || INTEGRITY_OK=false

if [[ "$INTEGRITY_OK" == false ]]; then
  echo "fixture_integrity: VIOLATED - see $OUT_DIR/fixture_integrity.log"
  echo "fixture_integrity: VIOLATED" > "$OUT_DIR/verdict.txt"
  echo "routing to manual review; no test pass/fail reported for this run."
  exit 0
fi
echo "fixture_integrity: clean"

FLUSH_NOTE="n/a (vanilla arm)"
if [[ "$ARM" == "gomcp" ]]; then
  echo "=== 3. checking disk_flush occurred (gomcp arm only) ==="
  if [[ -f "$OUT_DIR/transcript.jsonl" ]] && grep -q '"name":"mcp__gomcp__disk_flush"' "$OUT_DIR/transcript.jsonl"; then
    FLUSH_NOTE="disk_flush called"
  else
    FLUSH_NOTE="flush-forgotten: no disk_flush call found in transcript - worktree may be stale relative to what the agent believes it produced"
    echo "WARNING: $FLUSH_NOTE"
  fi
fi

echo "=== 4. running target tests + go vet ==="
TEST_CMD="$(grep -m1 '^TEST_CMD=' "$FEATURE_DIR/grading.md" | cut -d= -f2-)"
[[ -n "$TEST_CMD" ]] || { echo "no TEST_CMD= line found in $FEATURE_DIR/grading.md" >&2; exit 1; }

TESTS_OK=true
( cd "$WT_PATH" && eval "$TEST_CMD" ) > "$OUT_DIR/test_output.log" 2>&1 || TESTS_OK=false

VET_OK=true
VET_PKGS="$(git -C "$WT_PATH" diff --cached --name-only "$BASELINE_SHA" -- '*.go' | xargs -n1 dirname | sort -u | sed 's#^#./#')"
if [[ -n "$VET_PKGS" ]]; then
  ( cd "$WT_PATH" && go vet $VET_PKGS ) > "$OUT_DIR/vet_output.log" 2>&1 || VET_OK=false
fi

echo "=== 5. verdict ==="
{
  echo "fixture_integrity: clean"
  echo "tier1_pass: $TESTS_OK"
  echo "vet_clean: $VET_OK"
  echo "flush_note: $FLUSH_NOTE"
  if [[ "$TESTS_OK" == true ]]; then
    echo "binary_gate: PASS"
  else
    echo "binary_gate: FAIL"
  fi
} | tee "$OUT_DIR/verdict.txt"

echo ""
echo "=== 6. LLM judge (not run by this script) ==="
echo "Run separately, same regardless of the verdict above:"
echo "  claude -p \"Compare \$OUT_DIR/produced.diff against the real historical diff for"
echo "  $FEATURE (git show $(cat "$FEATURE_DIR/PINNED_SHA" 2>/dev/null || echo '<merge-sha>')). "
echo "  Explain whether the produced diff is behaviorally correct, and flag any shortcut"
echo "  (stubbed logic, weakened assertion, orphaned stale site).\""
