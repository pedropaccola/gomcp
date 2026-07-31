package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/pedropaccola/gomcp/internal/store"
)

func TestSemanticFinders(t *testing.T) {
	st := sandboxStore(t)

	_, impl, err := searchImplementors(st)(context.Background(), nil, SearchImplementorsInput{
		PkgPath: "shapes", SymbolKey: "Shape",
	})
	if err != nil {
		t.Fatalf("search_implementors: %v", err)
	}
	if !slices.ContainsFunc(impl.Matches, func(m MatchEntry) bool { return m.SymbolKey == "Embedded" }) {
		t.Errorf("search_implementors(Shape) missing promoted-method implementor Embedded: %v", impl.Matches)
	}

	_, refs, err := searchReferences(st)(context.Background(), nil, SearchReferencesInput{
		PkgPath: "shapes", SymbolKey: "Circle",
	})
	if err != nil {
		t.Fatalf("search_references: %v", err)
	}
	if !slices.ContainsFunc(refs.Matches, func(m MatchEntry) bool {
		return m.PkgPath == "example.com/sandbox/use" && m.SymbolKey == "NewCircle"
	}) {
		t.Errorf("search_references(Circle) missing use:NewCircle: %v", refs.Matches)
	}

	if _, _, err := searchImplementors(st)(context.Background(), nil, SearchImplementorsInput{
		PkgPath: "shapes", SymbolKey: "Circle",
	}); err == nil || !strings.Contains(err.Error(), "interface") {
		t.Errorf("search_implementors on a struct must error mentioning interface, got %v", err)
	}
}

func TestFindersAndDiagnostics(t *testing.T) {
	st := sandboxStore(t)

	_, like, err := searchDeclarationsLike(st)(context.Background(), nil, SearchLikeInput{Name: "area"})
	if err != nil {
		t.Fatalf("search_declarations_like: %v", err)
	}
	if !slices.ContainsFunc(like.Matches, func(m MatchEntry) bool {
		return m.SymbolKey == "Circle.Area" && m.PkgPath == "example.com/sandbox/shapes" && m.Kind == "method"
	}) {
		t.Errorf("search_declarations_like(area) missing Circle.Area: %v", like.Matches)
	}

	_, src, err := searchSource(st)(context.Background(), nil, SearchSourceInput{Regexp: `(?m)^type Embedded struct`})
	if err != nil {
		t.Fatalf("search_source: %v", err)
	}
	if len(src.Matches) != 1 || src.Matches[0].SymbolKey != "Embedded" {
		t.Errorf("search_source(type Embedded) = %v, want single Embedded", src.Matches)
	}
	if _, _, err := searchSource(st)(context.Background(), nil, SearchSourceInput{Regexp: "("}); err == nil {
		t.Error("search_source must reject an invalid regexp")
	}

	_, diags, err := diagnostics(st)(context.Background(), nil, DiagnosticsInput{})
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if !slices.ContainsFunc(diags.Diagnostics, func(d DiagnosticEntry) bool { return d.Kind == "type" }) {
		t.Errorf("sandbox type error missing from inventory: %v", diags.Diagnostics)
	}
}

// TestSearchImplementorsSurvivesNarrowRecheck exercises the Recheck v2
// escape hatch end to end: an edit to mvdest (unrelated to shapes) leaves
// the generation narrowly checked, carrying shapes/Embedded forward from
// an earlier type-checking session. search_implementors must still find
// Embedded correctly, by forcing a full recheck itself rather than
// silently trusting a mixed-generation answer.
func TestSearchImplementorsSurvivesNarrowRecheck(t *testing.T) {
	st := sandboxStore(t)

	if _, err := st.Edit(context.Background(), func(tx *store.Tx) error {
		return tx.EditSymbol("example.com/sandbox/mvdest", "Existing", "func Existing() int { return 1 }")
	}); err != nil {
		t.Fatalf("Edit(mvdest): %v", err)
	}

	_, impl, err := searchImplementors(st)(context.Background(), nil, SearchImplementorsInput{
		PkgPath: "shapes", SymbolKey: "Shape",
	})
	if err != nil {
		t.Fatalf("search_implementors after narrow recheck: %v", err)
	}
	if !slices.ContainsFunc(impl.Matches, func(m MatchEntry) bool { return m.SymbolKey == "Embedded" }) {
		t.Errorf("search_implementors(Shape) missing Embedded after narrow recheck: %v", impl.Matches)
	}
}
