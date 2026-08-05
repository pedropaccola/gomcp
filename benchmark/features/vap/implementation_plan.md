# ValidatingAdmissionPolicy — CEL-based in-process admission control (KEP-3488, alpha)

## Summary

Every admission webhook today runs out-of-process: the API server serializes the request, sends it
over HTTP to an external service, and waits. `ValidatingAdmissionPolicy` (and its companion
`ValidatingAdmissionPolicyBinding`) lets a cluster operator express the same kind of admission
check as a small CEL (Common Expression Language) program evaluated **in-process**, with no
network hop, no external service to run, and no webhook `TypeConverter` round-trip. This feature
introduces two new namespaced-cluster-scoped API resources under a new `admissionregistration.k8s
.io/v1alpha1` API group version, and a new built-in admission plugin (`ValidatingAdmissionPolicy`)
that compiles and evaluates them.

The following types already exist at this worktree's starting commit
(`pkg/apis/admissionregistration/types.go` — read them there, do not re-derive their shape) and
are not yours to redesign, only to build the machinery around: `ValidatingAdmissionPolicy` /
`ValidatingAdmissionPolicyBinding` (+ `List` variants), `ValidatingAdmissionPolicySpec` (fields:
`ParamKind *ParamKind`, `MatchConstraints *MatchResources`, `Validations []Validation`,
`FailurePolicy *FailurePolicyType`), `Validation` (fields: `Expression string`, `Message string`,
`Reason *metav1.StatusReason`), `ParamKind` (`APIVersion`, `Kind`), `ValidatingAdmissionPolicyBindingSpec`
(`PolicyName string`, `ParamRef *ParamRef`, `MatchResources *MatchResources`), `ParamRef` (`Name`,
`Namespace`), and the shared `MatchResources`/`NamedRuleWithOperations` types webhooks already use.
The `CELValidatingAdmission` feature gate (`k8s.io/apiserver/pkg/features`) gates the whole plugin.

## Behavioral requirements

1. **CEL compilation.** Each `Validation.Expression` on a policy must compile to a runnable CEL
   program once, at policy-load time, not per admission request. The request's `object`/`oldObject`
   and the resolved param object (if `ParamKind` is set) must be exposed as CEL variables the
   expression can reference. A boolean-false result means the request is denied; the `Message`
   field (or a CEL-computed message, if the expression supports one) becomes the denial reason, and
   `Reason` (defaulting sensibly when unset) determines the HTTP status code returned to the
   client. A policy whose expression fails to compile at all must be handled per its
   `FailurePolicy` (`Fail`/`Ignore`) exactly the same way a webhook that's unreachable is today —
   this is deliberately the same failure-mode vocabulary admission webhooks already use, not a new
   one.
2. **Matching.** Both `ValidatingAdmissionPolicy.Spec.MatchConstraints` and
   `ValidatingAdmissionPolicyBinding.Spec.MatchResources` need to be evaluated against an incoming
   admission request as directly as possible, reusing the `MatchResources`/`NamedRuleWithOperations`
   matching semantics from `pkg/apis/admissionregistration/types.go` — a request only reaches a
   given policy+binding pair's CEL evaluation once both match. A binding may omit `MatchResources`
   entirely, meaning "match everything the policy itself matches" (no additional narrowing).
3. **Param resolution.** When a policy sets `ParamKind`, its binding's `ParamRef` names a specific
   object of that kind to resolve and pass into the CEL expression's evaluation context. Resolving
   an arbitrary, cluster-operator-specified `APIVersion`/`Kind` at runtime — not a compile-time-known
   Go type — needs the dynamic client and REST mapper, not a typed clientset call.
4. **Plugin lifecycle and dependency injection.** The plugin follows the same
   dependency-injection pattern every in-tree admission plugin already uses: it declares which
   `initializer.Wants*` interfaces it implements (informer factory, clientset, REST mapper, dynamic
   client, a drain/stop notification), receives them via the initializer during server startup, and
   only starts serving admission requests once it's synced and `ValidateInitialization` has
   succeeded. **A new `Wants*` capability doesn't yet exist in the initializer for one of these
   dependencies — check `staging/.../apiserver/pkg/admission/initializer/{initializer,interfaces}.go`
   for which capabilities are already wired for other plugins vs. which one this plugin needs that
   isn't there yet**, and add it following the existing pattern exactly (an interface + a setter
   method + the initializer calling it when present), not a bespoke mechanism.
