# Benchmark Results

Fill the 12 run-rows first, from `grade.sh`'s `verdict.txt` and `extract_metrics.py`'s JSON output.
Write the qualitative failure-mode note from the transcript/diff/LLM-judge output alone, *before*
looking back at that run's token table, so the prose isn't unconsciously shaped by a number
already seen. A `fixture_integrity: VIOLATED` run is never averaged into its feature's aggregate
row — route it to manual review instead and note it explicitly in that row.

## Run results

| Feature | Arm | Run | Input tok | Cache-creation tok | Cache-read tok | Output tok | Tool calls | Nav:Mutation | Distinct tools | Fixture integrity | Tier-1 pass | Tier-2 pass (VAP only) | Vet clean | Self-declared complete | Flush forgotten (gomcp only) | Compaction events | Qualitative note |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| backofflimit | gomcp | 1 | | | | | | | | | | n/a | | | | | |
| backofflimit | gomcp | 2 | | | | | | | | | | n/a | | | | | |
| backofflimit | vanilla | 1 | | | | | | | | | | n/a | | | | | |
| backofflimit | vanilla | 2 | | | | | | | | | | n/a | | | | | |
| schedulinggates | gomcp | 1 | | | | | | | | | | n/a | | | | | |
| schedulinggates | gomcp | 2 | | | | | | | | | | n/a | | | | | |
| schedulinggates | vanilla | 1 | | | | | | | | | | n/a | | | | | |
| schedulinggates | vanilla | 2 | | | | | | | | | | n/a | | | | | |
| vap | gomcp | 1 | | | | | | | | | | | | | | | |
| vap | gomcp | 2 | | | | | | | | | | | | | | | |
| vap | vanilla | 1 | | | | | | | | | | | | | | | |
| vap | vanilla | 2 | | | | | | | | | | | | | | | |

## Per-feature aggregates (mean ± range across the 2 reruns, per arm)

| Feature | Arm | Input tok | Cache-creation tok | Cache-read tok | Output tok | Tool calls | Nav:Mutation | Distinct tools |
|---|---|---|---|---|---|---|---|---|
| backofflimit | gomcp | | | | | | | |
| backofflimit | vanilla | | | | | | | |
| schedulinggates | gomcp | | | | | | | |
| schedulinggates | vanilla | | | | | | | |
| vap | gomcp | | | | | | | |
| vap | vanilla | | | | | | | |

## Per-feature narrative

One paragraph per feature: which of the 10 claims (`claims.md`) it actually speaks to, whether the
token/tool-call delta matched the predicted direction, and any staleness-leak or shortcut-pass case
the LLM judge surfaced.

### backofflimit

*(not yet run)*

### schedulinggates

*(not yet run)*

### vap

*(not yet run — also note here how the PR #113314 scope-down described in
`features/vap/README.md` affects how directly this feature's results compare to the other two)*
