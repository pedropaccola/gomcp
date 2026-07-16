// Package tools is the presentation layer: it composes engine lookups into
// the MCP tools exposed to the agent and owns every decision about what gets
// printed. The engine returns data; this package returns representations.
//
// This file declares the tool surface — registration and I/O shapes. Read
// handlers live one semantic category per file — enumerators.go, describers.go,
// finders.go, diagnostics.go — with read.go holding the cross-category
// resolution helpers (readPackage, readSymbol, methodSignatures). Write
// handlers mirror that: creators.go, editors.go, refactorings.go, session.go,
// with edit.go holding runEdit, the one relay every mutating handler flows
// through. shared.go holds helpers genuinely called from both sides. Handlers
// themselves carry no doc comments by design — they are mechanical relays,
// documented by their tool descriptions in Register.
// Tool naming convention: list_* enumerate a scope, describe_* render one
// address, search_* scan the workspace, diagnostics reports problems, every
// other prefix mirrors its mutation verb, and flush writes to disk.
//
// Output convention: every reader carries an optional diagnostics block
// (DiagBlock) scoped to exactly what it read. Scoped blocks are views,
// never the inventory — a problem outside the read scope does not appear,
// so an empty block means "nothing wrong here", not "workspace healthy".
// The diagnostics tool is the complete inventory.
package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
)

// reads annotates a read-only tool: the workspace is a closed world, and
// reading it never modifies it.
func reads(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		IdempotentHint:  true,
		DestructiveHint: new(false),
		OpenWorldHint:   new(false),
	}
}

// mutates annotates a mutating tool. Every mutation is idempotent for
// retries by construction: existence preconditions plus the error-means-
// untouched transaction contract guarantee an identical retry either
// reproduces the same state or errors without effect — it can never
// double-apply. destructive follows the semantic sections: Creators cannot
// destroy; Editors, Refactorings, and flush overwrite or remove.
func mutates(title string, destructive bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		IdempotentHint:  true,
		DestructiveHint: new(destructive),
		OpenWorldHint:   new(false),
	}
}

