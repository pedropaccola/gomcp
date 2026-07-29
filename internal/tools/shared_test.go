package tools

import (
	"fmt"
	"testing"

	"github.com/pedropaccola/gomcp/internal/store"
)

func TestDiagBlockTruncation(t *testing.T) {
	diags := make([]store.Diagnostic, 5)
	for i := range diags {
		diags[i] = store.Diagnostic{Msg: fmt.Sprintf("problem %d", i)}
	}

	block := newDiagnosticsTruncated(diags, 3)
	if len(block.Diagnostics) != 3 {
		t.Fatalf("len(Diagnostics) = %d, want 3 shown", len(block.Diagnostics))
	}
	if block.Truncated == nil || *block.Truncated != 2 {
		t.Errorf("Truncated = %v, want 2", block.Truncated)
	}

	if block := newDiagnosticsTruncated(diags, 10); len(block.Diagnostics) != 5 || block.Truncated != nil {
		t.Errorf("below the limit: %+v, want 5 shown, no truncation", block)
	}

	if block := newDiagnosticsTruncated(diags, 0); len(block.Diagnostics) != 0 || block.Truncated == nil || *block.Truncated != 5 {
		t.Errorf("zero limit must still count everything as truncated: %+v", block)
	}

	if cfg := newToolConfig(-1); cfg.diagLimit != 20 {
		t.Errorf("newToolConfig must ignore negative n in favor of the default, got %d", cfg.diagLimit)
	}

	if block := newDiagnosticsTruncated(nil, 20); block.Diagnostics != nil || block.Truncated != nil {
		t.Errorf("empty input must stay a zero-value DiagnosticsTruncated, got %+v", block)
	}
}
