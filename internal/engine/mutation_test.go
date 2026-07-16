package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pedropaccola/gomcp/internal/address"
)

func mustEdit(t *testing.T, e *Engine, fn func(*Tx) error) *EditReport {
	t.Helper()
	report, err := e.Edit(context.Background(), fn)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if report.Stale {
		t.Fatalf("recheck unavailable: %s", report.Note)
	}
	return report
}

func deltaStrings(report *EditReport) []string {
	out := make([]string, 0, len(report.Delta))
	for _, d := range report.Delta {
		out = append(out, d.String())
	}
	return out
}

func TestCreateSymbolAndRollback(t *testing.T) {
	e := sandboxEngine(t)

	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "extra.go", "// Twice doubles x.\nfunc Twice(x float64) float64 { return 2 * x }")
	})
	if len(report.Delta) != 0 {
		t.Errorf("valid creation produced diagnostics: %v", deltaStrings(report))
	}
	if !slices.Contains(report.Changed, address.RelativePath("shapes/extra.go")) {
		t.Errorf("Changed missing the new file: %v", report.Changed)
	}
	e.Read(func(v *View) error {
		sym, _, ok := v.resolveSymbol(spkg("shapes"), "Twice")
		if !ok {
			t.Fatal("Twice not resolvable after commit")
		}
		if src, _ := v.declSource(sym); !bytes.Contains(src, []byte("Twice doubles")) {
			t.Error("doc comment lost through the pipeline")
		}
		file, _, _ := v.resolveFile("shapes/extra.go")
		if file == nil || !file.Dirty() {
			t.Error("new file must be dirty until flushed")
		}
		return nil
	})

	// Rollback: an error after a successful verb must leave no trace.
	boom := errors.New("abort")
	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		if err := tx.CreateSymbol(spkg("shapes"), "extra.go", "func Thrice(x float64) float64 { return 3 * x }"); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("Edit must surface fn's error, got %v", err)
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Thrice"); ok {
			t.Error("rolled-back symbol still visible")
		}
		return nil
	})

	// Collision: creating an existing key errors before any change.
	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "shapes.go", "func Circle() {}")
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("collision must error mentioning existence, got %v", err)
	}
}

func TestReplaceSymbolBlastRadiusAndHealing(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.EditSymbol(spkg("shapes"), "Circle", "type Circle struct{ Radius float64 }")
	})
	if !slices.ContainsFunc(report.Delta, func(d Diagnostic) bool {
		return d.Kind == DiagType && strings.Contains(string(d.File), "use/use.go")
	}) {
		t.Errorf("renaming Circle's field must break use/use.go in the delta: %v", deltaStrings(report))
	}
	if slices.ContainsFunc(report.Delta, func(d Diagnostic) bool { return d.Kind == DiagList }) {
		t.Errorf("relayed go list compiler output must be filtered: %v", deltaStrings(report))
	}

	// Healing: revert in a second Tx. The file is already dirty, so Changed
	// must still report it (touched-by-this-Tx, not newly-dirty), the old
	// breakage must show as Resolved, and nothing new may appear.
	heal := mustEdit(t, e, func(tx *Tx) error {
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
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "Base")
	})
	if !slices.ContainsFunc(deltaStrings(report), func(s string) bool {
		return strings.Contains(s, "Base")
	}) {
		t.Errorf("deleting Base must break Embedded: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Base"); ok {
			t.Error("Base still resolvable after delete")
		}
		return nil
	})
}

func TestRenameSymbolPropagates(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.renameSymbol(spkg("shapes"), "Circle", "Round")
	})
	if len(report.Delta) != 0 {
		t.Errorf("a propagated rename must not introduce diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Circle"); ok {
			t.Error("old name still resolvable")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Round"); !ok {
			t.Error("new name not resolvable")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Round.Area"); !ok {
			t.Error("method key did not follow the renamed receiver")
		}
		file, _, _ := v.resolveFile("use/use.go")
		if !bytes.Contains(file.Src(), []byte("shapes.Round{")) || bytes.Contains(file.Src(), []byte("shapes.Circle")) {
			t.Errorf("use/use.go not rewritten:\n%s", file.Src())
		}
		return nil
	})
}

