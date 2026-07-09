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
	if !slices.Contains(report.Changed, RelativePath("shapes/extra.go")) {
		t.Errorf("Changed missing the new file: %v", report.Changed)
	}
	e.Read(func(v *View) error {
		sym, _, ok := v.Symbol(spkg("shapes"), "Twice")
		if !ok {
			t.Fatal("Twice not resolvable after commit")
		}
		if src, _ := v.DeclSource(sym); !bytes.Contains(src, []byte("Twice doubles")) {
			t.Error("doc comment lost through the pipeline")
		}
		file, _, _ := v.File("shapes/extra.go")
		if file == nil || !file.IsDirty {
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
		if _, _, ok := v.Symbol(spkg("shapes"), "Thrice"); ok {
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
		return tx.ReplaceSymbol(spkg("shapes"), "Circle", "type Circle struct{ Radius float64 }")
	})
	delta := deltaStrings(report)
	if !slices.ContainsFunc(delta, func(s string) bool {
		return strings.Contains(s, "use/use.go") && strings.HasPrefix(s, "[type]")
	}) {
		t.Errorf("renaming Circle's field must break use/use.go in the delta: %v", delta)
	}
	if slices.ContainsFunc(delta, func(s string) bool { return strings.HasPrefix(s, "[list]") }) {
		t.Errorf("relayed go list compiler output must be filtered: %v", delta)
	}

	// Healing: revert in a second Tx. The file is already dirty, so Changed
	// must still report it (touched-by-this-Tx, not newly-dirty), the old
	// breakage must show as Resolved, and nothing new may appear.
	heal := mustEdit(t, e, func(tx *Tx) error {
		return tx.ReplaceSymbol(spkg("shapes"), "Circle", "type Circle struct{ R float64 }")
	})
	if !slices.Contains(heal.Changed, RelativePath("shapes/shapes.go")) {
		t.Errorf("consecutive edit to a dirty file must still report it: %v", heal.Changed)
	}
	if len(heal.Delta) != 0 {
		t.Errorf("healing edit introduced diagnostics: %v", deltaStrings(heal))
	}
	if len(heal.Resolved) == 0 {
		t.Error("healing edit must report the diagnostics it resolved")
	}
	for _, diag := range heal.Resolved {
		if !slices.Contains(delta, diag.String()) {
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
		if _, _, ok := v.Symbol(spkg("shapes"), "Base"); ok {
			t.Error("Base still resolvable after delete")
		}
		return nil
	})
}

func TestRenameSymbolPropagates(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.RenameSymbol(spkg("shapes"), "Circle", "Round")
	})
	if len(report.Delta) != 0 {
		t.Errorf("a propagated rename must not introduce diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.Symbol(spkg("shapes"), "Circle"); ok {
			t.Error("old name still resolvable")
		}
		if _, _, ok := v.Symbol(spkg("shapes"), "Round"); !ok {
			t.Error("new name not resolvable")
		}
		if _, _, ok := v.Symbol(spkg("shapes"), "Round.Area"); !ok {
			t.Error("method key did not follow the renamed receiver")
		}
		file, _, _ := v.File("use/use.go")
		if !bytes.Contains(file.Src, []byte("shapes.Round{")) || bytes.Contains(file.Src, []byte("shapes.Circle")) {
			t.Errorf("use/use.go not rewritten:\n%s", file.Src)
		}
		return nil
	})
}

