package disk

import "testing"

func TestSplitPos(t *testing.T) {
	cases := []struct {
		pos       string
		file      string
		line, col int
		ok        bool
	}{
		{"", "", 0, 0, false},
		{"-", "", 0, 0, false},
		{"/a/b.go:12:3", "/a/b.go", 12, 3, true},
		{"/a/b.go:12", "/a/b.go", 12, 0, true},
		{"/a/b.go", "/a/b.go", 0, 0, true},
	}
	for _, c := range cases {
		file, line, col, ok := splitPos(c.pos)
		if file != c.file || line != c.line || col != c.col || ok != c.ok {
			t.Errorf("splitPos(%q) = (%q,%d,%d,%v), want (%q,%d,%d,%v)",
				c.pos, file, line, col, ok, c.file, c.line, c.col, c.ok)
		}
	}
}