func TestMovePackagePropagates(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MovePackage(spkg("shapes"), spkg("geo"))
	})
	if len(report.Delta) != 0 {
		t.Errorf("a propagated package move must not introduce diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, ok := v.resolvePackage(spkg("shapes")); ok {
			t.Error("old package address still resolvable")
		}
		pkg, ok := v.resolvePackage(spkg("geo"))
		if !ok || pkg.Name != "geo" || pkg.PkgPath != "example.com/sandbox/geo" {
			t.Fatalf("geo package wrong after move: %+v", pkg)
		}
		file, _, _ := v.resolveFile("use/use.go")
		if !bytes.Contains(file.Src(), []byte(`"example.com/sandbox/geo"`)) {
			t.Errorf("import path not rewritten:\n%s", file.Src())
		}
		if !bytes.Contains(file.Src(), []byte("geo.Circle{")) {
			t.Errorf("qualifiers not renamed:\n%s", file.Src())
		}

		// Aliased imports: path rewritten, alias untouched.
		alias, _, _ := v.resolveFile("use/alias.go")
		if !bytes.Contains(alias.Src(), []byte(`sh "example.com/sandbox/geo"`)) {
			t.Errorf("aliased import path not rewritten:\n%s", alias.Src())
		}
		if !bytes.Contains(alias.Src(), []byte("sh.Base{}")) {
			t.Errorf("alias qualifier must survive the move:\n%s", alias.Src())
		}

		// The external test package moves with its production sibling: new
		// clause name, rewritten self-import, renamed qualifiers.
		xtest, ok := v.resolveXTest(spkg("geo"))
		if !ok || xtest.Name != "geo_test" {
			t.Fatalf("XTest did not follow the move: %+v", xtest)
		}
		ext, _, _ := v.resolveFile("geo/external_test.go")
		if !bytes.Contains(ext.Src(), []byte(`"example.com/sandbox/geo"`)) || !bytes.Contains(ext.Src(), []byte("geo.Circle{")) {
			t.Errorf("external test not rewritten:\n%s", ext.Src())
		}

		// The package doc's leading "Package shapes" opens with the new
		// name too — the one place a package rename fixes prose.
		doc, _, _ := v.resolveFile("geo/shapes.go")
		if !bytes.Contains(doc.Src(), []byte("// Package geo provides fixture shape types for tests.")) {
			t.Errorf("package doc opening not rewritten:\n%s", doc.Src())
		}
		if bytes.Contains(doc.Src(), []byte("Package shapes")) {
			t.Errorf("old package doc opening still present:\n%s", doc.Src())
		}

		// A doc that doesn't open with "Package shapes" is left alone,
		// even though it mentions "shapes" mid-sentence.
		groups, _, _ := v.resolveFile("geo/groups.go")
		if !bytes.Contains(groups.Src(), []byte("Kinds are grouped separately from shapes themselves.")) {
			t.Errorf("non-conforming doc was touched:\n%s", groups.Src())
		}
		return nil
	})
}

func TestCreatePackageThroughRecheck(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		if err := tx.CreatePackage(spkg("util"), ""); err != nil {
			return err
		}
		return tx.CreateSymbol(spkg("util"), "util.go", "func Half(x float64) float64 { return x / 2 }")
	})
	if len(report.Delta) != 0 {
		t.Errorf("new package produced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		pkg, ok := v.resolvePackage(spkg("util"))
		if !ok {
			t.Fatal("util package missing after recheck — overlay-only directories not surviving the reload")
		}
		if pkg.PkgPath != "example.com/sandbox/util" {
			t.Errorf("recheck did not resolve the import path: %q", pkg.PkgPath)
		}
		if _, _, ok := v.resolveSymbol(spkg("util"), "Half"); !ok {
			t.Error("Half not resolvable in the new package")
		}
		return nil
	})
}

func TestPlacementPolicy(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		if err := tx.CreateSymbol(spkg("use"), "use.go", "const answer = 42"); err != nil {
			return err
		}
		if err := tx.CreateSymbol(spkg("use"), "use.go", "type helper struct{}"); err != nil {
			return err
		}
		return tx.CreateSymbol(spkg("use"), "use.go", "func (helper) run() {}")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("use/use.go")
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
			// helper's method must sit with its receiver, before the
			// pre-existing plain functions region is fine either way — but
			// the type region must precede existing funcs' end, so just
			// assert the method directly follows its receiver group.
			between := src[typePos:methodPos]
			if strings.Contains(between, "func NewCircle") || strings.Contains(between, "func UseArea") {
				t.Errorf("method separated from its receiver:\n%s", src)
			}
		}
		return nil
	})
}

