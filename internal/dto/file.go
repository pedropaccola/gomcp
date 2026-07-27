package dto

import "github.com/pedropaccola/gomcp/internal/address"

// File is dto's read-only view of one file's presentation-relevant
// facts: its path and its own package-doc comment, constructed directly
// by workspace so it carries no pointer into the live model.
type File struct {
	path address.FilePath
	doc  string
}

// Doc is the file's own package-doc comment text, or "" when it has none.
func (f File) Doc() string { return f.doc }

// Path is the file's workspace-relative address.
func (f File) Path() address.FilePath { return f.path }

// NewFile constructs a File from its plain fields.
func NewFile(path address.FilePath, doc string) File {
	return File{path: path, doc: doc}
}
