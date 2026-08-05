# backofflimit — grading

## Anchors

- PR: kubernetes/kubernetes#118009 ("Support BackoffLimitPerIndex in Jobs"), merged 2023-07-18
- Base commit (`PINNED_SHA`): `f3f5dd99ac7bdc61c61c3d587575090c3473ab5a`
- Merge commit: `a15c27661e68695be81d018a1fb81683881f4266`
- Test tier: 1 only (`test/integration/job/job_test.go` is pre-applied as part of the fixed
  acceptance spec too, since it's real upstream-authored test content, but it's not part of the
  required gate — it needs a running etcd this harness doesn't set up by default; run it manually
  if you have one available)
- No API type or generated-code changes in scope — `BackoffLimitPerIndex`/`MaxFailedIndexes`/
  `FailedIndexes`/`PodFailurePolicyActionFailIndex` already exist at `PINNED_SHA` (verified
  directly against `pkg/apis/batch/types.go` at that commit before scoping this feature — an
  earlier, separate PR added the API surface). `reference-generated/` is empty for this feature.

## Grading command

```
TEST_CMD=go test ./pkg/controller/job/...
```

(`grade.sh` reads the `TEST_CMD=` line above directly — keep it a single line, no shell
metacharacters beyond what `eval` in `grade.sh` can handle safely.)

## Pass criteria

1. Fixture integrity clean (`MANIFEST.sha256` matches for both `reference-tests/` and
   `reference-generated/` — the latter is empty for this feature, so any file appearing there at
   all is itself a violation).
2. `go test ./pkg/controller/job/...` exits 0.
3. `go vet` clean on every touched package (`grade.sh` computes this automatically from the
   frozen diff).

## Optional, non-gating checks

- `go test ./test/integration/job/...` (tier 2) — requires `ETCD_LOCATION` or an equivalent local
  etcd; not run by `grade.sh` automatically. Run manually and record separately if you have the
  setup for it; do not let its result affect `tier1_pass`/`binary_gate` in `results_template.md`.
- LLM judge pass (see `grade.sh`'s own final step) — always run, regardless of the binary gate's
  outcome.