// Register wires every tool into the server.
func Register(server *mcp.Server, eng *engine.Engine) {
	// Enumerators
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_packages",
		Annotations: reads("List Packages"),
		Description: "[Enumerator] List every Go package in the workspace by import path — the package " +
			"address every other tool expects (workspace-relative directories are accepted " +
			"too). Workspace-level diagnostics (module or toolchain problems) are included " +
			"when present.",
	}, listPackages(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_files",
		Annotations: reads("List Files"),
		Description: "[Enumerator] List the Go files of one package by bare name — combined with the " +
			"package they form the file address every other tool expects." + depNote,
	}, listFiles(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_symbols",
		Annotations: reads("List Symbols"),
		Description: "[Enumerator] List the top-level symbols of one package: key, kind, and a one-line " +
			"summary (the signature for funcs and methods, the declaration line for types, " +
			"vars, and consts). Methods are keyed \"Type.Name\". Pass file_name to restrict to " +
			"one file." + depNote,
	}, listSymbols(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_methods",
		Annotations: reads("List Methods"),
		Description: "[Enumerator] List the method signatures declared on one type." + keyNote + depNote,
	}, listMethods(eng))

	// Describers
	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_package",
		Annotations: reads("Describe Package"),
		Description: "[Describer] Show a package's godoc — every file's doc comment (the comment block " +
			"directly above \"package X\"), concatenated in file order — plus its file list." + depNote,
	}, describePackage(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_file",
		Annotations: reads("Describe File"),
		Description: "[Describer] Show one file's own doc comment alone — the narrower read when only " +
			"that file's contribution to the package doc is needed." + depNote,
	}, describeFile(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_symbol",
		Annotations: reads("Describe Symbol"),
		Description: "[Describer] Show a symbol's full declaration source (doc comment included) and " +
			"kind, whatever it is — func, method, type, var, or const. A type's method " +
			"signatures are included too." + keyNote + depNote,
	}, describeSymbol(eng))

	// Finders
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_declarations_like",
		Annotations: reads("Search Declarations Like"),
		Description: "[Finder] Find top-level declarations across the whole workspace whose key " +
			"contains the given name, case-insensitively. Methods match as \"Type.Name\".",
	}, searchDeclarationsLike(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_source",
		Annotations: reads("Search Source"),
		Description: "[Finder] Find top-level declarations across the whole workspace whose source " +
			"text matches a Go regular expression — bodies, doc comments, and string " +
			"literals included. The general-purpose finder when no name is known. " +
			"Text outside declarations (imports, package clauses) is not searched.",
	}, searchSource(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_implementors",
		Annotations: reads("Search Implementors"),
		Description: "[Finder] Find every named type in the workspace whose method set satisfies the " +
			"given interface, checked with full type information — embedded and promoted " +
			"methods included. The target must be a non-empty workspace interface; " +
			"dependencies are outside the search universe." + keyNote,
	}, searchImplementors(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_references",
		Annotations: reads("Search References"),
		Description: "[Finder] Find every top-level declaration in the workspace that references the " +
			"given symbol, resolved with full type information. Results are declaration " +
			"addresses, not line positions; the definition itself and self-references " +
			"are excluded. The target must be a workspace symbol." + keyNote,
	}, searchReferences(eng))

	// Diagnostics
	mcp.AddTool(server, &mcp.Tool{
		Name:        "diagnostics",
		Annotations: reads("Workspace Diagnostics"),
		Description: "[Diagnostics] Report every compiler and loader problem in the workspace: parse, load, " +
			"and type errors, each positioned as file:line:col.",
	}, diagnostics(eng))

	// Creators
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_package",
		Annotations: mutates("Create Package", false),
		Description: "[Creator] Create a new package directory with one starter file. The package name " +
			"defaults to the directory base. Fails if a package already exists there." + echoNote,
	}, createPackage(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_file",
		Annotations: mutates("Create File", false),
		Description: "[Creator] Add an empty file to an existing package, optionally seeded with a " +
			"package doc comment. Fails if the file exists." + echoNote,
	}, createFile(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_symbol",
		Annotations: mutates("Create Symbol", false),
		Description: "[Creator] Add one new top-level symbol (func, method, type, var, or const) to a " +
			"file of an existing package. The file is created if missing; the symbol's name " +
			"must not exist. Imports and placement are managed by the server — just write " +
			"the declaration. A new plain const or var merges into that file's existing " +
			"grouped block of the same kind, if one already exists, instead of starting a " +
			"new one; a position-dependent (iota) group never merges and always starts its " +
			"own, placed next to its shared type's own declaration when typed and that type " +
			"is in the same file, otherwise in the standard const/var region." + echoNote,
	}, createSymbol(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_symbol_batch",
		Annotations: mutates("Create Symbol Batch", false),
		Description: "[Creator] Like create_symbol, for several symbols in one round-trip: one " +
			"transaction, one recheck, one echo. Entries are created in order; if any entry " +
			"fails (including one entry naming a symbol an earlier entry in the same batch " +
			"just created), the whole batch is discarded — none of it applies — and the error " +
			"names which entry failed. The diagnostics returned are the batch's final delta " +
			"only: a diagnostic one entry introduces and a later entry resolves never appears, " +
			"the same as within any single edit's own blast radius." + echoNote,
	}, createSymbolBatch(eng))

	// Editors
	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit_symbol",
		Annotations: mutates("Edit Symbol", true),
		Description: "[Editor] Replace a symbol's entire declaration (doc comment included). For a " +
			"member of a grouped const/var/type block, pass the member's spec as written " +
			"inside the group. Renaming via replacement is allowed if the new name doesn't " +
			"collide. For a member of a position-dependent const group (iota, or inheriting " +
			"the previous spec's expression), pass the group's whole intended state — every " +
			"member, not just the one you're addressing — since anything less silently drops " +
			"whatever member names aren't mentioned; the symbol you addressed must still be " +
			"present, or the edit is refused (use move_symbol to rename a group member " +
			"instead). Introducing iota into a member of a group that doesn't already use it " +
			"is refused. Imports are managed by the server — just use the identifiers." + keyNote + echoNote,
	}, editSymbol(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit_symbol_batch",
		Annotations: mutates("Edit Symbol Batch", true),
		Description: "[Editor] Like edit_symbol, for several symbols in one round-trip: one " +
			"transaction, one recheck, one echo. Every entry must address a different symbol — " +
			"two entries targeting the same one, identical source or not, are refused before " +
			"anything is touched, since a batch should express one decision per symbol, not an " +
			"ambiguous pair of them. Entries are applied in order; if any entry fails, the whole " +
			"batch is discarded and the error names which entry failed. The diagnostics returned " +
			"are the batch's final delta only: a diagnostic one entry introduces and a later " +
			"entry resolves never appears, the same as within any single edit's own blast " +
			"radius." + keyNote + echoNote,
	}, editSymbolBatch(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit_file",
		Annotations: mutates("Edit File", true),
		Description: "[Editor] Replace or clear a file's package doc comment — the comment block " +
			"directly above \"package X\" — leaving the rest of the file untouched. Empty doc " +
			"clears it." + echoNote,
	}, editFile(eng))

	// Deleters
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_symbol",
		Annotations: mutates("Delete Symbol", true),
		Description: "[Deleter] Delete a symbol — its spec alone when it lives in a grouped block " +
			"with siblings, unless its value is derived from its position in the group " +
			"(iota, or inheriting the previous spec's expression), in which case the whole " +
			"group is deleted together, since deleting one such member and keeping the rest " +
			"has no single correct resolution (use edit_symbol with the group's whole " +
			"intended state instead). A name sharing a spec with others (`var a, b int`) is " +
			"trimmed from it instead of taking the others down with it; names sharing one " +
			"multi-valued expression (`var a, b = f()`) blank the targeted one to `_` instead, " +
			"since the call's arity can't shrink — deleting every real name this way collapses " +
			"to removing the whole statement. Idempotent: a symbol that's already gone is a " +
			"noop, not an error." + keyNote + echoNote,
	}, deleteSymbol(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_file",
		Annotations: mutates("Delete File", true),
		Description: "[Deleter] Delete a file and every declaration in it. Idempotent: a file that's " +
			"already gone is a noop, not an error." + echoNote,
	}, deleteFile(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_package",
		Annotations: mutates("Delete Package", true),
		Description: "[Deleter] Delete a whole package directory. Idempotent: a package that's " +
			"already gone is a noop, not an error." + echoNote,
	}, deletePackage(eng))

	// Refactorings
	mcp.AddTool(server, &mcp.Tool{
		Name:        "move_symbol",
		Annotations: mutates("Move Symbol", true),
		Description: "[Refactoring] Rename a symbol, relocate it to another file, or both, in any " +
			"combination — at least one of new_pkg_path (with new_file_name), new_file_name, " +
			"or new_symbol_key is required. new_symbol_key follows the same grammar as " +
			"symbol_key: a bare identifier for a non-method, \"Recv.Name\" for a method — and " +
			"for a method it must be qualified, with Recv matching the symbol's actual " +
			"receiver exactly, since a rename can never change what a method belongs to. A " +
			"rename propagates to every resolved reference across the workspace first " +
			"(renaming an interface method does not chase implementors — broken " +
			"satisfactions appear in the returned diagnostics instead), then the — possibly " +
			"renamed — declaration is relocated; the destination file is created when " +
			"missing. Cross-package relocation does not rewrite qualifiers at use sites " +
			"still referring to the old package — that surfaces as ordinary diagnostics " +
			"afterward. A member of a grouped const/var/type block is extracted as a " +
			"standalone declaration; a member whose value depends on its position in the " +
			"group (iota, or inheriting the previous spec's expression) can be renamed " +
			"freely — renaming never touches position — but relocating it relocates its " +
			"*whole* group together, in order, even if only one member's key was given, " +
			"since extracting just one member alone would break the positions of the rest. " +
			"Never crosses the test build boundary." + keyNote + echoNote,
	}, moveSymbol(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "move_file",
		Annotations: mutates("Move File", true),
		Description: "[Refactoring] Rename a file within its package, relocate it to another package, " +
			"or both — at least one of new_pkg_path or new_file_name is required. Declarations " +
			"travel with the file unchanged; relocating into a different package can leave " +
			"declarations that referenced now-out-of-scope unexported siblings broken — that " +
			"surfaces as ordinary diagnostics afterward, not a refusal." + echoNote,
	}, moveFile(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "move_package",
		Annotations: mutates("Move Package", true),
		Description: "[Refactoring] Move a package directory, rewriting its import path in every importer. " +
			"When the package name matches the old directory base, the name and every " +
			"unaliased qualifier are renamed too — as is each file's own leading " +
			"\"Package oldname\" doc-comment opening, when it has one." + echoNote,
	}, movePackage(eng))

	// Session
	mcp.AddTool(server, &mcp.Tool{
		Name:        "flush",
		Annotations: mutates("Flush to Disk", true),
		Description: "[Session] Write every in-memory edit to disk: dirty files are written, deleted and " +
			"renamed-away paths are unlinked. Until flush, the filesystem is untouched.",
	}, flush(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "reload",
		Annotations: mutates("Reload from Disk", true),
		Description: "[Session] Rebuild the in-memory workspace from disk, discarding every unflushed " +
			"edit and pending deletion — the inverse of flush. The echo reports what was " +
			"discarded, grouped by package, plus the fresh workspace diagnostics. Use after " +
			"the filesystem changed behind the server.",
	}, reload(eng))
}

