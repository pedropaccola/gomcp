#!/usr/bin/env bash
# run_arm.sh <feature> <gomcp|vanilla> <run-index>
#
# Single invocation wrapper: launches one non-interactive Claude Code run
# against one worktree, arm-scoped tool config, writing its transcript
# and final result under results/runs/.
#
# Preconditions:
#   - setup_worktrees.sh already ran for this feature.
#   - the gomcp binary is built once, ahead of time (GOMCP_BINARY env var,
#     default $BENCH_DIR/../gomcp-binary) — never `go run` per invocation,
#     since the server's behavior is tied to the running binary, not source.
#   - `claude --version` recorded once before the first real run; do not
#     let the CLI auto-update mid-benchmark.
set -euo pipefail

FEATURE="${1:?usage: run_arm.sh <feature> <gomcp|vanilla> <run-index>}"
ARM="${2:?usage: run_arm.sh <feature> <gomcp|vanilla> <run-index>}"
RUN="${3:?usage: run_arm.sh <feature> <gomcp|vanilla> <run-index>}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FEATURE_DIR="$BENCH_DIR/features/$FEATURE"
K8S_ROOT="${BENCH_K8S_ROOT:-$HOME/k8s-bench}"
WT_PATH="$K8S_ROOT/${FEATURE}-${ARM}-run${RUN}"
GOMCP_BINARY="${GOMCP_BINARY:-$BENCH_DIR/../gomcp-binary}"
OUT_DIR="$BENCH_DIR/results/runs/${FEATURE}-${ARM}-${RUN}"

[[ "$ARM" == "gomcp" || "$ARM" == "vanilla" ]] || { echo "arm must be 'gomcp' or 'vanilla'" >&2; exit 1; }
[[ -d "$WT_PATH" ]] || { echo "worktree missing: $WT_PATH — run setup_worktrees.sh first" >&2; exit 1; }
[[ -f "$FEATURE_DIR/implementation_plan.md" ]] || { echo "missing $FEATURE_DIR/implementation_plan.md" >&2; exit 1; }

mkdir -p "$OUT_DIR"

if [[ "$ARM" == "gomcp" ]]; then
  [[ -x "$GOMCP_BINARY" ]] || { echo "gomcp binary not found/executable at $GOMCP_BINARY — build it once first: go build -o $GOMCP_BINARY ./cmd/gomcp" >&2; exit 1; }
  MCP_CONFIG="$OUT_DIR/mcp-config.json"
  sed -e "s#__GOMCP_BINARY__#$GOMCP_BINARY#" -e "s#__WORKTREE_PATH__#$WT_PATH#" \
    "$BENCH_DIR/configs/gomcp-mcp-config.json" > "$MCP_CONFIG"
  # gomcp's own tools, plus Bash scoped to the go toolchain only - gomcp
  # isn't meant to replace go build/test/vet/generate, only raw file
  # editing. No Read/Write/Edit/Grep/Glob: the point under test is
  # whether gomcp's own tools are sufficient on their own.
  ALLOWED_TOOLS="mcp__gomcp__list_packages,mcp__gomcp__list_files,mcp__gomcp__list_methods,mcp__gomcp__list_symbols,mcp__gomcp__describe_packages,mcp__gomcp__describe_files,mcp__gomcp__describe_symbols,mcp__gomcp__search_declarations_like,mcp__gomcp__search_source,mcp__gomcp__search_implementors,mcp__gomcp__search_references,mcp__gomcp__diagnostics_workspace,mcp__gomcp__diagnostics_packages,mcp__gomcp__diagnostics_files,mcp__gomcp__diagnostics_symbols,mcp__gomcp__create_packages,mcp__gomcp__create_files,mcp__gomcp__create_symbols,mcp__gomcp__edit_symbols,mcp__gomcp__edit_files,mcp__gomcp__delete_symbols,mcp__gomcp__delete_files,mcp__gomcp__delete_packages,mcp__gomcp__refactor_move_symbol,mcp__gomcp__refactor_move_file,mcp__gomcp__refactor_move_package,mcp__gomcp__disk_flush,mcp__gomcp__disk_reload,Bash(go build:*),Bash(go test:*),Bash(go vet:*),Bash(go generate:*)"
else
  MCP_CONFIG="$BENCH_DIR/configs/empty-mcp-config.json"
  ALLOWED_TOOLS="Read,Write,Edit,Grep,Glob,Bash(go build:*),Bash(go test:*),Bash(go vet:*),Bash(go generate:*),Bash(git diff:*)"
fi

PROMPT="$(cat "$FEATURE_DIR/implementation_plan.md")"

echo "running $FEATURE / $ARM / run $RUN against $WT_PATH ..."
claude -p "$PROMPT" \
  --mcp-config "$MCP_CONFIG" \
  --strict-mcp-config \
  --model claude-sonnet-5 \
  --permission-mode bypassPermissions \
  --allowedTools "$ALLOWED_TOOLS" \
  --output-format json \
  --add-dir "$WT_PATH" \
  > "$OUT_DIR/result.json" 2> "$OUT_DIR/stderr.log"

echo "done. Result: $OUT_DIR/result.json"
echo "Transcript is under ~/.claude/projects/<escaped-cwd>/<session-id>.jsonl - copy it into"
echo "  $OUT_DIR/transcript.jsonl before running extract_metrics.py, since later runs against"
echo "  the same worktree path would otherwise overwrite it."
