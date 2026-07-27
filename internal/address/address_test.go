package address

import "testing"

func TestIsOutsideRoot(t *testing.T) {
	for _, bad := range []string{"..", "../secret.go"} {
		if !IsOutsideRoot(bad) {
			t.Errorf("IsOutsideRoot(%q) = false, want true", bad)
		}
	}
	if IsOutsideRoot("internal/engine") {
		t.Errorf(`IsOutsideRoot("internal/engine") = true, want false`)
	}
}

func TestNewPkgPath(t *testing.T) {
	const module = PkgPath("test.mod")
	if p, err := NewPkgPath(module, "./internal//engine/"); err != nil || p != "test.mod/internal/engine" {
		t.Errorf(`NewPkgPath(module, "./internal//engine/") = %q, %v`, p, err)
	}
	if p, err := NewPkgPath(module, "test.mod/internal/engine"); err != nil || p != "test.mod/internal/engine" {
		t.Errorf(`NewPkgPath(module, "test.mod/internal/engine") = %q, %v`, p, err)
	}
	for _, bad := range []string{"/etc/passwd", "..", "../secret", "a/../../b", "main.go"} {
		if p, err := NewPkgPath(module, bad); err == nil {
			t.Errorf("NewPkgPath(module, %q) = %q, accepted an invalid package address", bad, p)
		}
	}
}

func TestNewFilePath(t *testing.T) {
	const module = PkgPath("test.mod")
	const pkg = PkgPath("test.mod/internal/engine")
	if p, err := NewFilePath(module, pkg, "main.go"); err != nil || p != "test.mod/internal/engine/main.go" {
		t.Errorf(`NewFilePath(module, pkg, "main.go") = %q, %v`, p, err)
	}
	if p, err := NewFilePath(module, pkg, "internal/engine/main.go"); err != nil || p != "test.mod/internal/engine/main.go" {
		t.Errorf(`NewFilePath(module, pkg, "internal/engine/main.go") = %q, %v`, p, err)
	}
	for _, bad := range []string{"../secret.go", "other/main.go", "main.txt"} {
		if p, err := NewFilePath(module, pkg, bad); err == nil {
			t.Errorf("NewFilePath(module, pkg, %q) = %q, accepted an invalid file address", bad, p)
		}
	}
}
