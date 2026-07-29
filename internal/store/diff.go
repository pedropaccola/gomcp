package store

// diffDiagnostics compares a workspace's diagnostics inventory across a
// transaction: delta is what's newly present, resolved is what's newly
// absent, and unrelated counts diagnostics unchanged by either edge — the
// pre-existing breakage a transaction's echo deliberately stays silent
// about, visible only through the uncapped diagnostics tool.
func diffDiagnostics(before, after []Diagnostic) (delta, resolved []Diagnostic, unrelated int) {
	beforeSet := make(map[string]Diagnostic, len(before))
	for _, diag := range before {
		beforeSet[diag.String()] = diag
	}
	afterSet := make(map[string]bool, len(after))
	for _, diag := range after {
		key := diag.String()
		afterSet[key] = true
		if _, existed := beforeSet[key]; existed {
			unrelated++
		} else {
			delta = append(delta, diag)
		}
	}
	for _, key := range sortedKeys(beforeSet) {
		if !afterSet[key] {
			resolved = append(resolved, beforeSet[key])
		}
	}
	return delta, resolved, unrelated
}