func TestRenamePackagePropagates(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.RenamePackage(spkg("shapes"), spkg("geo"))
	})
	if len(report.Delta) != 0 {
		t.Errorf("a propagated package rename must not introduce diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		if _, ok := v.Package(spkg("shapes")); ok {
			t.Error("old package address still resolvable")
		}
		pkg, ok := v.Package(spkg("geo"))
		if !ok || pkg.Name != "geo" || pkg.PkgPath != "example.com/sandbox/geo" {
			t.Fatalf("geo package wrong after rename: %+v", pkg)
		}
		file, _, _ := v.File("use/use.go")
		if !bytes.Contains(file.Src, []byte(`"example.com/sandbox/geo"`)) {
			t.Errorf("import path not rewritten:\n%s", file.Src)
		}
		if !bytes.Contains(file.Src, []byte("geo.Circle{")) {
			t.Errorf("qualifiers not renamed:\n%s", file.Src)
		}

		// Aliased imports: path rewritten, alias untouched.
		alias, _, _ := v.File("use/alias.go")
		if !bytes.Contains(alias.Src, []byte(`sh "example.com/sandbox/geo"`)) {
			t.Errorf("aliased import path not rewritten:\n%s", alias.Src)
		}
		if !bytes.Contains(alias.Src, []byte("sh.Base{}")) {
			t.Errorf("alias qualifier must survive the rename:\n%s", alias.Src)
		}

		// The external test package moves with its production sibling: new
		// clause name, rewritten self-import, renamed qualifiers.
		xtest, ok := v.XTest(spkg("geo"))
		if !ok || xtest.Name != "geo_test" {
			t.Fatalf("XTest did not follow the rename: %+v", xtest)
		}
		ext, _, _ := v.File("geo/external_test.go")
		if !bytes.Contains(ext.Src, []byte(`"example.com/sandbox/geo"`)) || !bytes.Contains(ext.Src, []byte("geo.Circle{")) {
			t.Errorf("external test not rewritten:\n%s", ext.Src)
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
		pkg, ok := v.Package(spkg("util"))
		if !ok {
			t.Fatal("util package missing after recheck — overlay-only directories not surviving the reload")
		}
		if pkg.PkgPath != "example.com/sandbox/util" {
			t.Errorf("recheck did not resolve the import path: %q", pkg.PkgPath)
		}
		if _, _, ok := v.Symbol(spkg("util"), "Half"); !ok {
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
		file, _, _ := v.File("use/use.go")
		src := string(file.Src)
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
	if !slices.Contains(report.Changed, RelativePath("use/use.go")) {
		t.Errorf("repaired file missing from Changed: %v", report.Changed)
	}
	e.Read(func(v *View) error {
		file, _, _ := v.File("use/use.go")
		if !bytes.Contains(file.Src, []byte(`"example.com/sandbox/colors"`)) {
			t.Errorf("import not spliced:\n%s", file.Src)
		}
		return nil
	})
}

func TestImportRepairRefusesAmbiguity(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		for _, pkg := range []PkgPath{spkg("a/dup"), spkg("b/dup")} {
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
		file, _, _ := v.File("use/use.go")
		if bytes.Contains(file.Src, []byte("/dup")) {
			t.Errorf("repair guessed between ambiguous packages:\n%s", file.Src)
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
	if !slices.Contains(written, RelativePath("shapes/extra.go")) {
		t.Errorf("Flush written = %v, missing extra.go", written)
	}
	if !slices.Contains(removed, RelativePath("broken/broken.go")) {
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
		return tx.ReplaceSymbol(spkg("shapes"), "KindSquare", "KindSquare Kind = 99")
	})
	if len(report.Delta) != 0 {
		t.Errorf("spec replacement introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		sq, _, _ := v.Symbol(spkg("shapes"), "KindSquare")
		if spec, _ := v.SpecSource(sq); !bytes.Contains(spec, []byte("= 99")) {
			t.Errorf("spec not replaced: %q", spec)
		}
		if _, _, ok := v.Symbol(spkg("shapes"), "KindCircle"); !ok {
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
		if _, _, ok := v.Symbol(spkg("shapes"), "Scalar"); ok {
			t.Error("Scalar still resolvable")
		}
		if _, _, ok := v.Symbol(spkg("shapes"), "Pair"); !ok {
			t.Error("sibling spec destroyed by grouped deletion")
		}
		return nil
	})
	// Deleting the last member removes the whole (now empty) group decl.
	mustEdit(t, e, func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "Pair")
	})
	e.Read(func(v *View) error {
		file, _, _ := v.File("shapes/groups.go")
		if bytes.Contains(file.Src, []byte("type (")) {
			t.Errorf("empty type group left behind:\n%s", file.Src)
		}
		return nil
	})
}

func TestDeleteMultiNameSpecRefused(t *testing.T) {
	e := sandboxEngine(t)
	if _, err := e.Edit(context.Background(), func(tx *Tx) error {
		return tx.DeleteSymbol(spkg("shapes"), "minX")
	}); err == nil || !strings.Contains(err.Error(), "declared together") {
		t.Errorf("multi-name spec deletion must refuse with guidance, got %v", err)
	}
}

func TestRenameMethodReportsBrokenSatisfaction(t *testing.T) {
	e := sandboxEngine(t)
	// Renaming only Circle's method is exact for the object and its uses,
	// but Circle stops satisfying Shape — the documented v1 semantics say
	// that breakage arrives in the echo, not silently.
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.RenameSymbol(spkg("shapes"), "Circle.Area", "Extent")
	})
	if !slices.ContainsFunc(deltaStrings(report), func(s string) bool {
		return strings.Contains(s, "does not implement") || strings.Contains(s, "missing method Area")
	}) {
		t.Errorf("broken interface satisfaction missing from delta: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		file, _, _ := v.File("use/use.go")
		if !bytes.Contains(file.Src, []byte("c.Extent()")) {
			t.Errorf("direct method call not renamed:\n%s", file.Src)
		}
		return nil
	})
}