const echoNote = " Returns the files changed, the diagnostics the edit introduced (its blast-" +
	"radius), and the pre-existing diagnostics it resolved; an error means nothing was changed."

// depNote marks the read tools that also serve dependencies.
const depNote = " Dependencies resolve by import path too: read-only, exported API only, loaded on first touch."

// keyNote marks the tools whose SymbolKey input addresses a symbol.
const keyNote = " symbol_key is the symbol's address: its bare name, or \"Type.Name\" for methods."

// DiagBlock is the shared optional diagnostics view, scoped to whatever the
// carrying tool read. See the package doc's output convention. Diagnostics
// is capped at diagLimit (default 20, tunable via -diagnostics-limit);
// Truncated is nil when everything fit, otherwise the count left out —
// the diagnostics tool itself is never capped, so it's always the
// complete-inventory fallback.
type DiagBlock struct {
	Diagnostics []DiagnosticEntry `json:"diagnostics,omitempty"`
	Truncated   *int              `json:"truncated,omitempty"`
}

type ListPackagesInput struct{}

type ListPackagesOutput struct {
	Packages []string `json:"packages"`
	DiagBlock
}

type ListFilesInput struct {
	PkgPath string `json:"pkg_path"`
}

type ListFilesOutput struct {
	Files []string `json:"files"`
	DiagBlock
}

