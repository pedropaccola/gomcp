package address

import "testing"

func TestIsOutsideRoot(t *testing.T) {
	for _, bad := range []string{"..", "../secret.go"} {
		if !IsOutsideRoot(bad) {
			t.Errorf("IsOutsideRoot(%q) = false, want true", bad)
		}
	}
	if IsOutsideRoot("internal/store") {
		t.Errorf(`IsOutsideRoot("internal/store") = true, want false`)
	}
}

func TestNewPkgPath(t *testing.T) {
	const module = PkgPath("test.mod")
	if p, err := NewPkgPath(module, "./internal//store/"); err != nil || p != "test.mod/internal/store" {
		t.Errorf(`NewPkgPath(module, "./internal//store/") = %q, %v`, p, err)
	}
	if p, err := NewPkgPath(module, "test.mod/internal/store"); err != nil || p != "test.mod/internal/store" {
		t.Errorf(`NewPkgPath(module, "test.mod/internal/store") = %q, %v`, p, err)
	}
	for _, bad := range []string{"/etc/passwd", "..", "../secret", "a/../../b", "main.go"} {
		if p, err := NewPkgPath(module, bad); err == nil {
			t.Errorf("NewPkgPath(module, %q) = %q, accepted an invalid package address", bad, p)
		}
	}
}

func TestNewFilePath(t *testing.T) {
	const module = PkgPath("test.mod")
	const pkg = PkgPath("test.mod/internal/store")
	if p, err := NewFilePath(module, pkg, "main.go"); err != nil || p != "test.mod/internal/store/main.go" {
		t.Errorf(`NewFilePath(module, pkg, "main.go") = %q, %v`, p, err)
	}
	if p, err := NewFilePath(module, pkg, "internal/store/main.go"); err != nil || p != "test.mod/internal/store/main.go" {
		t.Errorf(`NewFilePath(module, pkg, "internal/store/main.go") = %q, %v`, p, err)
	}
	for _, bad := range []string{"../secret.go", "other/main.go", "main.txt"} {
		if p, err := NewFilePath(module, pkg, bad); err == nil {
			t.Errorf("NewFilePath(module, pkg, %q) = %q, accepted an invalid file address", bad, p)
		}
	}
}