func TestRenameFileAndFlush(t *testing.T) {
	root := copySandbox(t)
	e := NewEngine(root, nil)
	if err := e.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.RenameFile(spkg("shapes"), "groups.go", "extras.go")
	})
	if len(report.Delta) != 0 {
		t.Errorf("file rename introduced diagnostics: %v", deltaStrings(report))
	}
	for _, want := range []RelativePath{"shapes/groups.go", "shapes/extras.go"} {
		if !slices.Contains(report.Changed, want) {
			t.Errorf("Changed = %v, missing %s", report.Changed, want)
		}
	}
	if _, _, err := e.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes", "extras.go")); err != nil {
		t.Errorf("renamed file not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shapes", "groups.go")); !os.IsNotExist(err) {
		t.Errorf("old path still on disk: %v", err)
	}
}

func TestMoveSymbol(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "NotShape", "groups.go")
	})
	if len(report.Delta) != 0 {
		t.Errorf("move introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		sym, _, ok := v.Symbol(spkg("shapes"), "NotShape")
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
		return tx.MoveSymbol(spkg("shapes"), "Circle.Area", "groups.go")
	})
	if len(report.Delta) != 0 {
		t.Errorf("method move introduced diagnostics: %v", deltaStrings(report))
	}
}

func TestMoveGroupedSpec(t *testing.T) {
	e := sandboxEngine(t)
	report := mustEdit(t, e, func(tx *Tx) error {
		return tx.MoveSymbol(spkg("shapes"), "DefaultScale", "shapes.go")
	})
	if len(report.Delta) != 0 {
		t.Errorf("grouped move introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		sym, _, ok := v.Symbol(spkg("shapes"), "DefaultScale")
		if !ok {
			t.Fatal("DefaultScale lost by move")
		}
		if sym.File != "shapes/shapes.go" {
			t.Errorf("DefaultScale lives in %q, want shapes/shapes.go", sym.File)
		}
		if _, _, ok := v.Symbol(spkg("shapes"), "debugMode"); !ok {
			t.Error("sibling spec destroyed by grouped move")
		}
		file, _, _ := v.File("shapes/shapes.go")
		if !bytes.Contains(file.Src, []byte("var DefaultScale")) {
			t.Errorf("grouped member not extracted as standalone declaration:\n%s", file.Src)
		}
		return nil
	})
}

func TestMoveRefusals(t *testing.T) {
	e := sandboxEngine(t)
	cases := []struct {
		key, file, want string
	}{
		{"KindCircle", "shapes.go", "position in a const group"},
		{"minX", "shapes.go", "declared together"},
		{"NotShape", "shapes.go", "already lives"},
		{"Missing", "shapes.go", "no symbol"},
		{"NotShape", "extra_test.go", "test build boundary"},
	}
	for _, tc := range cases {
		_, err := e.Edit(context.Background(), func(tx *Tx) error {
			return tx.MoveSymbol(spkg("shapes"), tc.key, tc.file)
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
		return tx.MoveSymbol(spkg("shapes"), "Doubled", "moved.go")
	})
	if len(report.Delta) != 0 {
		t.Errorf("move introduced diagnostics: %v", deltaStrings(report))
	}
	e.Read(func(v *View) error {
		sym, _, ok := v.Symbol(spkg("shapes"), "Doubled")
		if !ok {
			t.Fatal("Doubled lost by move")
		}
		if sym.File != "shapes/moved.go" {
			t.Errorf("Doubled lives in %q, want shapes/moved.go", sym.File)
		}
		file, _, _ := v.File("shapes/moved.go")
		if !bytes.Contains(file.Src, []byte("// Doubled reports twice the default scale.")) {
			t.Errorf("doc comment did not travel with the move:\n%s", file.Src)
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
	for _, want := range []RelativePath{"shapes/extra.go", "use/alias.go"} {
		if !slices.Contains(discarded, want) {
			t.Errorf("discarded missing %q: %v", want, discarded)
		}
	}
	e.Read(func(v *View) error {
		if _, _, ok := v.Symbol(spkg("shapes"), "Extra"); ok {
			t.Error("unflushed symbol survived reload")
		}
		if _, _, ok := v.File("use/alias.go"); !ok {
			t.Error("unflushed deletion survived reload: alias.go missing")
		}
		return nil
	})
}