type ListSymbolsInput struct {
	PkgPath  string  `json:"pkg_path"`
	FileName *string `json:"file_name,omitempty"`
}

type SymbolEntry struct {
	SymbolKey string `json:"symbol_key"`
	Kind      string `json:"kind"`
	Summary   string `json:"summary"`
}

type ListSymbolsOutput struct {
	Symbols []SymbolEntry `json:"symbols"`
	DiagBlock
}

type ListMethodsInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

type ListMethodsOutput struct {
	Methods []string `json:"methods"`
	DiagBlock
}

type SearchLikeInput struct {
	Name string `json:"name"`
}

type SearchSourceInput struct {
	Regexp string `json:"regexp"`
}

type SearchImplementorsInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

type SearchReferencesInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

type MatchEntry struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
	Kind      string `json:"kind"`
}

type SearchOutput struct {
	Matches []MatchEntry `json:"matches"`
}

type DiagnosticsInput struct{}

type DiagnosticsOutput struct {
	Diagnostics []DiagnosticEntry `json:"diagnostics"`
}

// WriteOutput is the shared echo of every write tool (creators, editors,
// refactorings alike): the files changed grouped by package, the diagnostics
// this edit introduced and resolved (each nil when there's nothing to report,
// not an empty block) how many pre-existing diagnostics it left untouched,
// and whether those two diagnostics blocks can be trusted at all.
type WriteOutput struct {
	Files                     map[string][]string `json:"files"`
	IntroducedDiagnostics     *DiagBlock          `json:"introduced_diagnostics,omitempty"`
	ResolvedDiagnostics       *DiagBlock          `json:"resolved_diagnostics,omitempty"`
	UnrelatedDiagnosticsCount *int                `json:"unrelated_diagnostics_count,omitempty"`
	DiagnosticsUnavailable    *bool               `json:"diagnostics_unavailable,omitempty"`
}

