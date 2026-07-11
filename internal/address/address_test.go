package address

import "testing"

func TestCleanPath(t *testing.T) {
	if p, ok := CleanPath("./internal//engine/"); !ok || p != "internal/engine" {
		t.Errorf(`CleanPath("./internal//engine/") = %q, %v`, p, ok)
	}
	if p, ok := CleanPath("main.go"); !ok || p != "main.go" {
		t.Errorf(`CleanPath("main.go") = %q, %v`, p, ok)
	}
	for _, bad := range []string{"/etc/passwd", "..", "../secret.go", "a/../../b"} {
		if p, ok := CleanPath(bad); ok {
			t.Errorf("CleanPath(%q) = %q, accepted an address outside the workspace", bad, p)
		}
	}
}
