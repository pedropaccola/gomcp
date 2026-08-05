#!/usr/bin/env bash
# setup_worktrees.sh <feature>
#
# Creates the 4 worktrees a feature needs (gomcp x2 reruns, vanilla x2
# reruns), all off the same pinned pre-feature commit, sharing one object
# store so each arm's `git diff` against that commit is directly the
# produced patch. Requires the Kubernetes repo cloned (or clones it,
# blobless, on first use) under $BENCH_K8S_ROOT (default ~/k8s-bench).
#
# Each worktree then gets features/<feature>/reference-tests/ and
# reference-generated/ copied into place at their real upstream paths,
# committed as one fixed "BASELINE-FIXTURES" commit both arms start from
# — see benchmark/README.md's "Fixture integrity" note for why both are
# pre-applied and visible, not hidden.
set -euo pipefail

FEATURE="${1:?usage: setup_worktrees.sh <feature>}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FEATURE_DIR="$BENCH_DIR/features/$FEATURE"
K8S_ROOT="${BENCH_K8S_ROOT:-$HOME/k8s-bench}"
K8S_REPO="$K8S_ROOT/kubernetes"

if [[ ! -f "$FEATURE_DIR/PINNED_SHA" ]]; then
  echo "no PINNED_SHA file for feature '$FEATURE' (expected $FEATURE_DIR/PINNED_SHA)" >&2
  exit 1
fi
PINNED_SHA="$(cat "$FEATURE_DIR/PINNED_SHA")"

if [[ ! -d "$K8S_REPO" ]]; then
  echo "cloning kubernetes/kubernetes (blobless) into $K8S_REPO ..."
  mkdir -p "$K8S_ROOT"
  git clone --filter=blob:none https://github.com/kubernetes/kubernetes.git "$K8S_REPO"
fi

git -C "$K8S_REPO" fetch origin "$PINNED_SHA" --depth=1 2>/dev/null || git -C "$K8S_REPO" fetch origin
git -C "$K8S_REPO" cat-file -e "$PINNED_SHA^{commit}" || {
  echo "pinned SHA $PINNED_SHA not found after fetch — check PINNED_SHA is still reachable" >&2
  exit 1
}

for ARM in gomcp vanilla; do
  for RUN in 1 2; do
    WT_NAME="${FEATURE}-${ARM}-run${RUN}"
    WT_PATH="$K8S_ROOT/$WT_NAME"
    if [[ -d "$WT_PATH" ]]; then
      echo "skip $WT_NAME: worktree already exists at $WT_PATH"
      continue
    fi
    echo "creating worktree $WT_NAME off $PINNED_SHA ..."
    git -C "$K8S_REPO" worktree add --detach "$WT_PATH" "$PINNED_SHA"

    # Mirror reference-tests/ and reference-generated/ into place at their
    # real upstream paths (both directories mirror the destination path
    # structure exactly, so a straight recursive copy is correct).
    if [[ -d "$FEATURE_DIR/reference-tests" ]]; then
      cp -r "$FEATURE_DIR/reference-tests/." "$WT_PATH/"
    fi
    if [[ -d "$FEATURE_DIR/reference-generated" ]]; then
      cp -r "$FEATURE_DIR/reference-generated/." "$WT_PATH/"
    fi

    git -C "$WT_PATH" add -A
    git -C "$WT_PATH" -c user.name="benchmark" -c user.email="benchmark@localhost" \
      commit -m "BASELINE-FIXTURES (do not modify: tests + generated code)" --quiet
    echo "  baseline-fixtures commit: $(git -C "$WT_PATH" rev-parse HEAD)"
  done
done

echo "done. 4 worktrees ready under $K8S_ROOT for feature '$FEATURE'."
echo "sanity check before running any agent: (cd $K8S_ROOT/${FEATURE}-gomcp-run1 && go build ./...)"
