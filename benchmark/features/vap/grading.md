# vap — grading

## Anchors

- PR: kubernetes/kubernetes#113314 ("[KEP-3488]Implement CEL for Admission Control"), merged
  2022-11-08T01:08:34Z, merge commit `595ea324113580ae61f4a15ab3e5b22303a195cf`.
- Base commit (`PINNED_SHA`): `7369bd27e05eea1ac7dd3ff55f95e50df8c9a4ff` (shared with
  `schedulinggates`, which also targets this same day's tree).
- Full scope-curation rationale (what's excluded and why, the corrected 43-file/3494-line
  hand-written scope, the 20 test-support files, the 60 generated files) is in `README.md` in this
  directory — read it before touching this feature, it documents real corrections made after an
  initial undercount.
- Test tier: 1 (`go test` on the hand-written packages) + 2 (`test/integration/apiserver/cel/...`,
  real CEL compilation and admission decisions against a live etcd+apiserver — this is where most
  of this feature's actual behavior gets exercised, unlike `backofflimit`/`schedulinggates` where
  tier 2 was optional). Tier 2 is gating here, not optional, given how much of this feature's
  correctness (matching, param resolution, actual CEL evaluation results) tier-1 unit tests alone
  can't reach.

## Grading commands

```
TEST_CMD=go test ./pkg/apis/admissionregistration/... ./pkg/registry/admissionregistration/... ./staging/src/k8s.io/apiserver/pkg/admission/plugin/cel/... ./staging/src/k8s.io/apiserver/pkg/admission/initializer/... ./pkg/kubeapiserver/... ./pkg/printers/internalversion/... ./staging/src/k8s.io/api/...
```

(`grade.sh` reads the `TEST_CMD=` line directly — kept as one line despite its length.)

Tier 2 (gating for this feature, run separately since it needs a live etcd+apiserver and isn't
wired into `grade.sh`'s automatic tier-1 step):

```
go test ./test/integration/apiserver/cel/...
```

## Pass criteria

1. Fixture integrity clean (`MANIFEST.sha256` matches for both `reference-tests/` and
   `reference-generated/` — 80 files total, 20 test + 60 generated).
2. Tier-1 `TEST_CMD` above exits 0.
3. Tier-2 `go test ./test/integration/apiserver/cel/...` exits 0 (run manually with a local etcd,
   or via the same integration-test harness the rest of this repo's `test/integration/` suite
   uses — record its result in `results_template.md` alongside tier-1, not as a separate optional
   note, unlike the other two features).
4. `go vet` clean on every touched package (`grade.sh` computes this automatically from the frozen
   diff).

## Notes for the grader

- This feature's binary gate genuinely depends on tier 2 passing, unlike `backofflimit`/
  `schedulinggates`. If a local etcd isn't available when grading a given run, mark
  `tier2_pass: not run (no etcd)` explicitly in that run's row rather than silently treating tier-1
  alone as the binary gate — a tier-1-only pass on this feature is meaningfully weaker evidence
  than on the other two, and the results table should say so plainly, not imply parity.
- LLM judge pass (see `grade.sh`'s own final step) — always run, regardless of the binary gate's
  outcome. This feature is the designated carrier of claim #8 (safe-by-construction rename
  propagation) per `README.md`'s anchors table — if scoping or curation made a real rename/move
  opportunity too thin to show up naturally in either arm's transcript, note that explicitly in the
  per-feature narrative rather than silently letting claim #8 go untested.
