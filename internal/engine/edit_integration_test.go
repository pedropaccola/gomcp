package engine

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
	"github.com/pedropaccola/gomcp/internal/dto"
	"github.com/pedropaccola/gomcp/internal/gate"
)

func TestCreateSymbolAndRollback(t *testing.T) {
	e := sandboxEngine(t)

	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "extra.go", "// Twice doubles x.\nfunc Twice(x float64) float64 { return 2 * x }")
	})
	if len(report.Delta) != 0 {
		t.Errorf("valid creation produced diagnostics: %v", deltaStrings(report))
	}
	if !slices.Contains(report.Changed, address.RelativePath("shapes/extra.go")) {
		t.Errorf("Changed missing the new file: %v", report.Changed)
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Twice"); !ok {
		t.Fatal("Twice not resolvable after commit")
	}
	e.Read(context.Background(), func(v *gate.View) error {
		if src, _ := v.DeclSource(spkg("shapes"), "Twice"); !strings.Contains(src, "Twice doubles") {
			t.Error("doc comment lost through the pipeline")
		}
		return nil
	})
	file, _, _ := resolveFile(e, "shapes/extra.go")
	if file == nil || !file.IsDirty() {
		t.Error("new file must be dirty until flushed")
	}

	// Rollback: an error after a successful verb must leave no trace.
	boom := errors.New("abort")
	if _, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		if err := tx.CreateSymbol(spkg("shapes"), "extra.go", "func Thrice(x float64) float64 { return 3 * x }"); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("Edit must surface fn's error, got %v", err)
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Thrice"); ok {
		t.Error("rolled-back symbol still visible")
	}

	// Collision: creating an existing key errors before any change.
	if _, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "shapes.go", "func Circle() {}")
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("collision must error mentioning existence, got %v", err)
	}
}

func TestReplaceSymbolBlastRadiusAndHealing(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.EditSymbol(spkg("shapes"), "Circle", "type Circle struct{ Radius float64 }")
	})
	if !slices.ContainsFunc(report.Delta, func(d dto.Diagnostic) bool {
		return d.Kind == dto.DiagType && strings.Contains(string(d.File), "use/use.go")
	}) {
		t.Errorf("renaming Circle's field must break use/use.go in the delta: %v", deltaStrings(report))
	}
	if slices.ContainsFunc(report.Delta, func(d dto.Diagnostic) bool { return d.Kind == dto.DiagList }) {
		t.Errorf("relayed go list compiler output must be filtered: %v", deltaStrings(report))
	}

	// Healing: revert in a second Tx. The file is already dirty, so Changed
	// must still report it (touched-by-this-Tx, not newly-dirty), the old
	// breakage must show as Resolved, and nothing new may appear.
	heal := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.EditSymbol(spkg("shapes"), "Circle", "type Circle struct{ R float64 }")
	})
	if !slices.Contains(heal.Changed, address.RelativePath("shapes/shapes.go")) {
		t.Errorf("consecutive edit to a dirty file must still report it: %v", heal.Changed)
	}
	if len(heal.Delta) != 0 {
		t.Errorf("healing edit introduced diagnostics: %v", deltaStrings(heal))
	}
	if len(heal.Resolved) == 0 {
		t.Error("healing edit must report the diagnostics it resolved")
	}
	for _, diag := range heal.Resolved {
		if !slices.Contains(report.Delta, diag) {
			t.Errorf("Resolved contains %q, which the breaking edit never introduced", diag)
		}
	}
}

func TestDeleteSymbolBlastRadius(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "Base")
	})
	if !slices.ContainsFunc(deltaStrings(report), func(s string) bool {
		return strings.Contains(s, "Base")
	}) {
		t.Errorf("deleting Base must break Embedded: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Base"); ok {
		t.Error("Base still resolvable after delete")
	}
}

func TestRenameSymbolPropagates(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle", "", "", "Round")
	})
	if len(report.Delta) != 0 {
		t.Errorf("a propagated rename must not introduce diagnostics: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Circle"); ok {
		t.Error("old name still resolvable")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Round"); !ok {
		t.Error("new name not resolvable")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Round.Area"); !ok {
		t.Error("method key did not follow the renamed receiver")
	}
	file, _, _ := resolveFile(e, "use/use.go")
	if !bytes.Contains(file.Src(), []byte("shapes.Round{")) || bytes.Contains(file.Src(), []byte("shapes.Circle")) {
		t.Errorf("use/use.go not rewritten:\n%s", file.Src())
	}
}