func TestImportSelfRepair(t *testing.T) {
	e := sandboxEngine(t)
	// The exact sequence that failed live: create a package and reference it
	// from another package in the same session, before anything is flushed.
	report := mustEdit(t, e, func(tx *Tx) error {
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
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("use/use.go")
		if !bytes.Contains(file.Src(), []byte(`"example.com/sandbox/colors"`)) {
			t.Errorf("import not spliced:\n%s", file.Src())
		}
		return nil
	})
}

func TestImportRepairRefusesAmbiguity(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
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
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("use/use.go")
		if bytes.Contains(file.Src(), []byte("/dup")) {
			t.Errorf("repair guessed between ambiguous packages:\n%s", file.Src())
		}
		return nil
	})
}

func TestFlushWritesAndUnlinks(t *testing.T) {
	root := copySandbox(t)
	e := NewEngine(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	mustEdit(t, e, func(tx *Tx) error {
		if err := tx.CreateSymbol(spkg("shapes"), "extra.go", "func Twice(x float64) float64 { return 2 * x }"); err != nil {
			return err
		}
		return tx.DeleteFile(spkg("broken"), "broken.go")
	})

	written, removed, err := e.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if !slices.Contains(written, address.RelativePath("shapes/extra.go")) {
		t.Errorf("Flush written = %v, missing extra.go", written)
	}
	if !slices.Contains(removed, address.RelativePath("broken/broken.go")) {
		t.Errorf("Flush removed = %v, missing broken.go", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes", "extra.go")); err != nil {
		t.Errorf("extra.go not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "broken", "broken.go")); !os.IsNotExist(err) {
		t.Errorf("broken.go still on disk: %v", err)
	}
}

func TestReplaceGroupedSpec(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.EditSymbol(spkg("shapes"), "DefaultScale", "// DefaultScale stretches everything.\nDefaultScale = 99.0")
	})
	if len(report.Delta) != 0 {
		t.Errorf("spec replacement introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		ds, _, _ := v.resolveSymbol(spkg("shapes"), "DefaultScale")
		if spec, _ := v.specSource(ds); !bytes.Contains(spec, []byte("= 99.0")) {
			t.Errorf("spec not replaced: %q", spec)
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "debugMode"); !ok {
			t.Error("sibling spec destroyed by grouped replacement")
		}
		return nil
	})
}

func TestDeleteGroupedSpec(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "Scalar")
	})
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Scalar"); ok {
			t.Error("Scalar still resolvable")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Pair"); !ok {
			t.Error("sibling spec destroyed by grouped deletion")
		}
		return nil
	})
	// Deleting the last member removes the whole (now empty) group decl.
	mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "Pair")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/groups.go")
		if bytes.Contains(file.Src(), []byte("type (")) {
			t.Errorf("empty type group left behind:\n%s", file.Src())
		}
		return nil
	})
}

func TestDeleteTrimsMultiNameSpec(t *testing.T) {
	// var minX, maxX = -10.0, 10.0 — one value per name: the targeted
	// name (and its paired value) is trimmed from the spec, not refused.
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "minX")
	})
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "minX"); ok {
			t.Error("minX still resolvable after delete")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "maxX"); !ok {
			t.Error("maxX destroyed by trimming its sibling minX")
		}
		file, _, _ := v.resolveFile("shapes/groups.go")
		if !bytes.Contains(file.Src(), []byte("var maxX = 10.0")) {
			t.Errorf("maxX not trimmed to a standalone spec:\n%s", file.Src())
		}
		return nil
	})
}

func TestRenameMethodReportsBrokenSatisfaction(t *testing.T) {
	e := sandboxEngine(t)
	// Renaming only Circle's method is exact for the object and its uses,
	// but Circle stops satisfying Shape — the documented v1 semantics say
	// that breakage arrives in the echo, not silently.
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.renameSymbol(spkg("shapes"), "Circle.Area", "Extent")
	})
	if !slices.ContainsFunc(deltaStrings(report), func(s string) bool {
		return strings.Contains(s, "does not implement") || strings.Contains(s, "missing method Area")
	}) {
		t.Errorf("broken interface satisfaction missing from delta: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("use/use.go")
		if !bytes.Contains(file.Src(), []byte("c.Extent()")) {
			t.Errorf("direct method call not renamed:\n%s", file.Src())
		}
		return nil
	})
}

