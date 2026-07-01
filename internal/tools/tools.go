// Package tools is the presentation layer: it composes engine lookups into
// the MCP tools exposed to the agent and owns every decision about what gets
// printed. The engine returns data; this package returns representations.
//
// This file declares the tool surface — registration and I/O shapes — while
// the implementation lives beside its semantic section (read.go, edit.go).
// Tool naming convention: list_* enumerate a scope, describe_* render one
// address, search_* scan the workspace, diagnostics reports problems,
// create_*/edit_*/delete_*/rename_* mutate, flush writes to disk.
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
		Annotations: reads("List packages"),
		Description: "List every Go package directory in the workspace by its relative path. " +
			"These paths are the package addresses every other tool expects. " +
			"Workspace-level diagnostics (module or toolchain problems) are included when present.",
	}, listPackages(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_files",
		Annotations: reads("List files"),
		Description: "List the Go files of one package as workspace-relative paths.",
	}, listFiles(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_symbols",
		Annotations: reads("List symbols"),
		Description: "List the top-level symbols of one package: key, kind, and a one-line " +
			"summary (the signature for funcs and methods, the declaration line for types, " +
			"vars, and consts — var/const values appear here; they have no describe_* tool). " +
			"Methods are keyed \"Type.Name\". Pass file to restrict to one file.",
	}, listSymbols(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_methods",
		Annotations: reads("List methods"),
		Description: "List the method signatures declared on one type.",
	}, listMethods(eng))

	// Describers
	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_type",
		Annotations: reads("Describe type"),
		Description: "Show a type's full declaration source (doc comment included) " +
			"together with the signatures of its methods.",
	}, describeType(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_function",
		Annotations: reads("Describe function"),
		Description: "Show a function's full declaration source, doc comment and body included.",
	}, describeFunction(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_method",
		Annotations: reads("Describe method"),
		Description: "Show a method's full declaration source, doc comment and body included.",
	}, describeMethod(eng))

	// Finders
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_declarations_like",
		Annotations: reads("Search declarations"),
		Description: "Find top-level declarations across the whole workspace whose key " +
			"contains the given name, case-insensitively. Methods match as \"Type.Name\".",
	}, searchDeclarationsLike(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_source",
		Annotations: reads("Search source"),
		Description: "Find top-level declarations across the whole workspace whose source " +
			"text matches a Go regular expression — bodies, doc comments, and string " +
			"literals included. The general-purpose finder when no name is known. " +
			"Text outside declarations (imports, package clauses) is not searched.",
	}, searchSource(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_implementors",
		Annotations: reads("Find implementors"),
		Description: "Find every named type in the workspace whose method set satisfies the " +
			"given interface, checked with full type information — embedded and promoted " +
			"methods included. The target must be a non-empty interface.",
	}, searchImplementors(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_references",
		Annotations: reads("Find references"),
		Description: "Find every top-level declaration in the workspace that references the " +
			"given symbol, resolved with full type information. Results are declaration " +
			"addresses, not line positions; the definition itself and self-references " +
			"are excluded.",
	}, searchReferences(eng))

	// Diagnostics
	mcp.AddTool(server, &mcp.Tool{
		Name:        "diagnostics",
		Annotations: reads("Workspace diagnostics"),
		Description: "Report every compiler and loader problem in the workspace: parse, load, " +
			"and type errors, each positioned as file:line:col.",
	}, diagnostics(eng))

	// Creators
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_package",
		Annotations: mutates("Create package", false),
		Description: "Create a new package directory with one starter file. The package name " +
			"defaults to the directory base. Fails if a package already exists there." + echoNote,
	}, createPackage(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_file",
		Annotations: mutates("Create file", false),
		Description: "Add an empty file to an existing package. Fails if the file exists." + echoNote,
	}, createFile(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_declaration",
		Annotations: mutates("Create declaration", false),
		Description: "Add one new top-level declaration to a file of an existing package. The " +
			"file is created if missing; the declaration's name must not exist. Imports and " +
			"placement are managed by the server — just write the declaration." + echoNote,
	}, createDeclaration(eng))

	// Editors
	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit_declaration",
		Annotations: mutates("Replace declaration", true),
		Description: "Replace a declaration's entire source (doc comment included). For a member " +
			"of a grouped const/var/type block, pass the member's spec as written inside the " +
			"group. Renaming via replacement is allowed if the new name doesn't collide. " +
			"Imports are managed by the server — just use the identifiers." + echoNote,
	}, editDeclaration(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_declaration",
		Annotations: mutates("Delete declaration", true),
		Description: "Delete a declaration — its spec alone when it lives in a grouped block " +
			"with siblings." + echoNote,
	}, deleteDeclaration(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_file",
		Annotations: mutates("Delete file", true),
		Description: "Delete a file and every declaration in it." + echoNote,
	}, deleteFile(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_package",
		Annotations: mutates("Delete package", true),
		Description: "Delete a whole package directory." + echoNote,
	}, deletePackage(eng))

	// Refactorings
	mcp.AddTool(server, &mcp.Tool{
		Name:        "rename_declaration",
		Annotations: mutates("Rename declaration", true),
		Description: "Rename a declaration and every resolved reference to it across the whole " +
			"workspace. Renaming an interface method does not chase implementors — broken " +
			"satisfactions appear in the returned diagnostics instead." + echoNote,
	}, renameDeclaration(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rename_file",
		Annotations: mutates("Rename file", true),
		Description: "Rename a file within its package. Declarations are unaffected." + echoNote,
	}, renameFile(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rename_package",
		Annotations: mutates("Move package", true),
		Description: "Move a package directory, rewriting its import path in every importer. " +
			"When the package name matches the old directory base, the name and every " +
			"unaliased qualifier are renamed too." + echoNote,
	}, renamePackage(eng))

	// Session
	mcp.AddTool(server, &mcp.Tool{
		Name:        "flush",
		Annotations: mutates("Flush to disk", true),
		Description: "Write every in-memory edit to disk: dirty files are written, deleted and " +
			"renamed-away paths are unlinked. Until flush, the filesystem is untouched.",
	}, flush(eng))
}