func TestMovePackagePropagates(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MovePackage(spkg("shapes"), spkg("geo"))
	})
	if len(report.Delta) != 0 {
		t.Errorf("a propagated package move must not introduce diagnostics: %v", deltaStrings(report))
	}
	if _, ok := resolvePackage(e, spkg("shapes")); ok {
		t.Error("old package address still resolvable")
	}
	pkg, ok := resolvePackage(e, spkg("geo"))
	if !ok || pkg.Name != "geo" || pkg.PkgPath != "example.com/sandbox/geo" {
		t.Fatalf("geo package wrong after move: %+v", pkg)
	}
	file, _, _ := resolveFile(e, "use/use.go")
	if !bytes.Contains(file.Src(), []byte(`"example.com/sandbox/geo"`)) {
		t.Errorf("import path not rewritten:\n%s", file.Src())
	}
	if !bytes.Contains(file.Src(), []byte("geo.Circle{")) {
		t.Errorf("qualifiers not renamed:\n%s", file.Src())
	}

	// Aliased imports: path rewritten, alias untouched.
	alias, _, _ := resolveFile(e, "use/alias.go")
	if !bytes.Contains(alias.Src(), []byte(`sh "example.com/sandbox/geo"`)) {
		t.Errorf("aliased import path not rewritten:\n%s", alias.Src())
	}
	if !bytes.Contains(alias.Src(), []byte("sh.Base{}")) {
		t.Errorf("alias qualifier must survive the move:\n%s", alias.Src())
	}

	// The external test package moves with its production sibling: new
	// clause name, rewritten self-import, renamed qualifiers.
	xtest, ok := resolveXTest(e, spkg("geo"))
	if !ok || xtest.Name != "geo_test" {
		t.Fatalf("XTest did not follow the move: %+v", xtest)
	}
	ext, _, _ := resolveFile(e, "geo/external_test.go")
	if !bytes.Contains(ext.Src(), []byte(`"example.com/sandbox/geo"`)) || !bytes.Contains(ext.Src(), []byte("geo.Circle{")) {
		t.Errorf("external test not rewritten:\n%s", ext.Src())
	}

	// The package doc's leading "Package shapes" opens with the new
	// name too — the one place a package rename fixes prose.
	doc, _, _ := resolveFile(e, "geo/shapes.go")
	if !bytes.Contains(doc.Src(), []byte("// Package geo provides fixture shape types for tests.")) {
		t.Errorf("package doc opening not rewritten:\n%s", doc.Src())
	}
	if bytes.Contains(doc.Src(), []byte("Package shapes")) {
		t.Errorf("old package doc opening still present:\n%s", doc.Src())
	}

	// A doc that doesn't open with "Package shapes" is left alone,
	// even though it mentions "shapes" mid-sentence.
	groups, _, _ := resolveFile(e, "geo/groups.go")
	if !bytes.Contains(groups.Src(), []byte("Kinds are grouped separately from shapes themselves.")) {
		t.Errorf("non-conforming doc was touched:\n%s", groups.Src())
	}
}

func TestPlacementPolicy(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		if err := tx.CreateSymbol(spkg("use"), "use.go", "const answer = 42"); err != nil {
			return err
		}
		if err := tx.CreateSymbol(spkg("use"), "use.go", "type helper struct{}"); err != nil {
			return err
		}
		return tx.CreateSymbol(spkg("use"), "use.go", "func (helper) run() {}")
	})
	file, _, _ := resolveFile(e, "use/use.go")
	src := string(file.Src())
	idx := func(needle string) int {
		i := strings.Index(src, needle)
		if i < 0 {
			t.Fatalf("%q missing from use.go:\n%s", needle, src)
		}
		return i
	}
	importPos := idx("import")
	constPos := idx("const answer")
	typePos := idx("type helper")
	methodPos := idx("func (helper) run")
	funcPos := idx("func NewCircle")
	if !(importPos < constPos && constPos < typePos && typePos < methodPos) {
		t.Errorf("placement order wrong: import=%d const=%d type=%d method=%d", importPos, constPos, typePos, methodPos)
	}
	if methodPos > funcPos {
		between := src[typePos:methodPos]
		if strings.Contains(between, "func NewCircle") || strings.Contains(between, "func UseArea") {
			t.Errorf("method separated from its receiver:\n%s", src)
		}
	}
}

func TestImportSelfRepair(t *testing.T) {
	e := sandboxEngine(t)
	// The exact sequence that failed live: create a package and reference it
	// from another package in the same session, before anything is flushed.
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		if err := tx.CreatePackage(spkg("colors"), ""); err != nil {
			return err
		}
		if err := tx.CreateSymbol(spkg("colors"), "colors.go", `const Red = "red"`); err != nil {
			return err
		}
		return tx.CreateSymbol(spkg("use"), "use.go", "func PaintIt() string { return colors.Red }")
	})
	if len(report.Delta) != 0 {
		t.Errorf("self-repair did not clear the delta: %v", deltaStrings(report))
	}
	if !slices.Contains(report.Changed, address.RelativePath("use/use.go")) {
		t.Errorf("repaired file missing from Changed: %v", report.Changed)
	}
	file, _, _ := resolveFile(e, "use/use.go")
	if !bytes.Contains(file.Src(), []byte(`"example.com/sandbox/colors"`)) {
		t.Errorf("import not spliced:\n%s", file.Src())
	}
}

func TestImportRepairRefusesAmbiguity(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		for _, pkg := range []address.PkgPath{spkg("a/dup"), spkg("b/dup")} {
			if err := tx.CreatePackage(pkg, ""); err != nil {
				return err
			}
			if err := tx.CreateSymbol(pkg, "dup.go", "const X = 1"); err != nil {
				return err
			}
		}
		return tx.CreateSymbol(spkg("use"), "use.go", "func Which() int { return dup.X }")
	})
	if !slices.ContainsFunc(deltaStrings(report), func(s string) bool {
		return strings.Contains(s, "undefined: dup")
	}) {
		t.Errorf("ambiguous name must leave the diagnostic standing: %v", deltaStrings(report))
	}
	file, _, _ := resolveFile(e, "use/use.go")
	if bytes.Contains(file.Src(), []byte("/dup")) {
		t.Errorf("repair guessed between ambiguous packages:\n%s", file.Src())
	}
}

func TestReplaceGroupedSpec(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.EditSymbol(spkg("shapes"), "DefaultScale", "// DefaultScale stretches everything.\nDefaultScale = 99.0")
	})
	if len(report.Delta) != 0 {
		t.Errorf("spec replacement introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(context.Background(), func(v *gate.View) error {
		if spec, _ := v.SpecSource(spkg("shapes"), "DefaultScale"); !strings.Contains(spec, "= 99.0") {
			t.Errorf("spec not replaced: %q", spec)
		}
		return nil
	})
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "debugMode"); !ok {
		t.Error("sibling spec destroyed by grouped replacement")
	}
}

