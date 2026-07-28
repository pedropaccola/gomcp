package dto

import "github.com/pedropaccola/gomcp/internal/address"

// File is dto's read-only view of one file's presentation-relevant
// facts: its path and its own package-doc comment, constructed directly
// by workspace so it carries no pointer into the live model.
type File struct {
	Path address.FilePath
	Doc  string
}