func TestMoveFileAndFlush(t *testing.T) {
	root := copySandbox(t)
	e := NewEngine(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveFile(spkg("shapes"), "groups.go", "", "extras.go")
	})
	if len(report.Delta) != 0 {
		t.Errorf("file move introduced diagnostics: %v", deltaStrings(report))
	}
	for _, want := range []address.RelativePath{"shapes/groups.go", "shapes/extras.go"} {
		if !slices.Contains(report.Changed, want) {
			t.Errorf("Changed = %v, missing %s", report.Changed, want)
		}
	}
	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes", "extras.go")); err != nil {
		t.Errorf("moved file not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes", "groups.go")); !os.IsNotExist(err) {
		t.Errorf("old path still on disk: %v", err)
	}
}

func TestMoveSymbol(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "NotShape", "", "groups.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("move introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		sym, _, ok := v.resolveSymbol(spkg("shapes"), "NotShape")
		if !ok {
			t.Fatal("NotShape lost by move")
		}
		if sym.File != "shapes/groups.go" {
			t.Errorf("NotShape lives in %q, want shapes/groups.go", sym.File)
		}
		return nil
	})
	// A method moves too: without its receiver anchor in the destination it
	// falls to the bottom, and interface satisfaction stays intact.
	report = mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "groups.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("method move introduced diagnostics: %v", deltaStrings(report))
	}
}

func TestMoveGroupedSpec(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "DefaultScale", "", "shapes.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("grouped move introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		sym, _, ok := v.resolveSymbol(spkg("shapes"), "DefaultScale")
		if !ok {
			t.Fatal("DefaultScale lost by move")
		}
		if sym.File != "shapes/shapes.go" {
			t.Errorf("DefaultScale lives in %q, want shapes/shapes.go", sym.File)
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "debugMode"); !ok {
			t.Error("sibling spec destroyed by grouped move")
		}
		file, _, _ := v.resolveFile("shapes/shapes.go")
		if !bytes.Contains(file.Src(), []byte("var DefaultScale")) {
			t.Errorf("grouped member not extracted as standalone declaration:\n%s", file.Src())
		}
		return nil
	})
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
		_, err := e.Edit(context.Background(), func(tx *Tx) error {
			return tx.MoveSymbol(spkg("shapes"), tc.key, "", tc.file, "")
		})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("MoveSymbol(%q, %q) error = %v, want it to contain %q", tc.key, tc.file, err, tc.want)
		}
	}
}

func TestMoveToNewFile(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "extra.go", "// Doubled reports twice the default scale.\nfunc Doubled() float64 { return 2 * DefaultScale }")
	})
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Doubled", "", "moved.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("move introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		sym, _, ok := v.resolveSymbol(spkg("shapes"), "Doubled")
		if !ok {
			t.Fatal("Doubled lost by move")
		}
		if sym.File != "shapes/moved.go" {
			t.Errorf("Doubled lives in %q, want shapes/moved.go", sym.File)
		}
		file, _, _ := v.resolveFile("shapes/moved.go")
		if !bytes.Contains(file.Src(), []byte("// Doubled reports twice the default scale.")) {
			t.Errorf("doc comment did not travel with the move:\n%s", file.Src())
		}
		return nil
	})
}

func TestReloadDiscards(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		if err := tx.CreateSymbol(spkg("shapes"), "extra.go", "func Extra() {}"); err != nil {
			return err
		}
		return tx.DeleteFile(spkg("use"), "alias.go")
	})
	discarded, err := e.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	for _, want := range []address.RelativePath{"shapes/extra.go", "use/alias.go"} {
		if !slices.Contains(discarded, want) {
			t.Errorf("discarded missing %q: %v", want, discarded)
		}
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Extra"); ok {
			t.Error("unflushed symbol survived reload")
		}
		if _, _, ok := v.resolveFile("use/alias.go"); !ok {
			t.Error("unflushed deletion survived reload: alias.go missing")
		}
		return nil
	})
}

