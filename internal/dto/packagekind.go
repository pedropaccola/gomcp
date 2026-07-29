package dto

// PackageKind classifies what a package is: dto's own copy of
// workspace.PackageKind, since Package (this package's own read-only
// DTO) must not reference a workspace type once dto becomes a leaf
// package workspace itself depends on.
type PackageKind int

const (
	KindProd PackageKind = iota
	KindXTest
	KindExternal
)

var packageKindNames = [...]string{"prod", "xtest", "external"}

// String returns k's lowercase name.
func (k PackageKind) String() string {
	return packageKindNames[k]
}
