# Pod Scheduling Readiness / `schedulingGates` (KEP-3521, alpha)

## Summary

A Pod can now be created with `spec.schedulingGates` — an opaque, ordered list of named gates
(already present on the API type at this worktree's starting commit,
`staging/src/k8s.io/api/core/v1/types.go`, do not modify it). While that list is non-empty, the
scheduler must not attempt to schedule the Pod at all — not just fail to find a node for it, but
never even consider it a scheduling candidate — until every gate has been removed (by some
external actor clearing the list; clearing gates is out of scope for the scheduler itself). This
requires a **new kind of scheduling-framework extension point**, distinct from every existing one:
today's plugins all run once a Pod is already being actively considered for scheduling; this one
must run *before* a Pod is even placed in the active scheduling queue, so gated pods don't
uselessly cycle through scheduling attempts, get counted in scheduling-latency metrics, or awaken
other machinery expecting an actual scheduling decision.

## Behavioral requirements

1. **New plugin interface, `PreEnqueuePlugin`.** Add it alongside the other plugin-type interfaces
   in `pkg/scheduler/framework/interface.go`: a `Plugin` that exposes a `PreEnqueue(ctx
   context.Context, p *v1.Pod) *Status` method. A nil/success `*Status` means the Pod may proceed
   to the active queue; otherwise it must not, and the returned status's reason and unresolvable
   status becomes visible via the same mechanism other plugins already use to report a Pod as
   unschedulable. Wire it as a new named extension point on the scheduling framework, the same way
   every other plugin category (filter, score, permit, ...) already is — plugins are constructed
   and grouped into the framework's plugin lists once at setup, not looked up ad hoc per Pod.
2. **The concrete `SchedulingGates` plugin.** A new plugin (package `schedulinggates`) implementing
   `PreEnqueuePlugin`: it returns an unresolvable-unschedulable status naming every gate still
   present on `pod.Spec.SchedulingGates` when the list is non-empty, and success otherwise. Gate
   this behavior behind the existing `PodSchedulingReadiness` feature gate (`pkg/features`) — when
   disabled, the plugin must always succeed regardless of the gate list. The scheduler doesn't check
   feature gates ad hoc at call sites; it threads a `Features` struct (`pkg/scheduler/framework/
   plugins/feature`) through plugin construction — add this gate there following the pattern every
   other feature-gated plugin in that struct already uses, and thread it through
   `NewInTreeRegistry` (`pkg/scheduler/framework/plugins/registry.go`) the same way. Register the
   plugin's name in the scheduler's plugin-name registry (`pkg/scheduler/framework/plugins/names`)
   and its constructor in the plugin registry, matching the pattern every other built-in plugin
   already follows there.
3. **Default plugin configuration.** A new plugin doesn't join the scheduler's default set by
   simply existing — every `KubeSchedulerConfiguration` API version
   (`pkg/scheduler/apis/config/v1`, `v1beta2`, `v1beta3`, plus the internal, unversioned
   `pkg/scheduler/apis/config` type each of those converts to/from) needs a **new field** added to
   its `Plugins` struct for this plugin's extension point (see point 5 below for what that
   extension point is called) — check how the existing extension-point fields on that struct are
   named, tagged, and threaded through that version's own defaulting, merging, and validation code
   (`defaults.go`/`default_plugins.go`/`validation.go` or equivalent per version), then do the same
   for the new one. Whether the plugin itself joins the default-enabled set unconditionally or only
   when `PodSchedulingReadiness` is on is something the existing default-plugin-list construction
   code in each version should make clear once you're looking at it — don't assume without
   checking.
4. **Queue behavior for gated pods.** When a Pod is added to the scheduling queue, every registered
   `PreEnqueuePlugin` for that Pod's scheduling profile must run first. If any of them reports the
   Pod isn't ready, the Pod must land in the same holding area already used for pods that failed a
   real scheduling attempt (not the active queue), and must be distinguishable there as "gated"
   specifically (not merely "unschedulable" — the two are different states with different causes,
   and this distinction needs to be visible on the Pod's own queued-info tracking, not inferred
   after the fact). A Pod update event (the natural trigger for a gate being cleared) must cause the
   queue to re-attempt moving that Pod forward, exactly as an update event already does for
   ordinary unschedulable pods today.
5. **Plumbing the plugin set into the queue.** The queue needs to know, per scheduling profile,
   which `PreEnqueuePlugin`s apply — passed in at queue-construction time (an `Option`, matching
   the existing options pattern already used for the queue's other constructor parameters), not
   discovered at pod-add time.

## What's deliberately not detailed further

Two real details from the actual merged change aren't spelled out above — they're the kind of thing
a careful read of the existing metrics/backoff code (and the test files already in your workspace)
should surface, not prose:

- Whether "gated" and ordinary "unschedulable" pods should be counted together or separately in
  the scheduler's own observability output, and what that implies for how the holding area's
  existing accounting is constructed.
- Gated pods interact with the queue's *backoff* timer in a way that isn't obvious from the gating
  logic alone — think through, from first principles, whether a Pod that's currently gated should
  ever be treated as "waiting out a backoff timer" the same way a Pod that failed a real scheduling
  attempt is, and what would go wrong if it were.

## Fixed boilerplate

The test files at `pkg/scheduler/framework/plugins/schedulinggates/scheduling_gates_test.go`,
`pkg/scheduler/framework/runtime/framework_test.go`,
`pkg/scheduler/internal/queue/scheduling_queue_test.go`,
`pkg/scheduler/apis/config/v1/default_plugins_test.go`,
`pkg/scheduler/apis/config/v1beta2/default_plugins_test.go`,
`pkg/scheduler/apis/config/v1beta3/default_plugins_test.go`,
`test/integration/scheduler/queue_test.go`, and
`test/integration/scheduler/plugins/plugins_test.go` are already present in your workspace. They
are the acceptance spec for this task. Do not modify any of them under any circumstances (including
to make tests pass, skip them, or hand-patch a generated file instead of fixing your own code).
Your job is only the hand-written production code needed to make them pass as-is. Any change to
these files will be detected and the run discarded regardless of test outcome.