// TestEditDeltaExcludesPreexistingBrokenness confirms an edit's echo stays
// silent about diagnostics that existed before it and still exist after —
// including on the very first edit against an already-broken workspace.
// The sandbox's permanently type-broken package must show up in neither
// Delta nor Resolved, and must be counted in Unrelated instead.
func TestEditDeltaExcludesPreexistingBrokenness(t *testing.T) {
	e := sandboxEngine(t)

	var brokenCount int
	e.Read(func(v *View) error {
		brokenCount = len(v.Diagnostics(spkg("broken")))
		return nil
	})
	if brokenCount == 0 {
		t.Fatal("sandbox's broken package must carry at least one diagnostic for this test to mean anything")
	}

	report := mustEdit(t, e, func(tx *Tx) error {
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

	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateFile(spkg("shapes"), "extra_doc.go", "Extra holds throwaway fixtures for this test.")
	})
	e.Read(func(v *View) error {
		pkg, _ := v.Package(spkg("shapes"))
		for _, f := range pkg.Files() {
			if f.Path().Base() == "extra_doc.go" && f.Doc() != "Extra holds throwaway fixtures for this test." {
				t.Errorf("new file's doc = %q", f.Doc())
			}
		}
		return nil
	})

	// EditFile replaces the doc without touching the rest of the file.
	mustEdit(t, e, func(tx *Tx) error {
		return tx.EditFile(spkg("shapes"), "extra_doc.go", "Replaced doc.")
	})
	e.Read(func(v *View) error {
		pkg, _ := v.Package(spkg("shapes"))
		for _, f := range pkg.Files() {
			if f.Path().Base() == "extra_doc.go" && f.Doc() != "Replaced doc." {
				t.Errorf("edited file's doc = %q, want %q", f.Doc(), "Replaced doc.")
			}
		}
		return nil
	})

	// Clearing removes the doc entirely.
	mustEdit(t, e, func(tx *Tx) error {
		return tx.EditFile(spkg("shapes"), "extra_doc.go", "")
	})
	e.Read(func(v *View) error {
		pkg, _ := v.Package(spkg("shapes"))
		for _, f := range pkg.Files() {
			if f.Path().Base() == "extra_doc.go" && f.Doc() != "" {
				t.Errorf("cleared file still has doc: %q", f.Doc())
			}
		}
		return nil
	})

	// EditFile on an existing doc must only touch the doc comment.
	mustEdit(t, e, func(tx *Tx) error {
		return tx.EditFile(spkg("shapes"), "shapes.go", "New shapes doc.")
	})
	e.Read(func(v *View) error {
		sym, _, ok := v.resolveSymbol(spkg("shapes"), "Shape")
		if !ok {
			t.Fatal("Shape symbol lost after EditFile")
		}
		if src, _ := v.declSource(sym); !bytes.Contains(src, []byte("Shape is anything with an area")) {
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

	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.EditFile(spkg("shapes"), "nope.go", "x")
	}); err == nil {
		t.Error("editing a missing file's doc must error")
	}
}

func TestMoveSymbolRenameQualification(t *testing.T) {
	e := sandboxEngine(t)

	// Non-method: bare newSymbolKey renames; a qualified form is refused
	// since there is no receiver to qualify it with.
	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "NotShape", "", "", "Circle.NotShape")
	}); err == nil || !strings.Contains(err.Error(), "not a method") {
		t.Errorf("qualified newSymbolKey on a non-method must be refused, got %v", err)
	}
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "NotShape", "", "", "AlsoNotShape")
	})
	if len(report.Delta) != 0 {
		t.Errorf("non-method rename introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "AlsoNotShape"); !ok {
			t.Error("AlsoNotShape not resolvable after rename")
		}
		return nil
	})

	// Method: newSymbolKey must be qualified, and Recv must match exactly —
	// it can never actually change through a rename. The rename itself may
	// legitimately break interface satisfaction (TestRenameMethodReports
	// BrokenSatisfaction already covers that guarantee); this test only
	// cares that the qualification/Recv-matching logic behaves.
	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "", "Extent")
	}); err == nil || !strings.Contains(err.Error(), "must be") {
		t.Errorf("unqualified newSymbolKey on a method must be refused, got %v", err)
	}
	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "", "Square.Extent")
	}); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Errorf("mismatched receiver must be refused, got %v", err)
	}
	mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "", "", "Circle.Extent")
	})
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "Circle.Extent"); !ok {
			t.Error("Circle.Extent not resolvable after rename")
		}
		return nil
	})
}

