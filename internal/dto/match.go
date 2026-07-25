package dto

// Match is one hit of a workspace-wide scan, dto's own pairing of the
// matching package and symbol.
type Match struct {
	Pkg Package
	Sym Symbol
}