func TestDeleteGroupedSpec(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "Scalar")
	})
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Scalar"); ok {
		t.Error("Scalar still resolvable")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Pair"); !ok {
		t.Error("sibling spec destroyed by grouped deletion")
	}
	// Deleting the last member removes the whole (now empty) group decl.
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "Pair")
	})
	file, _, _ := resolveFile(e, "shapes/groups.go")
	if bytes.Contains(file.Src(), []byte("type (")) {
		t.Errorf("empty type group left behind:\n%s", file.Src())
	}
}

func TestDeleteTrimsMultiNameSpec(t *testing.T) {
	// var minX, maxX = -10.0, 10.0 — one value per name: the targeted
	// name (and its paired value) is trimmed from the spec, not refused.
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "minX")
	})
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "minX"); ok {
		t.Error("minX still resolvable after delete")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "maxX"); !ok {
		t.Error("maxX destroyed by trimming its sibling minX")
	}
	file, _, _ := resolveFile(e, "shapes/groups.go")
	if !bytes.Contains(file.Src(), []byte("var maxX = 10.0")) {
		t.Errorf("maxX not trimmed to a standalone spec:\n%s", file.Src())
	}
}

func TestRenameMethodReportsBrokenSatisfaction(t *testing.T) {
	e := sandboxEngine(t)
	// Renaming only Circle's method is exact for the object and its uses,
	// but Circle stops satisfying Shape — the documented v1 semantics say
	// that breakage arrives in the echo, not silently.
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "", "Circle.Extent")
	})
	if !slices.ContainsFunc(deltaStrings(report), func(s string) bool {
		return strings.Contains(s, "does not implement") || strings.Contains(s, "missing method Area")
	}) {
		t.Errorf("broken interface satisfaction missing from delta: %v", deltaStrings(report))
	}
	file, _, _ := resolveFile(e, "use/use.go")
	if !bytes.Contains(file.Src(), []byte("c.Extent()")) {
		t.Errorf("direct method call not renamed:\n%s", file.Src())
	}
}

func TestMoveSymbol(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "NotShape", "", "groups.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("move introduced diagnostics: %v", deltaStrings(report))
	}
	sym, _, ok := resolveSymbol(e, spkg("shapes"), "NotShape")
	if !ok {
		t.Fatal("NotShape lost by move")
	}
	if sym.File != "shapes/groups.go" {
		t.Errorf("NotShape lives in %q, want shapes/groups.go", sym.File)
	}
	// A method moves too: without its receiver anchor in the destination it
	// falls to the bottom, and interface satisfaction stays intact.
	report = mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "groups.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("method move introduced diagnostics: %v", deltaStrings(report))
	}
}

func TestMoveGroupedSpec(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "DefaultScale", "", "shapes.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("grouped move introduced diagnostics: %v", deltaStrings(report))
	}
	sym, _, ok := resolveSymbol(e, spkg("shapes"), "DefaultScale")
	if !ok {
		t.Fatal("DefaultScale lost by move")
	}
	if sym.File != "shapes/shapes.go" {
		t.Errorf("DefaultScale lives in %q, want shapes/shapes.go", sym.File)
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "debugMode"); !ok {
		t.Error("sibling spec destroyed by grouped move")
	}
	file, _, _ := resolveFile(e, "shapes/shapes.go")
	if !bytes.Contains(file.Src(), []byte("var DefaultScale")) {
		t.Errorf("grouped member not extracted as standalone declaration:\n%s", file.Src())
	}
}

func TestMoveRefusals(t *testing.T) {
	e := sandboxEngine(t)
	cases := []struct {
		key, file, want string
	}{
		{"minX", "shapes.go", "declared together"},
		{"NotShape", "shapes.go", "already lives"},
		{"Missing", "shapes.go", "no symbol"},
		{"NotShape", "extra_test.go", "test build boundary"},
	}
	for _, tc := range cases {
		_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
			return tx.MoveSymbol(spkg("shapes"), tc.key, "", tc.file, "")
		})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("MoveSymbol(%q, %q) error = %v, want it to contain %q", tc.key, tc.file, err, tc.want)
		}
	}
}

func TestMoveToNewFile(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "extra.go", "// Doubled reports twice the default scale.\nfunc Doubled() float64 { return 2 * DefaultScale }")
	})
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Doubled", "", "moved.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("move introduced diagnostics: %v", deltaStrings(report))
	}
	sym, _, ok := resolveSymbol(e, spkg("shapes"), "Doubled")
	if !ok {
		t.Fatal("Doubled lost by move")
	}
	if sym.File != "shapes/moved.go" {
		t.Errorf("Doubled lives in %q, want shapes/moved.go", sym.File)
	}
	file, _, _ := resolveFile(e, "shapes/moved.go")
	if !bytes.Contains(file.Src(), []byte("// Doubled reports twice the default scale.")) {
		t.Errorf("doc comment did not travel with the move:\n%s", file.Src())
	}
}

