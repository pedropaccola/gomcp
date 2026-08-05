# vap — scope curation notes

`kubernetes/kubernetes#113314` ("[KEP-3488]Implement CEL for Admission Control") is 182 files,
+23814/-2695 lines — the raw PR is not usable as the task scope; the plan itself anticipated this
("Scope VAP down to the CEL compilation + admission decision path only, excluding
webhook-conversion and beta-cycle PRs") but that curation was never actually done before this
recreation. Done here, 2026-08-05, against the real per-file diff stats (`gh api --paginate
repos/kubernetes/kubernetes/pulls/113314/files`), not guessed from filenames.

## What's excluded, and why

- **All generated code** (`zz_generated.*.go`, `generated.pb.go`/`.proto`,
  `types_swagger_doc_generated.go`, `api/openapi-spec/**`, `pkg/generated/openapi/**`,
  `client-go/applyconfigurations|informers|listers|kubernetes/**`) — by far the bulk of the raw
  PR's line count (the single largest file alone, the v1alpha1 OpenAPI spec, is 3769 lines).
  Never hand-authored by a contributor in the first place; goes in `reference-generated/`.
- **Unrelated dependency bumps** (`staging/src/k8s.io/{cloud-provider,controller-manager,
  kube-aggregator,pod-security-admission,sample-apiserver}/go.{mod,sum}`, `vendor/modules.txt`,
  `hack/.import-aliases`, `hack/lib/init.sh`) — routine go.mod churn from the same merge window,
  not part of the feature.
- **Other admission plugins' tests** (`plugin/pkg/admission/{gc,limitranger,namespace/
  autoprovision,namespace/exists,podnodeselector,podtolerationrestriction,resourcequota}/
  *_test.go`) — updated only because a new admission plugin changed the default plugin count/order
  those tests assert on; not part of VAP's own logic.
- **`fake.go` in the cel plugin package** (0 additions / 258 deletions) — wholly deleted, replaced
  by the real `controller.go`. Nothing to author here; noted so its absence isn't mistaken for an
  oversight.
- **webhook/beta-cycle touches** (`admission/plugin/webhook/generic/webhook.go`: 1+1 line,
  `webhook/predicates/rules/rules.go`: 4+4 lines) — genuinely trivial (a shared signature tweak),
  confirmed by line count, not just excluded by the plan's own naming rule.

## What's in scope (hand-written production code the agent must write)

**43 files, 3494 changed lines** — corrected 2026-08-05 from an initial 29-file/~3092-line
estimate that was built by aggregating stats per *directory group*, which silently folded in
several small single-purpose registration/wiring files under the same directory as an
already-counted file (e.g. `pkg/apis/admissionregistration/register.go`,
`v1alpha1/{defaults,doc,register}.go`, `v1/conversion.go`,
`apiserver/pkg/admission/initializer/interfaces.go`). A full per-file categorization pass (every
one of the raw PR's 182 files individually classified as generated/test/excluded/hand-written, not
grouped by directory) caught the gap; the corrected list below is exhaustive, not another partial
pass. Still a fixed, real feature-shape decision, not scope creep: no new category was added, the
same categories just got fully enumerated instead of estimated. 85% reduction from the raw PR's
23814 lines (previously reported as 87% against the undercounted total).

| Group | Files | Lines | Why in scope |
| :--- | :--- | :--- | :--- |
| CEL admission core (`staging/.../admission/plugin/cel/`, non-test: `admission.go`, `compiler.go`, `controller.go`, `controller_reconcile.go`, `initializer.go`, `interface.go`, `internal/generic/controller.go`, `matching/matching.go`, `policy_decision.go`, `validator.go`) | 10 | 1369 | This *is* the feature — CEL compilation and the admission decision path, exactly the plan's own stated scope target. |
| Admission initializer plumbing (`apiserver/pkg/admission/initializer/{initializer,interfaces}.go`) | 2 | 21 | New dependency (policy/binding informers) the CEL plugin receives via the initializer pattern every in-tree plugin already uses — without this, `initializer.go`'s constructor has nothing to be handed. |
| API types & registration (`pkg/apis/admissionregistration/{types,register}.go`, `install/install.go`, `fuzzer/fuzzer.go`, `v1/conversion.go`, `v1alpha1/{types,defaults,doc,register}.go`, `staging/.../api/admissionregistration/v1/types.go`, `v1alpha1/{types,doc,register}.go`) | 12 | 963 | The new resources' internal+external type definitions and the group/version registration boilerplate every new API type needs (scheme registration, install, roundtrip fuzzer, defaulting) — none of it optional for the type to exist or for `staging/.../api/roundtrip_test.go` to pass. |
| Validation (`pkg/apis/admissionregistration/validation/validation.go`) | 1 | 243 | Object-level validation for the two new resources. |
| Registry/REST (`validatingadmissionpolicy{,binding}/{authz,strategy,storage/storage,doc}.go`, `resolver/resolver.go`) | 9 | 738 | Makes the resources reachable via the real API — required for the tier-2 integration tests to run at all. (Package docs folded in here, not a separate row — same reasoning as the rest of this group.) |
| Wiring (`rest/storage_apiserver.go`, `kubeapiserver/{options/plugins,admission/initializer,default_storage_factory_builder}.go`, `apiserver/pkg/server/{options/admission,plugins}.go`, `pkg/controlplane/instance.go`, `apiextensions-apiserver/.../schema/cel/compilation.go`) | 8 | 89 | Small, mechanical plugin/storage registration — each 1–12 lines, confirmed by stat, not assumed. |

**Deliberately not named in the implementation plan** (the plan's own "2-3 flagged gaps" device):
`pkg/printers/internalversion/printers.go` (71 lines, kubectl status-column wiring for the new
resources) is left in scope but unmentioned in `implementation_plan.md`'s prose — a real,
discoverable gap the plan's own methodology calls for, not an oversight.

## Fixtures

`reference-tests/` (20 files, real upstream test content — includes non-`_test.go` test-support
files like `test/integration/etcd/data.go`, an etcd storage-path registry the integration tests
need) and `reference-generated/` (60 files: deepcopy/conversion/openapi/protobuf-marshal code, the
entire `client-go` generated surface for the new type — typed clientset, informers, listers,
apply-configurations — and the golden roundtrip fixtures under `testdata/HEAD/`) are both fetched
at the merge commit `595ea324113580ae61f4a15ab3e5b22303a195cf` and checksummed into
`MANIFEST.sha256`. `.proto` schema-source files and the entire `api/openapi-spec/**` static spec
tree are excluded from `reference-generated/` even though they're generated too: neither is
consumed by `go build`/`go test`/`go vet` for any package in this feature's scope, and the
`v3/*.json` files in particular are 24 mostly-unrelated per-group index stubs bumped only because a
new group changes a shared count.

## Anchors

- PR: kubernetes/kubernetes#113314, merged 2022-11-08T01:08:34Z
- Base commit: `7369bd27e05eea1ac7dd3ff55f95e50df8c9a4ff` (see `PINNED_SHA`)
- Merge commit: `595ea324113580ae61f4a15ab3e5b22303a195cf`
- Re-verified fresh against GitHub 2026-08-05, not trusted from any earlier draft.