func TestRenameUpdatesLeadingDoc(t *testing.T) {
	e := sandboxEngine(t)

	// A symbol doc that follows Go's name-first convention is updated.
	mustEdit(t, e, func(tx *Tx) error {
		return tx.renameSymbol(spkg("shapes"), "Named", "Nameable")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/shapes.go")
		if !bytes.Contains(file.Src(), []byte("// Nameable is a second interface")) {
			t.Errorf("doc comment not updated:\n%s", file.Src())
		}
		if bytes.Contains(file.Src(), []byte("// Named is a second interface")) {
			t.Errorf("old doc comment still present:\n%s", file.Src())
		}
		return nil
	})

	// A doc that doesn't start with the symbol's own name is left alone —
	// the safety boundary against corrupting unrelated prose.
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "extra.go",
			"// This helper returns something related to Foo.\nfunc Foo() int { return 0 }")
	})
	mustEdit(t, e, func(tx *Tx) error {
		return tx.renameSymbol(spkg("shapes"), "Foo", "Bar")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/extra.go")
		if !bytes.Contains(file.Src(), []byte("// This helper returns something related to Foo.")) {
			t.Errorf("non-conforming doc was touched:\n%s", file.Src())
		}
		return nil
	})
}

func TestMoveSoloGroupedSpecPreservesDoc(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "solo.go", "const (\n\t// Solo is the only member of its group.\n\tSolo = 1\n)")
	})
	mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "Solo", "", "solo2.go", "")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/solo2.go")
		t.Logf("moved file source:\n%s", file.Src())
		if !bytes.Contains(file.Src(), []byte("Solo is the only member")) {
			t.Errorf("doc comment lost when moving a solo grouped spec:\n%s", file.Src())
		}
		return nil
	})
}

func TestRenameSoloGroupedSpecUpdatesDoc(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "solo.go", "const (\n\t// Solo is the only member of its group.\n\tSolo = 1\n)")
	})
	mustEdit(t, e, func(tx *Tx) error {
		return tx.renameSymbol(spkg("shapes"), "Solo", "Alone")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/solo.go")
		t.Logf("renamed file source:\n%s", file.Src())
		if !bytes.Contains(file.Src(), []byte("// Alone is the only member")) {
			t.Errorf("solo grouped spec's doc not updated:\n%s", file.Src())
		}
		return nil
	})
}

func TestMoveSymbolRenamesIotaGroupMember(t *testing.T) {
	e := sandboxEngine(t)

	// A non-anchor member renames cleanly.
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "KindSquare", "", "", "KindQuad")
	})
	if len(report.Delta) != 0 {
		t.Errorf("renaming a non-anchor iota member introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "KindQuad"); !ok {
			t.Error("KindQuad not resolvable after rename")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "KindSquare"); ok {
			t.Error("old name KindSquare still resolvable")
		}
		return nil
	})

	// The anchor — the member carrying the explicit iota expression —
	// renames the same way: renaming never touches position, so there's
	// nothing special about being the anchor. Its sibling is untouched.
	report = mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "KindCircle", "", "", "KindRound")
	})
	if len(report.Delta) != 0 {
		t.Errorf("renaming the iota group's anchor introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "KindRound"); !ok {
			t.Error("KindRound not resolvable after rename")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "KindQuad"); !ok {
			t.Error("KindQuad lost when renaming the anchor")
		}
		file, _, _ := v.resolveFile("shapes/groups.go")
		if !bytes.Contains(file.Src(), []byte("// KindRound is the round one.")) {
			t.Errorf("anchor's own leading doc not updated:\n%s", file.Src())
		}
		return nil
	})
}