5. **In-process evaluation on the request path.** The plugin's `Validate` method (implementing
   `admission.ValidationInterface`) must skip evaluating policies against requests that are
   themselves creating/updating a `ValidatingAdmissionPolicy` or `ValidatingAdmissionPolicyBinding`
   object (no self-admission), wait for its internal caches to sync before evaluating any real
   request (bounded by a short timeout, returning a forbidden/not-yet-ready response rather than
   hanging indefinitely), and otherwise walk every policy whose match constraints match the
   request, resolve each one's binding(s) and params, run CEL evaluation, and aggregate the
   resulting per-policy decisions into a single admission allow/deny outcome for the request.
6. **REST-reachability.** Both resources need the standard registry/strategy/storage plumbing
   (object validation on create/update via the `validation` package, standard `PrepareForCreate`/
   `PrepareForUpdate`/`Validate`/`ValidateUpdate` strategy methods, a `genericregistry.Store`-backed
   REST storage), an authorizer check on the resources consistent with how other admission-control
   resources authorize access, and registration into the API server's REST storage map and the
   default admission plugin list — all standard, all with an existing sibling resource's wiring to
   copy the shape of (a webhook configuration resource is the closest analog for the registry/REST
   layer; another already-registered admission plugin is the closest analog for the plugin-list
   wiring).
7. **Validation.** Both resources need object-level validation: at minimum, that
   `Validation.Expression` is non-empty (deep CEL-syntax validation happens at compile time, not
   API-admission time — validation here is structural, not semantic), that `MatchResources`/`Rule`
   fields are well-formed the same way webhook configs already validate theirs, and that a binding
   naming a `ParamRef` only makes sense if its policy actually declares a `ParamKind`.

## A synthetic requirement, stated plainly as such

The real upstream history for this feature doesn't happen to contain a clean, isolated
"rename this symbol, update every reference" moment — its churn was mostly a from-scratch design,
not a rename propagated through an existing one. To still exercise that specific kind of change
(a repo-wide rename of a single exported identifier, correctly propagated to every file that
references it, with nothing left stale), this plan asks for one explicitly: implement the
top-level admission-decision entry point (the interface the plugin's `Validate` method calls into,
the one your evaluator/controller type satisfies) under the name `PolicyEvaluator` first, get
everything compiling and passing against it under that name, and only once that's done, rename it
to `CELPolicyEvaluator` everywhere it's declared, implemented, and referenced. This step is not
drawn from KEP-3488 or from the real PR — it's added here for benchmark-measurement purposes only,
and graded the same as everything else (the final code must build and pass tests under the final
name; an intermediate name left behind anywhere is a real defect, not a stylistic nit).

## What's deliberately not detailed further

- The exact shape of the informer-based controller that watches policy/binding objects and keeps
  compiled CEL programs in sync with them as objects are created/updated/deleted (vs. recompiling
  on every request) — read how another in-tree controller in this codebase reconciles watched
  objects into cached derived state before inventing a new pattern.
- Exactly which CEL variables/types are exposed to the expression beyond `object`/`oldObject`/the
  resolved param (e.g. whether request metadata like the operation type is available) — the
  test files already in your workspace exercise this surface; match what they expect.

## Fixed boilerplate

The real upstream test files (including non-`_test.go` test-support code, e.g. an etcd
storage-path registry integration tests depend on) are already present in your workspace at their
real paths — see the file listing in your workspace for the full set, concentrated under
`pkg/apis/admissionregistration/`, `pkg/registry/admissionregistration/`,
`pkg/kubeapiserver/options/`, `pkg/printers/internalversion/`,
`staging/src/k8s.io/api/`, `staging/src/k8s.io/apiserver/pkg/admission/`, and
`test/integration/apiserver/cel/`. They are the acceptance spec for this task. Do not modify any of
them under any circumstances (including to make tests pass, skip them, or hand-patch a generated
file instead of fixing your own code). Your job is only the hand-written production code needed to
make them pass as-is. Any change to these files will be detected and the run discarded regardless
of test outcome.

Generated code (deepcopy, conversion, OpenAPI, the entire `client-go` typed clientset/informers/
listers/apply-configurations surface for the new types, and golden roundtrip test fixtures) is
likewise already present and already correct for the final shape of the types you're building
against. Do not regenerate or hand-edit it.
