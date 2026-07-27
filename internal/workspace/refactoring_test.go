package workspace

import (
	"strings"
	"testing"
)

func TestValidateNewNameFunc(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	got, err := w.ValidateNewName("test.mod/pkg", "Foo", "Bar")
	if err != nil || got != "Bar" {
		t.Errorf("ValidateNewName(Foo, Bar) = %q, %v, want Bar, nil", got, err)
	}
}

func TestValidateNewNameFuncRejectsDotted(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	if _, err := w.ValidateNewName("test.mod/pkg", "Foo", "Recv.Bar"); err == nil {
		t.Error("ValidateNewName must reject a dotted newKey for a non-method")
	}
}

func TestValidateNewNameMethod(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\ntype Box struct{}\n\nfunc (b Box) M() {}\n")
	got, err := w.ValidateNewName("test.mod/pkg", "Box.M", "Box.N")
	if err != nil || got != "N" {
		t.Errorf("ValidateNewName(Box.M, Box.N) = %q, %v, want N, nil", got, err)
	}
}

func TestValidateNewNameMethodRequiresReceiverQualifier(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\ntype Box struct{}\n\nfunc (b Box) M() {}\n")
	if _, err := w.ValidateNewName("test.mod/pkg", "Box.M", "N"); err == nil {
		t.Error("ValidateNewName must require a receiver-qualified newKey for a method")
	}
}

func TestValidateNewNameMethodRejectsReceiverChange(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\ntype Box struct{}\n\nfunc (b Box) M() {}\n")
	if _, err := w.ValidateNewName("test.mod/pkg", "Box.M", "Other.N"); err == nil {
		t.Error("ValidateNewName must reject changing a method's receiver")
	}
}

func TestValidateNewNameUnknownSymbol(t *testing.T) {
	w := simpleFixture(t, "package pkg\n\nfunc Foo() {}\n")
	if _, err := w.ValidateNewName("test.mod/pkg", "Missing", "Bar"); err == nil {
		t.Error("ValidateNewName must error on an unresolved symbol")
	}
}

func TestDetectMoveConflictsSafeMove(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src":  "package src\n\ntype Box struct{}\n\nfunc (b Box) M() {}\n",
		"dest": "package dest\n",
	})
	if got := w.DetectMoveConflicts("src", "dest", []string{"Box", "Box.M"}); got != nil {
		t.Errorf("DetectMoveConflicts(Box, Box.M) = %v, want none", got)
	}
}

func TestDetectMoveConflictsMethodWithoutReceiver(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src":  "package src\n\ntype Box struct{}\n\nfunc (b Box) M() {}\n",
		"dest": "package dest\n",
	})
	got := w.DetectMoveConflicts("src", "dest", []string{"Box.M"})
	if len(got) != 1 || !strings.Contains(got[0], "Box") {
		t.Errorf("DetectMoveConflicts(Box.M) = %v, want one conflict naming receiver Box", got)
	}
}

func TestDetectMoveConflictsCollision(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src":  "package src\n\nfunc Helper() int { return 1 }\n",
		"dest": "package dest\n\nfunc Helper() int { return 2 }\n",
	})
	got := w.DetectMoveConflicts("src", "dest", []string{"Helper"})
	if len(got) != 1 || !strings.Contains(got[0], "already exists") {
		t.Errorf("DetectMoveConflicts(Helper) = %v, want a collision conflict", got)
	}
}

func TestDetectMoveConflictsDependencyOnUnexportedSibling(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src":  "package src\n\nvar helper = 1\n\nfunc UsesHelper() int { return helper }\n",
		"dest": "package dest\n",
	})
	got := w.DetectMoveConflicts("src", "dest", []string{"UsesHelper"})
	if len(got) != 1 || !strings.Contains(got[0], "unexported") {
		t.Errorf("DetectMoveConflicts(UsesHelper) = %v, want a dependency-on-unexported conflict", got)
	}
}

func TestDetectMoveConflictsBlockingReferrer(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src":  "package src\n\nfunc leaving() int { return 1 }\n\nfunc Stays() int { return leaving() }\n",
		"dest": "package dest\n",
	})
	got := w.DetectMoveConflicts("src", "dest", []string{"leaving"})
	if len(got) != 1 || !strings.Contains(got[0], "still references unexported") {
		t.Errorf("DetectMoveConflicts(leaving) = %v, want a blocking-referrer conflict", got)
	}
}

func TestDetectMoveConflictsSamePackageAlwaysSafe(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src": "package src\n\nfunc leaving() int { return 1 }\n\nfunc Stays() int { return leaving() }\n",
	})
	if got := w.DetectMoveConflicts("src", "src", []string{"leaving"}); got != nil {
		t.Errorf("DetectMoveConflicts within the same package = %v, want none", got)
	}
}

