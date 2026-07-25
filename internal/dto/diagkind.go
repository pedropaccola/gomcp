package dto

var diagKindNames = [...]string{"unknown", "list", "parse", "type"}

const (
	DiagUnknown DiagKind = iota
	DiagList
	DiagParse
	DiagType
)

// DiagKind classifies a problem report by its source in the load
// pipeline — dto's own copy of workspace.DiagKind, since Diagnostic
// (this package's own read-only DTO) must not reference a workspace
// type once dto becomes a leaf package workspace itself depends on.
type DiagKind int

func (k DiagKind) String() string {
	if k >= 0 && int(k) < len(diagKindNames) {
		return diagKindNames[k]
	}
	return "unknown"
}