// TestEditDeltaExcludesPreexistingBrokenness confirms an edit's echo stays
// silent about diagnostics that existed before it and still exist after —
// including on the very first edit against an already-broken workspace.
// The sandbox's permanently type-broken package must show up in neither
// Delta nor Resolved, and must be counted in Unrelated instead.
func TestEditDeltaExcludesPreexistingBrokenness(t *testing.T) {
	e := sandboxEngine(t)

	var brokenCount int
	e.Read(context.Background(), func(v *gate.View) error {
		brokenCount = len(v.Diagnostics(spkg("broken")))
		return nil
	})
	if brokenCount == 0 {
		t.Fatal("sandbox's broken package must carry at least one diagnostic for this test to mean anything")
	}

	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "extra.go", "func Quadruple(x float64) float64 { return 4 * x }")
	})
	if len(report.Delta) != 0 {
		t.Errorf("unrelated edit produced diagnostics: %v", deltaStrings(report))
	}
	if len(report.Resolved) != 0 {
		t.Errorf("unrelated edit resolved diagnostics it never touched: %v", report.Resolved)
	}
	if report.Unrelated != brokenCount {
		t.Errorf("Unrelated = %d, want %d (the untouched broken package's diagnostics)", report.Unrelated, brokenCount)
	}
}

func TestCreateFileWithDocAndEditFile(t *testing.T) {
	e := sandboxEngine(t)

	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateFile(spkg("shapes"), "extra_doc.go", "Extra holds throwaway fixtures for this test.")
	})
	e.Read(context.Background(), func(v *gate.View) error {
		pkg, _ := v.Package(spkg("shapes"))
		for _, f := range pkg.Files() {
			if f.Path().Base() == "extra_doc.go" && f.Doc() != "Extra holds throwaway fixtures for this test." {
				t.Errorf("new file's doc = %q", f.Doc())
			}
		}
		return nil
	})

	// EditFile replaces the doc without touching the rest of the file.
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.EditFile(spkg("shapes"), "extra_doc.go", "Replaced doc.")
	})
	e.Read(context.Background(), func(v *gate.View) error {
		pkg, _ := v.Package(spkg("shapes"))
		for _, f := range pkg.Files() {
			if f.Path().Base() == "extra_doc.go" && f.Doc() != "Replaced doc." {
				t.Errorf("edited file's doc = %q, want %q", f.Doc(), "Replaced doc.")
			}
		}
		return nil
	})

	// Clearing removes the doc entirely.
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.EditFile(spkg("shapes"), "extra_doc.go", "")
	})
	e.Read(context.Background(), func(v *gate.View) error {
		pkg, _ := v.Package(spkg("shapes"))
		for _, f := range pkg.Files() {
			if f.Path().Base() == "extra_doc.go" && f.Doc() != "" {
				t.Errorf("cleared file still has doc: %q", f.Doc())
			}
		}
		return nil
	})

	// EditFile on an existing doc must only touch the doc comment.
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.EditFile(spkg("shapes"), "shapes.go", "New shapes doc.")
	})
	e.Read(context.Background(), func(v *gate.View) error {
		if src, _ := v.DeclSource(spkg("shapes"), "Shape"); !strings.Contains(src, "Shape is anything with an area") {
			t.Errorf("EditFile disturbed an unrelated declaration:\n%s", src)
		}
		pkg, _ := v.Package(spkg("shapes"))
		for _, f := range pkg.Files() {
			if f.Path().Base() == "shapes.go" && f.Doc() != "New shapes doc." {
				t.Errorf("shapes.go doc = %q, want %q", f.Doc(), "New shapes doc.")
			}
		}
		return nil
	})

	if _, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.EditFile(spkg("shapes"), "nope.go", "x")
	}); err == nil {
		t.Error("editing a missing file's doc must error")
	}
}

func TestMoveSymbolRenameQualification(t *testing.T) {
	e := sandboxEngine(t)

	// Non-method: bare newSymbolKey renames; a qualified form is refused
	// since there is no receiver to qualify it with.
	if _, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "NotShape", "", "", "Circle.NotShape")
	}); err == nil || !strings.Contains(err.Error(), "not a method") {
		t.Errorf("qualified newSymbolKey on a non-method must be refused, got %v", err)
	}
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "NotShape", "", "", "AlsoNotShape")
	})
	if len(report.Delta) != 0 {
		t.Errorf("non-method rename introduced diagnostics: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "AlsoNotShape"); !ok {
		t.Error("AlsoNotShape not resolvable after rename")
	}

	// Method: newSymbolKey must be qualified, and Recv must match exactly —
	// it can never actually change through a rename. The rename itself may
	// legitimately break interface satisfaction (TestRenameMethodReports
	// BrokenSatisfaction already covers that guarantee); this test only
	// cares that the qualification/Recv-matching logic behaves.
	if _, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "", "Extent")
	}); err == nil || !strings.Contains(err.Error(), "must be") {
		t.Errorf("unqualified newSymbolKey on a method must be refused, got %v", err)
	}
	if _, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "", "Square.Extent")
	}); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Errorf("mismatched receiver must be refused, got %v", err)
	}
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "", "Circle.Extent")
	})
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "Circle.Extent"); !ok {
		t.Error("Circle.Extent not resolvable after rename")
	}
}

func TestRenameUpdatesLeadingDoc(t *testing.T) {
	e := sandboxEngine(t)

	// A symbol doc that follows Go's name-first convention is updated.
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Named", "", "", "Nameable")
	})
	file, _, _ := resolveFile(e, "shapes/shapes.go")
	if !bytes.Contains(file.Src(), []byte("// Nameable is a second interface")) {
		t.Errorf("doc comment not updated:\n%s", file.Src())
	}
	if bytes.Contains(file.Src(), []byte("// Named is a second interface")) {
		t.Errorf("old doc comment still present:\n%s", file.Src())
	}

	// A doc that doesn't start with the symbol's own name is left alone —
	// the safety boundary against corrupting unrelated prose.
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "extra.go",
			"// This helper returns something related to Foo.\nfunc Foo() int { return 0 }")
	})
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Foo", "", "", "Bar")
	})
	file, _, _ = resolveFile(e, "shapes/extra.go")
	if !bytes.Contains(file.Src(), []byte("// This helper returns something related to Foo.")) {
		t.Errorf("non-conforming doc was touched:\n%s", file.Src())
	}
}

