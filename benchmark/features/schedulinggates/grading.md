# schedulinggates — grading

## Anchors

- Core PR: kubernetes/kubernetes#113275 ("Pod scheduling readiness alpha: introduce a new
  PreEnqueue extension point"), merged 2022-11-08, merge commit
  `95bd687a284f63535cbf48b0696d8ae57c9929ef`.
- Companion PR: kubernetes/kubernetes#113442 ("scheduler: gated pods must not be treated as
  backing off"), merged 2022-11-08, merge commit `d619f60e0fd1fb72cb199db247a328e2f1c6f0a5`. Base
  ref for the companion PR (`81bd2496bce080e5788cf18a807cdd42bd4ee8c1`) is a few commits after
  `PINNED_SHA` on the real upstream history, but the file-level change it carries
  (`isPodBackingoff` must return `false` immediately when `podInfo.Gated`) applies cleanly on top
  of the core PR regardless; both are folded into one fixture set here since they land the same
  day and the companion is a direct correctness fix to the core PR's own new code, not a separate
  feature.
- Base commit (`PINNED_SHA`): `7369bd27e05eea1ac7dd3ff55f95e50df8c9a4ff` (shared with `vap`, which
  also targets this same day's tree).
- No changes needed to `v1.PodSpec.SchedulingGates` — verified directly against
  `staging/src/k8s.io/api/core/v1/types.go` at `PINNED_SHA`: the field already exists (added by an
  earlier, separate PR), same pattern as `backofflimit`.
- Unlike `backofflimit`, this feature **does** add a new hand-written field to the
  `KubeSchedulerConfiguration` API — `Plugins.PreEnqueue PluginSet` — across the internal type
  (`pkg/scheduler/apis/config/types.go`) and all three versioned types
  (`pkg/scheduler/apis/config/{v1,v1beta2,v1beta3}` — note this is `k8s.io/kube-scheduler/config`,
  a distinct, scheduler-only versioned config API, not the `core/v1` Pod API). That field addition
  is in the agent's hand-written scope, per `implementation_plan.md` point 3. Only the *generated*
  conversion/deepcopy/openapi code that field addition mechanically produces is pre-applied — see
  below.
- Test tier: 1 only (`test/integration/scheduler/...` needs a running API server this harness
  doesn't set up by default; pre-applied as part of the fixed acceptance spec but not part of the
  required gate).

## Generated files pre-applied to `reference-generated/`

- `pkg/generated/openapi/zz_generated.openapi.go`
- `pkg/scheduler/apis/config/zz_generated.deepcopy.go`
- `pkg/scheduler/apis/config/v1/zz_generated.conversion.go`
- `pkg/scheduler/apis/config/v1beta2/zz_generated.conversion.go`
- `pkg/scheduler/apis/config/v1beta3/zz_generated.conversion.go`
- `staging/src/k8s.io/kube-scheduler/config/v1/zz_generated.deepcopy.go`
- `staging/src/k8s.io/kube-scheduler/config/v1beta2/zz_generated.deepcopy.go`
- `staging/src/k8s.io/kube-scheduler/config/v1beta3/zz_generated.deepcopy.go`
- `test/instrumentation/testdata/stable-metrics-list.yaml` — mechanically derived from the metrics
  the plugin registers, not hand-authored prose; treated as generated for the same reason the
  deepcopy/conversion files are.

## Test-support files pre-applied to `reference-tests/` alongside the real `_test.go` files

These aren't test files themselves but are test-only fixture/helper code the real `_test.go` files
depend on to compile, and inventing their exact shape isn't part of what this feature's KEP
describes:

- `pkg/scheduler/testing/wrappers.go` (gains a builder method for constructing a Pod with
  scheduling gates in test fixtures — shared across many unrelated scheduler tests, not something
  either arm should be reverse-engineering the signature of)
- `test/e2e/framework/pod/wait.go`, `test/e2e/scheduling/predicates.go`,
  `test/integration/util/util.go` — e2e/integration test-support helpers, same reasoning.

## Grading command

```
TEST_CMD=go test ./pkg/scheduler/... ./cmd/kube-scheduler/...
```

## Pass criteria

1. Fixture integrity clean (`MANIFEST.sha256` matches for both `reference-tests/` and
   `reference-generated/`).
2. `go test ./pkg/scheduler/... ./cmd/kube-scheduler/...` exits 0.
3. `go vet` clean on every touched package (`grade.sh` computes this automatically from the frozen
   diff).

## Optional, non-gating checks

- `go test ./test/integration/scheduler/...` (tier 2) — requires a running API server; not run by
  `grade.sh` automatically. Run manually and record separately; do not let its result affect
  `tier1_pass`/`binary_gate` in `results_template.md`.
- LLM judge pass (see `grade.sh`'s own final step) — always run, regardless of the binary gate's
  outcome.