func TestMoveWholeIotaGroup(t *testing.T) {
	e := sandboxEngine(t)
	// Name the non-anchor member — the whole group must still move,
	// including the anchor, which was never named.
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "KindSquare", "", "kinds.go", "")
	})
	if len(report.Delta) != 0 {
		t.Errorf("moving an iota group introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		for _, key := range []string{"KindCircle", "KindSquare"} {
			sym, _, ok := v.resolveSymbol(spkg("shapes"), key)
			if !ok {
				t.Fatalf("%s lost by group move", key)
			}
			if sym.File != "shapes/kinds.go" {
				t.Errorf("%s lives in %q, want shapes/kinds.go (only KindSquare was named)", key, sym.File)
			}
		}
		file, _, _ := v.resolveFile("shapes/kinds.go")
		if !bytes.Contains(file.Src(), []byte("KindCircle Kind = iota")) || !bytes.Contains(file.Src(), []byte("KindSquare")) {
			t.Errorf("group not moved intact:\n%s", file.Src())
		}
		old, _, _ := v.resolveFile("shapes/groups.go")
		if bytes.Contains(old.Src(), []byte("KindCircle")) || bytes.Contains(old.Src(), []byte("KindSquare")) {
			t.Errorf("old file still has group members:\n%s", old.Src())
		}
		return nil
	})
}

func TestDeleteWholeIotaGroup(t *testing.T) {
	e := sandboxEngine(t)
	// Deleting the non-anchor member removes the whole group, not just
	// that one spec — a position-dependent group's members can't be
	// safely separated by deletion alone.
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "KindSquare")
	})
	if len(report.Delta) != 0 {
		t.Errorf("deleting an iota group introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "KindCircle"); ok {
			t.Error("KindCircle survived deleting its sibling KindSquare")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "KindSquare"); ok {
			t.Error("KindSquare still resolvable after delete")
		}
		file, _, _ := v.resolveFile("shapes/groups.go")
		if bytes.Contains(file.Src(), []byte("KindCircle")) || bytes.Contains(file.Src(), []byte("KindSquare")) {
			t.Errorf("group not fully removed:\n%s", file.Src())
		}
		return nil
	})
}

func TestEditIotaGroupWholeReplacement(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.EditSymbol(spkg("shapes"), "KindSquare",
			"// KindCircle is the round one.\nKindCircle Kind = iota\nKindSquare\nKindTriangle")
	})
	if len(report.Delta) != 0 {
		t.Errorf("whole-group edit introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		for _, key := range []string{"KindCircle", "KindSquare", "KindTriangle"} {
			if _, _, ok := v.resolveSymbol(spkg("shapes"), key); !ok {
				t.Errorf("%s missing after whole-group edit", key)
			}
		}
		return nil
	})
}

func TestEditIotaGroupPartialSubmissionDropsSiblings(t *testing.T) {
	e := sandboxEngine(t)
	// A partial submission is accepted as given — silently dropping the
	// sibling that wasn't mentioned, exactly as documented.
	mustEdit(t, e, func(tx *Tx) error {
		return tx.EditSymbol(spkg("shapes"), "KindCircle", "// KindCircle is the round one.\nKindCircle Kind = iota")
	})
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "KindCircle"); !ok {
			t.Error("KindCircle lost by its own whole-group edit")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "KindSquare"); ok {
			t.Error("KindSquare survived a replacement that didn't mention it")
		}
		return nil
	})
}

func TestEditIotaGroupRefusesRenamingTargetedKey(t *testing.T) {
	e := sandboxEngine(t)
	_, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.EditSymbol(spkg("shapes"), "KindCircle",
			"// KindRound is the round one.\nKindRound Kind = iota\nKindSquare")
	})
	if err == nil || !strings.Contains(err.Error(), "move_symbol") {
		t.Errorf("renaming the targeted key via edit_symbol must be refused with a move_symbol pointer, got %v", err)
	}
}

func TestEditGroupRefusesIntroducingIota(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "consts.go", "const (\n\tFoo = 1\n\tBar = 2\n)")
	})
	_, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.EditSymbol(spkg("shapes"), "Foo", "Foo = iota")
	})
	if err == nil || !strings.Contains(err.Error(), "introduce iota") {
		t.Errorf("introducing iota into a plain group must be refused, got %v", err)
	}
}

func TestCreatePlainConstMergesIntoExistingGroup(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "consts2.go", "const (\n\tFoo = 1\n\tBar = 2\n)")
	})
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "consts2.go", "const Baz = 3")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/consts2.go")
		src := string(file.Src())
		if strings.Count(src, "const") != 1 {
			t.Errorf("expected exactly one const group after merge, got source:\n%s", src)
		}
		for _, key := range []string{"Foo", "Bar", "Baz"} {
			if _, _, ok := v.resolveSymbol(spkg("shapes"), key); !ok {
				t.Errorf("%s missing after merge", key)
			}
		}
		return nil
	})
}

