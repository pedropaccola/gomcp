package workspace

import (
	"cmp"
	"go/token"
	"slices"
)

// offsetSpan converts a position range into a ByteRange in file's Src.
// Valid because Ast is by invariant a parse of exactly Src.
func (w *Workspace) offsetSpan(owner *Package, file *File, from, to token.Pos) (ByteRange, bool) {
	if !from.IsValid() || !to.IsValid() {
		return ByteRange{}, false
	}
	fset := w.FsetOf(owner)
	r := ByteRange{Start: ByteOffset(fset.Position(from).Offset), End: ByteOffset(fset.Position(to).Offset)}
	if !r.IsValid(file.Src()) {
		return ByteRange{}, false
	}
	return r, true
}

// NewSpliceAtOffset validates a pre-resolved ByteRange against pkg's path
// and returns the ByteSplice replacing it with repl — ok=false when the
// range isn't valid for the file's current Src. The one place a byte
// range is checked before becoming an edit: NewSpliceAtPos funnels
// through this once it has resolved a token.Pos range to a ByteRange, and
// a caller that already holds a resolved offset (an insertion point from
// InsertOffset, an extraction range from ExtractDeclaration) uses it
// directly instead of hand-building a ByteSplice.
func (w *Workspace) NewSpliceAtOffset(pkg *Package, path FilePath, r ByteRange, repl []byte) (ByteSplice, bool) {
	file, ok := pkg.File(path)
	if !ok {
		return ByteSplice{}, false
	}
	if !r.IsValid(file.Src()) {
		return ByteSplice{}, false
	}
	return ByteSplice{Path: path, ByteRange: r, Repl: repl}, true
}

// NewSpliceAtPos narrows a token.Pos range within pkg's path into a
// ByteSplice replacing that range with repl, resolving it through
// NewSpliceAtOffset — ok=false when the range doesn't resolve to a valid,
// in-bounds byte range.
func (w *Workspace) NewSpliceAtPos(pkg *Package, path FilePath, from, to token.Pos, repl []byte) (ByteSplice, bool) {
	file, ok := pkg.File(path)
	if !ok {
		return ByteSplice{}, false
	}
	r, ok := w.offsetSpan(pkg, file, from, to)
	if !ok {
		return ByteSplice{}, false
	}
	return w.NewSpliceAtOffset(pkg, path, r, repl)
}

// ByteOffset is a byte position into a file's canonical Src.
type ByteOffset int

// ToByteRange is the zero-width ByteRange at this offset — the shape a pure
// insertion point takes to become a ByteSplice.
func (o ByteOffset) ToByteRange() ByteRange { return ByteRange{Start: o, End: o} }

// ByteRange is a byte-offset range [Start, End) into a file's canonical Src.
type ByteRange struct{ Start, End ByteOffset }

// IsValid reports whether r is a well-formed, in-bounds range for src.
func (r ByteRange) IsValid(src []byte) bool {
	return r.Start >= 0 && r.End <= ByteOffset(len(src)) && r.Start <= r.End
}

// Slice returns the bytes r addresses within src.
func (r ByteRange) Slice(src []byte) []byte { return src[r.Start:r.End] }

// ByteSplice is one byte-span edit against a file's canonical Src: replace
// Range with Repl. A Value Object — safe to return across the Aggregate
// boundary, unlike the Symbol/Package/File pointers the analysis that
// produces one is computed from.
type ByteSplice struct {
	Path FilePath
	ByteRange
	Repl []byte
}

// ByteSplices is a batch of ByteSplice edits against the same file's Src.
type ByteSplices []ByteSplice

// Apply applies every splice in ss to src in descending offset order so
// earlier spans stay valid.
func (ss ByteSplices) Apply(src []byte) []byte {
	slices.SortFunc(ss, func(a, b ByteSplice) int { return cmp.Compare(b.Start, a.Start) })
	out := slices.Clone(src)
	for _, s := range ss {
		out = slices.Concat(out[:s.Start], s.Repl, out[s.End:])
	}
	return out
}