func TestMoveSoloGroupedSpecPreservesDoc(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "solo.go", "const (\n\t// Solo is the only member of its group.\n\tSolo = 1\n)")
	})
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Solo", "", "solo2.go", "")
	})
	file, _, _ := resolveFile(e, "shapes/solo2.go")
	t.Logf("moved file source:\n%s", file.Src())
	if !bytes.Contains(file.Src(), []byte("Solo is the only member")) {
		t.Errorf("doc comment lost when moving a solo grouped spec:\n%s", file.Src())
	}
}

func TestRenameSoloGroupedSpecUpdatesDoc(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "solo.go", "const (\n\t// Solo is the only member of its group.\n\tSolo = 1\n)")
	})
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Solo", "", "", "Alone")
	})
	file, _, _ := resolveFile(e, "shapes/solo.go")
	t.Logf("renamed file source:\n%s", file.Src())
	if !bytes.Contains(file.Src(), []byte("// Alone is the only member")) {
		t.Errorf("solo grouped spec's doc not updated:\n%s", file.Src())
	}
}

func TestMoveSymbolRenamesIotaGroupMember(t *testing.T) {
	e := sandboxEngine(t)

	// A non-anchor member renames cleanly.
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "KindSquare", "", "", "KindQuad")
	})
	if len(report.Delta) != 0 {
		t.Errorf("renaming a non-anchor iota member introduced diagnostics: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "KindQuad"); !ok {
		t.Error("KindQuad not resolvable after rename")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "KindSquare"); ok {
		t.Error("old name KindSquare still resolvable")
	}

	// The anchor — the member carrying the explicit iota expression —
	// renames the same way: renaming never touches position, so there's
	// nothing special about being the anchor. Its sibling is untouched.
	report = mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "KindCircle", "", "", "KindRound")
	})
	if len(report.Delta) != 0 {
		t.Errorf("renaming the iota group's anchor introduced diagnostics: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "KindRound"); !ok {
		t.Error("KindRound not resolvable after rename")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "KindQuad"); !ok {
		t.Error("KindQuad lost when renaming the anchor")
	}
	file, _, _ := resolveFile(e, "shapes/groups.go")
	if !bytes.Contains(file.Src(), []byte("// KindRound is the round one.")) {
		t.Errorf("anchor's own leading doc not updated:\n%s", file.Src())
	}
}

func TestMoveWholeIotaGroup(t *testing.T) {
	e := sandboxEngine(t)
	// Name the non-anchor member — the whole group must still move,
	// including the anchor, which was never named.
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "KindSquare", "", "kinds.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("moving an iota group introduced diagnostics: %v", deltaStrings(report))
	}
	for _, key := range []string{"KindCircle", "KindSquare"} {
		sym, _, ok := resolveSymbol(e, spkg("shapes"), key)
		if !ok {
			t.Fatalf("%s lost by group move", key)
		}
		if sym.File != "shapes/kinds.go" {
			t.Errorf("%s lives in %q, want shapes/kinds.go (only KindSquare was named)", key, sym.File)
		}
	}
	file, _, _ := resolveFile(e, "shapes/kinds.go")
	if !bytes.Contains(file.Src(), []byte("KindCircle Kind = iota")) || !bytes.Contains(file.Src(), []byte("KindSquare")) {
		t.Errorf("group not moved intact:\n%s", file.Src())
	}
	old, _, _ := resolveFile(e, "shapes/groups.go")
	if bytes.Contains(old.Src(), []byte("KindCircle")) || bytes.Contains(old.Src(), []byte("KindSquare")) {
		t.Errorf("old file still has group members:\n%s", old.Src())
	}
}

func TestDeleteWholeIotaGroup(t *testing.T) {
	e := sandboxEngine(t)
	// Deleting the non-anchor member removes the whole group, not just
	// that one spec — a position-dependent group's members can't be
	// safely separated by deletion alone.
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "KindSquare")
	})
	if len(report.Delta) != 0 {
		t.Errorf("deleting an iota group introduced diagnostics: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "KindCircle"); ok {
		t.Error("KindCircle survived deleting its sibling KindSquare")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "KindSquare"); ok {
		t.Error("KindSquare still resolvable after delete")
	}
	file, _, _ := resolveFile(e, "shapes/groups.go")
	if bytes.Contains(file.Src(), []byte("KindCircle")) || bytes.Contains(file.Src(), []byte("KindSquare")) {
		t.Errorf("group not fully removed:\n%s", file.Src())
	}
}

func TestEditIotaGroupWholeReplacement(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.EditSymbol(spkg("shapes"), "KindSquare",
			"// KindCircle is the round one.\nKindCircle Kind = iota\nKindSquare\nKindTriangle")
	})
	if len(report.Delta) != 0 {
		t.Errorf("whole-group edit introduced diagnostics: %v", deltaStrings(report))
	}
	for _, key := range []string{"KindCircle", "KindSquare", "KindTriangle"} {
		if _, _, ok := resolveSymbol(e, spkg("shapes"), key); !ok {
			t.Errorf("%s missing after whole-group edit", key)
		}
	}
}

func TestEditIotaGroupPartialSubmissionDropsSiblings(t *testing.T) {
	e := sandboxEngine(t)
	// A partial submission is accepted as given — silently dropping the
	// sibling that wasn't mentioned, exactly as documented.
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.EditSymbol(spkg("shapes"), "KindCircle", "// KindCircle is the round one.\nKindCircle Kind = iota")
	})
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "KindCircle"); !ok {
		t.Error("KindCircle lost by its own whole-group edit")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "KindSquare"); ok {
		t.Error("KindSquare survived a replacement that didn't mention it")
	}
}