func TestQualifierFixupsInboundRepointsExternalQualifier(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"mvsrc": "package mvsrc\n\nfunc Foo() int { return 1 }\n",
		"dest":  "package dest\n",
		"use":   "package use\n\nimport \"mvsrc\"\n\nfunc Bar() int { return mvsrc.Foo() }\n",
	})
	edits, err := w.ComputeQualifierFixups("mvsrc", "dest", []string{"Foo"})
	if err != nil {
		t.Fatalf("QualifierFixups: %v", err)
	}
	var found bool
	for _, e := range edits {
		if e.Path != "use/file.go" {
			continue
		}
		found = true
		src := []byte("package use\n\nimport \"mvsrc\"\n\nfunc Bar() int { return mvsrc.Foo() }\n")
		got := string(applyTestSplices(src, []Splice{e}))
		if !strings.Contains(got, "dest.Foo()") {
			t.Errorf("applied splice = %q, want it to repoint the qualifier to dest.Foo()", got)
		}
	}
	if !found {
		t.Errorf("QualifierFixups = %+v, want a splice in use/file.go", edits)
	}
}

func TestQualifierFixupsOutboundQualifiesStayingSibling(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"mvsrc": "package mvsrc\n\nfunc Stay() int { return 1 }\n\nfunc Moving() int { return Stay() }\n",
		"dest":  "package dest\n",
	})
	edits, err := w.ComputeQualifierFixups("mvsrc", "dest", []string{"Moving"})
	if err != nil {
		t.Fatalf("QualifierFixups: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("QualifierFixups = %+v, want exactly one outbound splice", edits)
	}
	src := []byte("package mvsrc\n\nfunc Stay() int { return 1 }\n\nfunc Moving() int { return Stay() }\n")
	got := string(applyTestSplices(src, edits))
	if !strings.Contains(got, "mvsrc.Stay()") {
		t.Errorf("applied splice = %q, want Moving's own reference to Stay qualified as mvsrc.Stay()", got)
	}
}

func TestQualifierFixupsErrorsOnMissingPackage(t *testing.T) {
	w := typesFixture(t, map[string]string{"mvsrc": "package mvsrc\n"})
	if _, err := w.ComputeQualifierFixups("mvsrc", "nosuchdest", []string{"Foo"}); err == nil {
		t.Error("QualifierFixups must error when destPkg doesn't exist")
	}
}

func TestRenameSplicesRewritesExternalReferences(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"mvsrc": "package mvsrc\n\nfunc Foo() int { return 1 }\n",
		"use":   "package use\n\nimport \"mvsrc\"\n\nfunc Bar() int { return mvsrc.Foo() }\n",
	})
	edits, err := w.ComputeRenameSplices("mvsrc", "Foo", "Baz")
	if err != nil {
		t.Fatalf("RenameSplices: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("RenameSplices = %+v, want exactly one splice in use/file.go", edits)
	}
	src := []byte("package use\n\nimport \"mvsrc\"\n\nfunc Bar() int { return mvsrc.Foo() }\n")
	got := string(applyTestSplices(src, edits))
	if !strings.Contains(got, "mvsrc.Baz()") {
		t.Errorf("applied splice = %q, want mvsrc.Baz()", got)
	}
}

func TestPackageMoveSplicesRewritesImportAndQualifier(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"mvsrc": "package mvsrc\n\nfunc Foo() int { return 1 }\n",
		"use":   "package use\n\nimport \"mvsrc\"\n\nfunc Bar() int { return mvsrc.Foo() }\n",
	})
	edits := w.ComputePackageMoveSplices("mvsrc", "dest", true, "mvsrc", "dest")
	if len(edits) != 2 {
		t.Fatalf("PackageMoveSplices = %+v, want an import-path splice and a qualifier splice", edits)
	}
	src := []byte("package use\n\nimport \"mvsrc\"\n\nfunc Bar() int { return mvsrc.Foo() }\n")
	got := string(applyTestSplices(src, edits))
	if !strings.Contains(got, `import "dest"`) || !strings.Contains(got, "dest.Foo()") {
		t.Errorf("applied splices = %q, want import path and qualifier both rewritten to dest", got)
	}
}

// TestDetectMoveConflictsCatchesGroupSiblingCollision proves the fix:
// checking DetectMoveConflicts with only the requested key misses a
// collision caused by a position-dependent group's other members, which
// move along silently via ExtractDeclaration — PositionDependentGroupMembers
// closes that gap by feeding DetectMoveConflicts everything that will
// actually move.
func TestDetectMoveConflictsCatchesGroupSiblingCollision(t *testing.T) {
	w := typesFixture(t, map[string]string{
		"src":  "package src\n\nconst (\n\tBase = iota\n\tSibling\n)\n",
		"dest": "package dest\n\nconst Sibling = 42\n",
	})
	if got := w.DetectMoveConflicts("src", "dest", []string{"Base"}); len(got) != 0 {
		t.Fatalf("DetectMoveConflicts(Base alone) = %v, want no conflicts (demonstrating why checking only the named key misses the sibling)", got)
	}
	movingKeys, err := w.PositionDependentGroupMembers("src", "Base")
	if err != nil {
		t.Fatalf("PositionDependentGroupMembers: %v", err)
	}
	got := w.DetectMoveConflicts("src", "dest", movingKeys)
	if len(got) != 1 || !strings.Contains(got[0], "Sibling") || !strings.Contains(got[0], "already exists") {
		t.Errorf("DetectMoveConflicts(%v) = %v, want a collision on Sibling", movingKeys, got)
	}
}
