#!/usr/bin/env python3
"""extract_metrics.py <transcript.jsonl>

Parses a Claude Code session JSONL transcript into the token/tool-call
breakdown the benchmark actually needs: four separate token numbers per
the plan's own Measurement section (not one total, since cache-read
dominates cumulative totals for both arms and would mask claim #3's
input-token signal), per-tool call counts, the navigation:mutation
ratio, and compaction events.

Usage: python3 extract_metrics.py path/to/transcript.jsonl > metrics.json
"""
import json
import re
import sys

GOMCP_NAV = {
    "mcp__gomcp__list_packages", "mcp__gomcp__list_files", "mcp__gomcp__list_methods",
    "mcp__gomcp__list_symbols", "mcp__gomcp__describe_packages", "mcp__gomcp__describe_files",
    "mcp__gomcp__describe_symbols", "mcp__gomcp__search_declarations_like",
    "mcp__gomcp__search_source", "mcp__gomcp__search_implementors", "mcp__gomcp__search_references",
    "mcp__gomcp__diagnostics_workspace", "mcp__gomcp__diagnostics_packages",
    "mcp__gomcp__diagnostics_files", "mcp__gomcp__diagnostics_symbols",
}
GOMCP_MUTATE = {
    "mcp__gomcp__create_packages", "mcp__gomcp__create_files", "mcp__gomcp__create_symbols",
    "mcp__gomcp__edit_symbols", "mcp__gomcp__edit_files", "mcp__gomcp__delete_symbols",
    "mcp__gomcp__delete_files", "mcp__gomcp__delete_packages", "mcp__gomcp__refactor_move_symbol",
    "mcp__gomcp__refactor_move_file", "mcp__gomcp__refactor_move_package",
}
VANILLA_NAV = {"Read", "Grep", "Glob"}
VANILLA_MUTATE = {"Edit", "Write"}


def main():
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        sys.exit(1)

    totals = {"input_tokens": 0, "cache_creation_input_tokens": 0,
              "cache_read_input_tokens": 0, "output_tokens": 0}
    tool_counts = {}
    compaction_events = 0
    flush_seen = False
    last_index_of_flush = -1
    line_index = 0

    with open(sys.argv[1], "r") as fh:
        for line in fh:
            line_index += 1
            line = line.strip()
            if not line:
                continue
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue

            if entry.get("isCompactSummary"):
                compaction_events += 1

            message = entry.get("message", {})
            if message.get("role") != "assistant":
                continue

            usage = message.get("usage", {})
            for key in totals:
                totals[key] += usage.get(key, 0) or 0

            for block in message.get("content", []) or []:
                if block.get("type") != "tool_use":
                    continue
                name = block.get("name", "")
                tool_counts[name] = tool_counts.get(name, 0) + 1
                if name == "mcp__gomcp__disk_flush":
                    flush_seen = True
                    last_index_of_flush = line_index

    nav = sum(tool_counts.get(t, 0) for t in GOMCP_NAV | VANILLA_NAV)
    mutate = sum(tool_counts.get(t, 0) for t in GOMCP_MUTATE | VANILLA_MUTATE)
    nav_mutation_ratio = (nav / mutate) if mutate else None

    is_gomcp_arm = any(re.match(r"^mcp__gomcp__", t) for t in tool_counts)
    non_gomcp_edit_calls = 0
    if is_gomcp_arm:
        non_gomcp_edit_calls = sum(
            c for t, c in tool_counts.items()
            if t in {"Edit", "Write"} or (t == "Bash" and False)  # Bash calls need manual review for edit intent
        )

    result = {
        "tokens": totals,
        "tool_call_counts": tool_counts,
        "distinct_tools_used": len(tool_counts),
        "navigation_calls": nav,
        "mutation_calls": mutate,
        "nav_mutation_ratio": nav_mutation_ratio,
        "compaction_events": compaction_events,
        "disk_flush_called": flush_seen if is_gomcp_arm else None,
        "flush_forgotten": (is_gomcp_arm and not flush_seen),
        "non_gomcp_edit_calls_if_gomcp_arm": non_gomcp_edit_calls if is_gomcp_arm else None,
        "note": "non_gomcp_edit_calls_if_gomcp_arm only catches Edit/Write; a raw-file-editing "
                "shell command via Bash needs manual transcript review to catch (claim #2/#5 check).",
    }
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
