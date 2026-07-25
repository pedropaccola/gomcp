package dto

var symbolKindNames = [...]string{"func", "method", "type", "var", "const"}

const (
	KindFunc SymbolKind = iota
	KindMethod
	KindType
	KindVar
	KindConst
)

// SymbolKind classifies a top-level declaration.
type SymbolKind int

func (k SymbolKind) String() string {
	if k >= 0 && int(k) < len(symbolKindNames) {
		return symbolKindNames[k]
	}
	return "unknown"
}