func TestEditIotaGroupRefusesRenamingTargetedKey(t *testing.T) {
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.EditSymbol(spkg("shapes"), "KindCircle",
			"// KindRound is the round one.\nKindRound Kind = iota\nKindSquare")
	})
	if err == nil || !strings.Contains(err.Error(), "move_symbol") {
		t.Errorf("renaming the targeted key via edit_symbol must be refused with a move_symbol pointer, got %v", err)
	}
}

func TestEditGroupRefusesIntroducingIota(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "consts.go", "const (\n\tFoo = 1\n\tBar = 2\n)")
	})
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.EditSymbol(spkg("shapes"), "Foo", "Foo = iota")
	})
	if err == nil || !strings.Contains(err.Error(), "introduce iota") {
		t.Errorf("introducing iota into a plain group must be refused, got %v", err)
	}
}

func TestCreatePlainConstMergesIntoExistingGroup(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "consts2.go", "const (\n\tFoo = 1\n\tBar = 2\n)")
	})
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "consts2.go", "const Baz = 3")
	})
	file, _, _ := resolveFile(e, "shapes/consts2.go")
	src := string(file.Src())
	if strings.Count(src, "const") != 1 {
		t.Errorf("expected exactly one const group after merge, got source:\n%s", src)
	}
	for _, key := range []string{"Foo", "Bar", "Baz"} {
		if _, _, ok := resolveSymbol(e, spkg("shapes"), key); !ok {
			t.Errorf("%s missing after merge", key)
		}
	}
}

func TestCreateIotaGroupNeverMergesIntoExistingGroup(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "kinds2.go", "const (\n\tFirstA = iota\n\tSecondA\n)")
	})
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "kinds2.go", "const (\n\tFirstB = iota\n\tSecondB\n)")
	})
	file, _, _ := resolveFile(e, "shapes/kinds2.go")
	src := string(file.Src())
	if strings.Count(src, "const") != 2 {
		t.Errorf("expected two separate iota groups, got source:\n%s", src)
	}
	for _, key := range []string{"FirstA", "SecondA", "FirstB", "SecondB"} {
		if _, _, ok := resolveSymbol(e, spkg("shapes"), key); !ok {
			t.Errorf("%s missing", key)
		}
	}
}

func TestCreateTypedIotaGroupNearItsType(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "status.go", "// Status is a lifecycle state.\ntype Status int")
	})
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "status.go",
			"const (\n\t// Active is running.\n\tActive Status = iota\n\tInactive\n)")
	})
	file, _, _ := resolveFile(e, "shapes/status.go")
	src := string(file.Src())
	typeIdx := strings.Index(src, "type Status int")
	constIdx := strings.Index(src, "Active Status = iota")
	if typeIdx == -1 || constIdx == -1 {
		t.Fatalf("declarations missing:\n%s", src)
	}
	if constIdx < typeIdx {
		t.Errorf("iota group not placed after its type declaration:\n%s", src)
	}
}

func TestCreateUntypedIotaGroupStandardRegion(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "flags.go", "// Flag is a marker type.\ntype Flag int")
	})
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "flags.go", "const (\n\tFlagA = 1 << iota\n\tFlagB\n)")
	})
	file, _, _ := resolveFile(e, "shapes/flags.go")
	src := string(file.Src())
	typeIdx := strings.Index(src, "type Flag int")
	constIdx := strings.Index(src, "FlagA = 1 << iota")
	if typeIdx == -1 || constIdx == -1 {
		t.Fatalf("declarations missing:\n%s", src)
	}
	if constIdx > typeIdx {
		t.Errorf("untyped iota group should land in the standard const/var region, before the type:\n%s", src)
	}
}

func TestDeleteBlanksSharedMultiValueSpec(t *testing.T) {
	// var boundX, boundY = boundsOf() — one shared call: the call's
	// arity is fixed, so the targeted name blanks to `_` instead of
	// shrinking the list.
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "boundX")
	})
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "boundX"); ok {
		t.Error("boundX still resolvable after delete")
	}
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "boundY"); !ok {
		t.Error("boundY destroyed by blanking its sibling boundX")
	}
	file, _, _ := resolveFile(e, "shapes/groups.go")
	if !bytes.Contains(file.Src(), []byte("var _, boundY = boundsOf()")) {
		t.Errorf("boundX not blanked to _:\n%s", file.Src())
	}
}

func TestDeleteConvergesToFullRemovalWhenNoRealNameRemains(t *testing.T) {
	// Deleting every real name out of a shared multi-value spec collapses
	// the whole statement, call included — same as deleting a solo name,
	// since nothing is bound to it anymore.
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "boundX")
	})
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "boundY")
	})
	if _, _, ok := resolveSymbol(e, spkg("shapes"), "boundY"); ok {
		t.Error("boundY still resolvable after its spec should have collapsed")
	}
	file, _, _ := resolveFile(e, "shapes/groups.go")
	if bytes.Contains(file.Src(), []byte("= boundsOf()")) {
		t.Errorf("shared-call spec not fully collapsed after its last real name was deleted:\n%s", file.Src())
	}
}

func TestDeleteSymbolNoopIfAbsent(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "NoSuchSymbol")
	})
	if len(report.Changed) != 0 || len(report.Delta) != 0 || len(report.Resolved) != 0 {
		t.Errorf("deleting a nonexistent symbol must be a pure noop, got %+v", report)
	}
}