type CreatePackageInput struct {
	PkgPath string  `json:"pkg_path"`
	Name    *string `json:"name,omitempty"`
}

type CreateFileInput struct {
	PkgPath  string  `json:"pkg_path"`
	FileName string  `json:"file_name"`
	Doc      *string `json:"doc,omitempty"`
}

type CreateSymbolInput struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
	Source   string `json:"source"`
}

type EditSymbolInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
	Source    string `json:"source"`
}

type DeleteSymbolInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

type DeleteFileInput struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
}

type DeletePackageInput struct {
	PkgPath string `json:"pkg_path"`
}

type MoveSymbolInput struct {
	PkgPath      string  `json:"pkg_path"`
	SymbolKey    string  `json:"symbol_key"`
	NewPkgPath   *string `json:"new_pkg_path,omitempty"`
	NewFileName  *string `json:"new_file_name,omitempty"`
	NewSymbolKey *string `json:"new_symbol_key,omitempty"`
}

type MoveFileInput struct {
	PkgPath     string  `json:"pkg_path"`
	FileName    string  `json:"file_name"`
	NewPkgPath  *string `json:"new_pkg_path,omitempty"`
	NewFileName *string `json:"new_file_name,omitempty"`
}

type MovePackageInput struct {
	PkgPath    string `json:"pkg_path"`
	NewPkgPath string `json:"new_pkg_path"`
}

type FlushInput struct{}

type FlushOutput struct {
	FilesWritten map[string][]string `json:"files_written,omitempty"`
	FilesRemoved map[string][]string `json:"files_removed,omitempty"`
}

type ReloadInput struct{}

// ReloadOutput reports what a reload threw away, grouped by package, plus
// the fresh workspace diagnostics — reload's scope is the whole workspace,
// so here the view and the inventory coincide.
type ReloadOutput struct {
	FilesDiscarded map[string][]string `json:"files_discarded,omitempty"`
	DiagBlock
}

// DiagnosticEntry is one problem report, addressed the same way every other
// tool addresses a symbol: PkgPath/SymbolKey are directly usable as-is with
// describe_symbol/edit_symbol. FileName is the coarser fallback when a
// diagnostic is attributable to a file but no single declaration; all three
// are nil for module/driver-level problems.
type DiagnosticEntry struct {
	PkgPath   *string `json:"pkg_path,omitempty"`
	FileName  *string `json:"file_name,omitempty"`
	SymbolKey *string `json:"symbol_key,omitempty"`
	Kind      string  `json:"kind"`
	Message   string  `json:"message"`
}

type EditFileInput struct {
	PkgPath  string  `json:"pkg_path"`
	FileName string  `json:"file_name"`
	Doc      *string `json:"doc,omitempty"`
}

type DescribePackageInput struct {
	PkgPath string `json:"pkg_path"`
}

// DescribePackageOutput is the package's godoc plus the file list already
// on hand while assembling it.
type DescribePackageOutput struct {
	Doc   *string  `json:"doc,omitempty"`
	Files []string `json:"files,omitempty"`
	DiagBlock
}

type DescribeFileInput struct {
	PkgPath  string `json:"pkg_path"`
	FileName string `json:"file_name"`
}

type DescribeFileOutput struct {
	Doc *string `json:"doc,omitempty"`
	DiagBlock
}

type DescribeSymbolInput struct {
	PkgPath   string `json:"pkg_path"`
	SymbolKey string `json:"symbol_key"`
}

// DescribeSymbolOutput covers every symbol kind uniformly; Methods is only
// populated when Kind == "type".
type DescribeSymbolOutput struct {
	File    string   `json:"file"`
	Source  string   `json:"source"`
	Kind    string   `json:"kind"`
	Methods []string `json:"methods,omitempty"`
	DiagBlock
}

// CreateSymbolBatchInput creates several symbols in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails.
type CreateSymbolBatchInput struct {
	Creates []CreateSymbolInput `json:"creates"`
}

// EditSymbolBatchInput edits several symbols in one transaction, one
// recheck, one echo — resolved in order, the whole batch discarded on the
// first entry that fails. Every entry must address a different symbol;
// two entries targeting the same one, identical source or not, are
// refused before the transaction opens.
type EditSymbolBatchInput struct {
	Edits []EditSymbolInput `json:"edits"`
}