const echoNote = " Returns the files changed, the diagnostics the edit introduced (its blast " +
	"radius), and the pre-existing diagnostics it resolved; an error means nothing was changed."

// DiagBlock is the shared optional diagnostics view, scoped to whatever the
// carrying tool read. See the package doc's output convention.
type DiagBlock struct {
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// ----- Enumerator shapes -----

type ListPackagesInput struct{}

type ListPackagesOutput struct {
	Packages []string `json:"packages"`
	DiagBlock
}

type ListFilesInput struct {
	Package string `json:"package"`
}

type ListFilesOutput struct {
	Files []string `json:"files"`
	DiagBlock
}

type ListSymbolsInput struct {
	Package string `json:"package"`
	File    string `json:"file,omitempty"`
}

type SymbolEntry struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

type ListSymbolsOutput struct {
	Symbols []SymbolEntry `json:"symbols"`
	DiagBlock
}

type ListMethodsInput struct {
	Package string `json:"package"`
	Type    string `json:"type"`
}

type ListMethodsOutput struct {
	Methods []string `json:"methods"`
	DiagBlock
}

// ----- Describer shapes -----

type DescribeTypeInput struct {
	Package string `json:"package"`
	Name    string `json:"name"`
}

type DescribeTypeOutput struct {
	File    string   `json:"file"`
	Source  string   `json:"source"`
	Methods []string `json:"methods,omitempty"`
	DiagBlock
}

type DescribeFunctionInput struct {
	Package string `json:"package"`
	Name    string `json:"name"`
}

type DescribeMethodInput struct {
	Package string `json:"package"`
	Type    string `json:"type"`
	Name    string `json:"name"`
}

// DescribeOutput is shared by describe_function and describe_method.
type DescribeOutput struct {
	File   string `json:"file"`
	Source string `json:"source"`
	DiagBlock
}

// ----- Finder shapes -----

type SearchLikeInput struct {
	Name string `json:"name"`
}

type SearchSourceInput struct {
	Regexp string `json:"regexp"`
}

type SearchImplementorsInput struct {
	Package string `json:"package"`
	Name    string `json:"name"`
}

type SearchReferencesInput struct {
	Package string `json:"package"`
	Key     string `json:"key"`
}

type MatchEntry struct {
	Package string `json:"package"`
	Key     string `json:"key"`
	Kind    string `json:"kind"`
}

type SearchOutput struct {
	Matches []MatchEntry `json:"matches"`
}

// ----- Diagnostics shapes -----

type DiagnosticsInput struct{}

type DiagnosticsOutput struct {
	Diagnostics []string `json:"diagnostics"`
}

// ----- Mutation shapes -----

// MutationOutput is the shared echo of every mutating tool: the files
// changed, the diagnostics this edit introduced (DiagBlock), and the
// pre-existing diagnostics it resolved.
type MutationOutput struct {
	Files []string `json:"files"`
	DiagBlock
	Resolved           []string `json:"resolved,omitempty"`
	RecheckUnavailable bool     `json:"recheck_unavailable,omitempty"`
}

type CreatePackageInput struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

type CreateFileInput struct {
	Package string `json:"package"`
	Name    string `json:"name"`
}

type CreateDeclarationInput struct {
	Package string `json:"package"`
	File    string `json:"file"`
	Source  string `json:"source"`
}

type EditDeclarationInput struct {
	Package string `json:"package"`
	Key     string `json:"key"`
	Source  string `json:"source"`
}

type DeleteDeclarationInput struct {
	Package string `json:"package"`
	Key     string `json:"key"`
}

type DeleteFileInput struct {
	Path string `json:"path"`
}

type DeletePackageInput struct {
	Package string `json:"package"`
}

type RenameDeclarationInput struct {
	Package string `json:"package"`
	Key     string `json:"key"`
	NewName string `json:"new_name"`
}

type RenameFileInput struct {
	Path    string `json:"path"`
	NewName string `json:"new_name"`
}

type RenamePackageInput struct {
	Package string `json:"package"`
	NewPath string `json:"new_path"`
}

type FlushInput struct{}

type FlushOutput struct {
	Written []string `json:"written"`
	Removed []string `json:"removed,omitempty"`
}