func TestDeleteSymbolNoopAfterGroupCollapse(t *testing.T) {
	// Deleting KindSquare removes the whole iota group, including
	// KindCircle. A later delete targeting KindCircle must be a noop,
	// not a failure — exactly what lets a batch name every member of a
	// group without the second entry aborting just because the first
	// already satisfied it.
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "KindSquare")
	})
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "KindCircle")
	})
	if len(report.Changed) != 0 || len(report.Delta) != 0 {
		t.Errorf("deleting an already-collapsed group member must be a noop, got %+v", report)
	}
}

func TestDeleteFileNoopIfAbsent(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		if err := tx.DeleteFile(spkg("shapes"), "nosuch.go"); err != nil {
			return err
		}
		return tx.DeleteFile(spkg("nosuchpkg"), "nosuch.go")
	})
	if len(report.Changed) != 0 {
		t.Errorf("deleting a nonexistent file (missing file, and missing package) must be a noop, got %+v", report)
	}
}

func TestDeletePackageNoopIfAbsent(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.DeletePackage(spkg("nosuchpkg"))
	})
	if len(report.Changed) != 0 {
		t.Errorf("deleting a nonexistent package must be a noop, got %+v", report)
	}
}

func TestMoveSymbolCrossPackageQualifierRewrite(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("mvsrc"), "Perimeter", spkg("mvdest"), "mvdest.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("cross-package move introduced diagnostics: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("mvdest"), "Perimeter"); !ok {
		t.Error("Perimeter not resolvable in mvdest after the move")
	}
	if _, _, ok := resolveSymbol(e, spkg("mvsrc"), "Perimeter"); ok {
		t.Error("Perimeter still resolvable in mvsrc after the move")
	}
	srcFile, _, _ := resolveFile(e, "mvsrc/mvsrc.go")
	if !bytes.Contains(srcFile.Src(), []byte("mvdest.Perimeter(r)")) {
		t.Errorf("sibling reference didn't gain the destination qualifier:\n%s", srcFile.Src())
	}
	useFile, _, _ := resolveFile(e, "use/use.go")
	if !bytes.Contains(useFile.Src(), []byte("mvdest.Perimeter(r)")) {
		t.Errorf("third-party reference wasn't repointed to the new package:\n%s", useFile.Src())
	}
	if bytes.Contains(useFile.Src(), []byte("mvsrc.Perimeter")) {
		t.Errorf("third-party reference still qualifies the old package:\n%s", useFile.Src())
	}
}

func TestMoveSymbolQualifierDropsAtDestination(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("mvalpha"), "Solo", spkg("mvbeta"), "mvbeta.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("cross-package move introduced diagnostics: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("mvbeta"), "Solo"); !ok {
		t.Error("Solo not resolvable in mvbeta after the move")
	}
	file, _, _ := resolveFile(e, "mvbeta/mvbeta.go")
	if !bytes.Contains(file.Src(), []byte("return Solo()")) {
		t.Errorf("destination's pre-existing reference didn't lose its qualifier:\n%s", file.Src())
	}
	if bytes.Contains(file.Src(), []byte("mvalpha")) {
		t.Errorf("stale mvalpha qualifier or import left behind:\n%s", file.Src())
	}
}

func TestMoveSymbolRefusesDependencyConflict(t *testing.T) {
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("mvsrc"), "dependsOnUnexported", spkg("mvdest"), "mvdest.go", "")
	})
	if err == nil || !strings.Contains(err.Error(), "qhelper") {
		t.Errorf("expected a refusal naming qhelper, got %v", err)
	}
}

func TestMoveSymbolRefusesBlockingReferrer(t *testing.T) {
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("mvsrc"), "unexportedThing", spkg("mvdest"), "mvdest.go", "")
	})
	if err == nil || !strings.Contains(err.Error(), "usesUnexportedThing") {
		t.Errorf("expected a refusal naming usesUnexportedThing, got %v", err)
	}
}

func TestMoveSymbolRefusesMethodWithoutReceiver(t *testing.T) {
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("mvsrc"), "Box.M", spkg("mvdest"), "mvdest.go", "")
	})
	if err == nil || !strings.Contains(err.Error(), "receiver type") {
		t.Errorf("expected a refusal about receiver locality, got %v", err)
	}
}

func TestMoveSymbolRefusesCollision(t *testing.T) {
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("mvsrc"), "Perimeter", spkg("mvdest"), "mvdest.go", "Existing")
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected a collision refusal, got %v", err)
	}
}

func TestMoveFileRefusesCrossPackageConflict(t *testing.T) {
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveFile(spkg("mvsrc"), "methodfile.go", spkg("mvdest"), "")
	})
	if err == nil || !strings.Contains(err.Error(), "receiver type") {
		t.Errorf("expected a method-locality refusal (Box stays behind in mvsrc.go), got %v", err)
	}
}

func TestMoveFileCrossPackageSucceedsWhenSafe(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveFile(spkg("mvsrc"), "standalone.go", spkg("mvdest"), "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("cross-package file move introduced diagnostics: %v", deltaStrings(report))
	}
	if _, _, ok := resolveSymbol(e, spkg("mvdest"), "StandaloneFunc"); !ok {
		t.Error("StandaloneFunc not resolvable in mvdest after the file move")
	}
	// MoveFile now rewrites external qualifiers too: use.go's pre-existing
	// mvsrc.StandaloneFunc reference must be repointed to mvdest, not left
	// dangling.
	useFile, _, _ := resolveFile(e, "use/use.go")
	if !bytes.Contains(useFile.Src(), []byte("mvdest.StandaloneFunc()")) {
		t.Errorf("external reference wasn't repointed to the new package:\n%s", useFile.Src())
	}
}