func TestCreateIotaGroupNeverMergesIntoExistingGroup(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "kinds2.go", "const (\n\tFirstA = iota\n\tSecondA\n)")
	})
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "kinds2.go", "const (\n\tFirstB = iota\n\tSecondB\n)")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/kinds2.go")
		src := string(file.Src())
		if strings.Count(src, "const") != 2 {
			t.Errorf("expected two separate iota groups, got source:\n%s", src)
		}
		for _, key := range []string{"FirstA", "SecondA", "FirstB", "SecondB"} {
			if _, _, ok := v.resolveSymbol(spkg("shapes"), key); !ok {
				t.Errorf("%s missing", key)
			}
		}
		return nil
	})
}

func TestCreateTypedIotaGroupNearItsType(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "status.go", "// Status is a lifecycle state.\ntype Status int")
	})
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "status.go",
			"const (\n\t// Active is running.\n\tActive Status = iota\n\tInactive\n)")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/status.go")
		src := string(file.Src())
		typeIdx := strings.Index(src, "type Status int")
		constIdx := strings.Index(src, "Active Status = iota")
		if typeIdx == -1 || constIdx == -1 {
			t.Fatalf("declarations missing:\n%s", src)
		}
		if constIdx < typeIdx {
			t.Errorf("iota group not placed after its type declaration:\n%s", src)
		}
		return nil
	})
}

func TestCreateUntypedIotaGroupStandardRegion(t *testing.T) {
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "flags.go", "// Flag is a marker type.\ntype Flag int")
	})
	mustEdit(t, e, func(tx *Tx) error {
		return tx.CreateSymbol(spkg("shapes"), "flags.go", "const (\n\tFlagA = 1 << iota\n\tFlagB\n)")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.resolveFile("shapes/flags.go")
		src := string(file.Src())
		typeIdx := strings.Index(src, "type Flag int")
		constIdx := strings.Index(src, "FlagA = 1 << iota")
		if typeIdx == -1 || constIdx == -1 {
			t.Fatalf("declarations missing:\n%s", src)
		}
		if constIdx > typeIdx {
			t.Errorf("untyped iota group should land in the standard const/var region, before the type:\n%s", src)
		}
		return nil
	})
}

func TestDeleteBlanksSharedMultiValueSpec(t *testing.T) {
	// var boundX, boundY = boundsOf() — one shared call: the call's
	// arity is fixed, so the targeted name blanks to `_` instead of
	// shrinking the list.
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "boundX")
	})
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "boundX"); ok {
			t.Error("boundX still resolvable after delete")
		}
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "boundY"); !ok {
			t.Error("boundY destroyed by blanking its sibling boundX")
		}
		file, _, _ := v.resolveFile("shapes/groups.go")
		if !bytes.Contains(file.Src(), []byte("var _, boundY = boundsOf()")) {
			t.Errorf("boundX not blanked to _:\n%s", file.Src())
		}
		return nil
	})
}

func TestDeleteConvergesToFullRemovalWhenNoRealNameRemains(t *testing.T) {
	// Deleting every real name out of a shared multi-value spec collapses
	// the whole statement, call included — same as deleting a solo name,
	// since nothing is bound to it anymore.
	e := sandboxEngine(t)
	mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "boundX")
	})
	mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "boundY")
	})
	e.Read(func(v *View) error {
		if _, _, ok := v.resolveSymbol(spkg("shapes"), "boundY"); ok {
			t.Error("boundY still resolvable after its spec should have collapsed")
		}
		file, _, _ := v.resolveFile("shapes/groups.go")
		if bytes.Contains(file.Src(), []byte("= boundsOf()")) {
			t.Errorf("shared-call spec not fully collapsed after its last real name was deleted:\n%s", file.Src())
		}
		return nil
	})
}

func TestDeleteSymbolNoopIfAbsent(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
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
	mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "KindSquare")
	})
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "KindCircle")
	})
	if len(report.Changed) != 0 || len(report.Delta) != 0 {
		t.Errorf("deleting an already-collapsed group member must be a noop, got %+v", report)
	}
}

func TestDeleteFileNoopIfAbsent(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
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
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.DeletePackage(spkg("nosuchpkg"))
	})
	if len(report.Changed) != 0 {
		t.Errorf("deleting a nonexistent package must be a noop, got %+v", report)
	}
}
