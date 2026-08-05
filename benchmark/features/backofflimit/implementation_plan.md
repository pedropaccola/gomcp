# Job `BackoffLimitPerIndex` (KEP-3850, alpha)

## Summary

Indexed Jobs (`spec.completionMode: Indexed`) currently apply one `spec.backoffLimit` across the
whole Job: once that many pods have failed in total, the Job is marked failed. This feature lets
an Indexed Job instead track backoff **per index**: `spec.backoffLimitPerIndex` caps how many times
*one index* may fail before that index is considered permanently failed, independent of every
other index. Indexes that exceed their own per-index limit stop retrying; indexes that haven't
don't. The Job as a whole only fails once too many indexes have failed outright.

The following fields already exist on the `Job` API type (`pkg/apis/batch/types.go`) at this
worktree's starting commit — read them there rather than re-deriving their shape, and do not
re-add or modify them, only *use* them:

- `spec.backoffLimitPerIndex *int32` — the per-index failure cap. `nil` means the feature isn't
  used for this Job (existing whole-Job `backoffLimit` behavior must be entirely unaffected).
- `spec.maxFailedIndexes *int32` — once the number of failed indexes exceeds this, the whole Job
  is marked failed, regardless of how many indexes might still succeed.
- `status.failedIndexes *string` — mirrors the existing `status.completedIndexes string` field:
  same compressed interval format (e.g. `"1,3-5"`), same purpose, now tracking failed rather than
  succeeded indexes.
- `batch.PodFailurePolicyActionFailIndex` — a new action value alongside the existing `Ignore`/
  `Count`/`FailJob` pod-failure-policy actions. A pod failure matching a rule with this action
  should count as a failure of *that pod's index specifically*, not (only) toward the whole-Job
  backoff count.
- The `JobBackoffLimitPerIndex` feature gate (`pkg/features`) gates all of the above — every new
  code path must check it's enabled before taking the per-index path; when disabled, behavior must
  be identical to today.

## Behavioral requirements

1. **Per-index backoff delay.** Today, the delay before creating a replacement pod after a failure
   is computed from the *whole Job's* consecutive-failure count and last-failure time. When
   `backoffLimitPerIndex` is set, that same exponential-backoff delay calculation must instead be
   computed **per index** — from that specific index's own most recent failed pod, tracked via the
   existing `job-index-failure-count` pod annotation. The underlying backoff-duration math
   (exponential growth capped at a maximum) doesn't change; only what failure count and what
   last-failure timestamp feed into it does.
2. **Per-index failure tracking and gating.** During each sync of an Indexed Job with
   `backoffLimitPerIndex` set, compute the current set of failed indexes (existing failed indexes
   from `status.failedIndexes`, plus any newly observed since the last sync), in the same
   compressed-interval format `status.completedIndexes` already uses — the existing
   succeeded-index interval-tracking logic is directly reusable here, not a new parser. A pod
   counts toward its index's failure only once (indexes/pods already counted must not be
   double-counted across syncs). Whether a given failed pod's failure should count toward its
   index at all depends on pod-failure-policy resolution (see point 4) and on the index not
   already having exceeded its own `backoffLimitPerIndex`.
3. **Job-level completion conditions, new for this mode.** When `backoffLimitPerIndex` is set and
   the Job doesn't already have a finishing condition from existing logic:
   - If the number of failed indexes exceeds `maxFailedIndexes`: the Job is Failed.
   - If the number of failed indexes plus already-succeeded indexes together reach
     `spec.completions` (meaning every index has now either succeeded or permanently failed, with
     none left that could still succeed): the Job is Failed.
   Both must produce a distinct, identifiable failure reason/message on the Job's Failed condition
   (existing `newCondition`-style helper already used elsewhere in this file is the right
   mechanism, not a new one).
4. **Pod failure policy gains the new action.** `matchPodFailurePolicy` already resolves each
   failed pod against `spec.podFailurePolicy`'s rules (matching by exit code or by pod condition)
   and returns whether the failure counts and which action matched. It must now also recognize
   `PodFailurePolicyActionFailIndex` as a legal action value for both existing rule-matching paths
   (exit-code rules and pod-condition rules) — a match against this action counts the failure
   (same as `Count` does) and reports the `FailIndex` action back to the caller, but *only* when
   the `JobBackoffLimitPerIndex` feature gate is enabled; treat it as unrecognized/no-op otherwise.
5. **Finalizer-removal timing.** This is the part most likely to be gotten wrong silently, so it's
   stated explicitly rather than left as one of the flagged gaps below: today, a pod's tracking
   finalizer is removed as soon as it's terminal (or the Job is finishing, or being deleted). Once
   per-index backoff is in play, removing a failed pod's finalizer immediately is *not* always
   correct — a replacement pod for that index shouldn't be free to start before that index's own
   backoff delay (point 1) has actually elapsed. Work out which failed pods need their finalizer
   removal *delayed* relative to today's rule, and re-drive a resync once the delay for any such
   pod has elapsed (so the controller re-evaluates rather than waiting indefinitely for an
   unrelated trigger).

## What's deliberately not detailed further

Two things are true about the real implementation that this plan won't spell out, because they're
discoverable from the existing code and from the test files already in your workspace, not from
this prose:

- Exactly how the existing succeeded-index interval-tracking code is structured, and how directly
  it can be reused (vs. adapted) for failed-index tracking.
- The exact data structure connecting "which pods are being held back from finalizer removal" to
  "when should the controller re-check them" — read how the controller already schedules its own
  resyncs for other delayed-action cases in this same file before inventing a new mechanism.

## Fixed boilerplate

The test files at `pkg/controller/job/backoff_utils_test.go`,
`pkg/controller/job/indexed_job_utils_test.go`, `pkg/controller/job/job_controller_test.go`,
`pkg/controller/job/pod_failure_policy_test.go`, and `test/integration/job/job_test.go` are already
present in your workspace. They are the acceptance spec for this task. Do not modify any of them
under any circumstances (including to make tests pass, skip them, or hand-patch a generated file
instead of fixing your own code). Your job is only the hand-written production code needed to make
them pass as-is. Any change to these files will be detected and the run discarded regardless of
test outcome.