func TestMoveFileRefusesLeavingReceiverBehind(t *testing.T) {
	// mvsrc.go declares Box; methodfile.go declares Box.AreaOfBox. Moving
	// just mvsrc.go would leave AreaOfBox behind without its receiver type
	// — the reverse of TestMoveFileRefusesCrossPackageConflict's case.
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveFile(spkg("mvsrc"), "mvsrc.go", spkg("mvdest"), "")
	})
	if err == nil || !strings.Contains(err.Error(), "AreaOfBox") || !strings.Contains(err.Error(), "left behind") {
		t.Errorf("expected a refusal naming AreaOfBox left behind without Box, got %v", err)
	}
}

func TestMoveSymbolRefusesLeavingMethodsBehind(t *testing.T) {
	// Box (in mvsrc.go) has methods M (mvsrc.go) and AreaOfBox
	// (methodfile.go). Moving just the Box type via MoveSymbol must refuse
	// — both methods would be left behind without their receiver type.
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("mvsrc"), "Box", spkg("mvdest"), "mvdest.go", "")
	})
	if err == nil || !strings.Contains(err.Error(), "left behind") {
		t.Errorf("expected a refusal about methods left behind without Box, got %v", err)
	}
}

func TestMoveSymbolRequalifiesOutboundExportedDependency(t *testing.T) {
	// OutboundExported calls PublicHelper, an exported sibling staying
	// behind. moveConflicts doesn't refuse this (only unexported blocking
	// referrers refuse) — the moved code's own outbound reference must
	// gain srcPkg's qualifier instead, not break silently.
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("mvsrc"), "OutboundExported", spkg("mvdest"), "mvdest.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("cross-package move introduced diagnostics: %v", deltaStrings(report))
	}
	file, _, _ := resolveFile(e, "mvdest/mvdest.go")
	if !bytes.Contains(file.Src(), []byte("mvsrc.PublicHelper()")) {
		t.Errorf("outbound reference to the remaining exported sibling wasn't requalified:\n%s", file.Src())
	}
}

func TestMoveFileRequalifiesOutboundExportedDependency(t *testing.T) {
	// fileoutbound.go's FileOutbound calls PublicHelper, declared in a
	// different file (outbound.go) that isn't moving — tests MoveFile's
	// outbound qualifier fixup specifically, not just MoveSymbol's.
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveFile(spkg("mvsrc"), "fileoutbound.go", spkg("mvdest"), "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("cross-package file move introduced diagnostics: %v", deltaStrings(report))
	}
	file, _, _ := resolveFile(e, "mvdest/fileoutbound.go")
	if !bytes.Contains(file.Src(), []byte("mvsrc.PublicHelper()")) {
		t.Errorf("outbound reference to the remaining exported sibling wasn't requalified:\n%s", file.Src())
	}
}

// TestRenameDoesNotCorruptSameNamedStructField verifies that renaming a
// package-level declaration leaves an unrelated struct field sharing its
// bare name untouched.
func TestRenameDoesNotCorruptSameNamedStructField(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Square", "", "", "Rectangle")
	})
	if len(report.Delta) != 0 {
		t.Errorf("rename introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(context.Background(), func(v *gate.View) error {
		src, ok := v.DeclSource(spkg("shapes"), "Wrapper.WrapperArea")
		if !ok {
			t.Fatal("Wrapper.WrapperArea not found after renaming the unrelated Square type")
		}
		if !strings.Contains(src, "w.Square.Area()") {
			t.Errorf("field access w.Square must survive a rename of the unrelated Square type, got: %q", src)
		}
		return nil
	})
}

// TestMoveFileDoesNotCorruptMethodCallSites moves Gauge and its method
// Value cross-package via MoveFile, verifying QualifierFixups requalifies
// the bare type reference in use.go but leaves the method call site
// itself untouched — method calls carry no package qualifier the way a
// bare type/func reference does.
func TestMoveFileDoesNotCorruptMethodCallSites(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *gate.Tx) error {
		return tx.MoveFile(spkg("mvsrc"), "qualmethod.go", spkg("mvdest"), "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("cross-package file move introduced diagnostics: %v", deltaStrings(report))
	}
	useFile, _, _ := resolveFile(e, "use/use.go")
	if !bytes.Contains(useFile.Src(), []byte("mvdest.Gauge{N: 3}")) {
		t.Errorf("type reference wasn't requalified to the new package:\n%s", useFile.Src())
	}
	if !bytes.Contains(useFile.Src(), []byte("gauge.Value()")) {
		t.Errorf("method call site was corrupted by the move:\n%s", useFile.Src())
	}
}

// TestMoveSymbolGroupMovesTypeAndMethodsAcrossFiles proves MoveSymbolGroup
// handles methods spread across multiple source files correctly — Box's
// M lives in mvsrc.go, AreaOfBox in methodfile.go — each gets its own
// per-file extraction, and all three land together in the destination.
func TestMoveSymbolGroupMovesTypeAndMethodsAcrossFiles(t *testing.T) {
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *gate.Tx) error {
		return tx.MoveSymbolGroup(spkg("mvsrc"), []string{"Box", "Box.M", "Box.AreaOfBox"}, spkg("mvdest"), "box.go")
	})
	if err != nil {
		t.Fatalf("MoveSymbolGroup: %v", err)
	}
	for _, key := range []string{"Box", "Box.M", "Box.AreaOfBox"} {
		if _, _, ok := resolveSymbol(e, spkg("mvsrc"), key); ok {
			t.Errorf("%q should no longer exist in mvsrc", key)
		}
		if _, _, ok := resolveSymbol(e, spkg("mvdest"), key); !ok {
			t.Errorf("%q missing from mvdest after MoveSymbolGroup", key)
		}
	}
}
