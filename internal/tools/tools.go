// Package tools is the presentation layer: it composes engine lookups into
// the MCP tools exposed to the agent and owns every decision about what gets
// printed. The engine returns data; this package returns representations.
//
// This file declares the tool surface — registration and I/O shapes. Read
// handlers live one semantic category per file — enumerators.go, describers.go,
// finders.go, diagnostics.go — with read.go holding the cross-category
// resolution helpers (readPackage, readSymbol, methodSignatures). Write
// handlers mirror that: creators.go, deleters.go, editors.go, refactorings.go,
// disk.go (flush/reload, the disk boundary), with edit.go holding
// runEdit, the one relay every mutating handler flows through. shared.go
// holds helpers genuinely called from both sides. Handlers themselves carry
// no doc comments by design — they are mechanical relays, documented by
// their tool descriptions in Register.
// Tool naming convention: list_*/describe_*/search_* read, diagnostics
// stands alone as the one full-inventory report, create_*/edit_*/delete_*
// mirror their mutation verb, refactor_* covers structure-preserving
// transformations, and disk_* crosses the disk boundary.
// Tool descriptions earn words only for what changes the agent's input or
// its reading of the output — server internals stay out of them.
//
// Output convention: every reader carries an optional diagnostics block
// (DiagBlock) scoped to exactly what it read. Scoped blocks are views,
// never the inventory — a problem outside the read scope does not appear,
// so an empty block means "nothing wrong here", not "workspace healthy".
// The diagnostics tool is the complete inventory.
package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/store"
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

// Register wires every tool into the server. diagLimit caps the
// diagnostics rendered in every scoped DiagBlock (list_*/describe_*
// output, mutation echoes); negative values fall back to the default
// (20). The diagnostics_workspace tool itself is never capped.
func Register(server *mcp.Server, eng *store.Store, diagLimit int) {
	cfg := newToolConfig(diagLimit)

	// Enumerators
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_packages",
		Annotations: reads("List Packages"),
		Description: "List every Go package in the workspace by import path — the package " +
			"address every other tool expects (workspace-relative directories are accepted " +
			"too).",
	}, listPackages(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_files",
		Annotations: reads("List Files"),
		Description: "List the Go files of one package by bare name — combined with the " +
			"package they form the file address every other tool expects." + depNote,
	}, listFiles(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_symbols",
		Annotations: reads("List Symbols"),
		Description: "List the top-level symbols of one package: key, kind, and a one-line " +
			"summary (the signature for funcs and methods, the declaration line for types, " +
			"vars, and consts). Methods are keyed \"Type.Name\". Pass file_name to restrict to " +
			"one file." + depNote,
	}, listSymbols(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_methods",
		Annotations: reads("List Methods"),
		Description: "List the method signatures declared on one type." + keyNote + depNote,
	}, listMethods(eng, cfg))

	// Describers
	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_packages",
		Annotations: reads("Describe Packages"),
		Description: "Show one or more packages' godoc — every file's doc comment (the " +
			"comment block directly above \"package X\"), concatenated in file order — plus " +
			"its file list, in one round trip, resolved in order. If any entry fails, the " +
			"whole call fails and the error names which entry failed — batch entries that are " +
			"independent and already known-good; call once per entry instead if you want " +
			"feedback between steps." + depNote,
	}, describePackage(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_files",
		Annotations: reads("Describe Files"),
		Description: "Show one or more files' own doc comment alone — the narrower read " +
			"when only a file's contribution to its package doc is needed, in one round trip, " +
			"resolved in order. If any entry fails, the whole call fails and the error names " +
			"which entry failed — batch entries that are independent and already known-good; " +
			"call once per entry instead if you want feedback between steps." + depNote,
	}, describeFile(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_symbols",
		Annotations: reads("Describe Symbols"),
		Description: "Show one or more symbols' full declaration source (doc comment " +
			"included) and kind, whatever each is — func, method, type, var, or const, in one " +
			"round trip, resolved in order. A type's method signatures are included too. If " +
			"any entry fails, the whole call fails and the error names which entry failed — " +
			"batch entries that are independent and already known-good; call once per entry " +
			"instead if you want feedback between steps." + keyNote + depNote,
	}, describeSymbol(eng, cfg))

	// Finders
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_declarations_like",
		Annotations: reads("Search Declarations Like"),
		Description: "Find top-level declarations across the whole workspace whose key " +
			"contains the given name, case-insensitively. Methods match as \"Type.Name\".",
	}, searchDeclarationsLike(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_source",
		Annotations: reads("Search Source"),
		Description: "Find top-level declarations across the whole workspace whose source " +
			"text matches a Go regular expression — bodies, doc comments, and string " +
			"literals included. The general-purpose finder when no name is known. " +
			"Text outside declarations (imports, package clauses) is not searched.",
	}, searchSource(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_implementors",
		Annotations: reads("Search Implementors"),
		Description: "Find every named type in the workspace whose method set satisfies the " +
			"given interface, checked with full type information — embedded and promoted " +
			"methods included. The target must be a non-empty workspace interface; " +
			"dependencies are outside the search universe." + keyNote,
	}, searchImplementors(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_references",
		Annotations: reads("Search References"),
		Description: "Find every top-level declaration in the workspace that references the " +
			"given symbol, resolved with full type information. Results are declaration " +
			"addresses, not line positions; the definition itself and self-references " +
			"are excluded. The target must be a workspace symbol." + keyNote,
	}, searchReferences(eng))

	// Diagnostics
	mcp.AddTool(server, &mcp.Tool{
		Name:        "diagnostics_workspace",
		Annotations: reads("Workspace Diagnostics"),
		Description: "Report every compiler and loader problem in the workspace: parse, load, " +
			"and type errors, each positioned as file:line:col.",
	}, diagnostics(eng))

	// Creators
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_packages",
		Annotations: mutates("Create Packages", false),
		Description: "Create one or more new package directories, each with one starter " +
			"file, in one transaction, one recheck, one echo — resolved in order. The package " +
			"name defaults to the directory base. If any entry fails (including one entry " +
			"naming a package an earlier entry in the same batch just created), the whole " +
			"batch is discarded and the error names which entry failed — batch entries that " +
			"are independent and already known-good; call once per entry instead if you want " +
			"diagnostics feedback between steps." + echoNote,
	}, createPackage(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_files",
		Annotations: mutates("Create Files", false),
		Description: "Add one or more empty files to existing packages, each optionally " +
			"seeded with a package doc comment, in one transaction, one recheck, one echo — " +
			"resolved in order. Fails if a file already exists. If any entry fails, the whole " +
			"batch is discarded and the error names which entry failed — batch entries that " +
			"are independent and already known-good; call once per entry instead if you want " +
			"diagnostics feedback between steps." + echoNote,
	}, createFile(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_symbols",
		Annotations: mutates("Create Symbols", false),
		Description: "Add one or more new top-level symbols (func, method, type, var, or " +
			"const), each to a file of an existing package, in one transaction, one recheck, " +
			"one echo — resolved in order. A file is created if missing; a symbol's name must " +
			"not exist (including one entry naming a symbol an earlier entry in the same " +
			"batch just created). Imports and placement are managed by the server — just " +
			"write the declaration. A new plain const or var merges into that file's existing " +
			"grouped block of the same kind, if one already exists, instead of starting a new " +
			"one; a position-dependent (iota) group never merges and always starts its own, " +
			"placed next to its shared type's own declaration when typed and that type is in " +
			"the same file, otherwise in the standard const/var region. If any entry fails, " +
			"the whole batch is discarded and the error names which entry failed — batch " +
			"entries that are independent and already known-good; call once per entry instead " +
			"if you want diagnostics feedback between steps." + echoNote,
	}, createSymbol(eng, cfg))

	// Editors
	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit_symbols",
		Annotations: mutates("Edit Symbols", true),
		Description: "Replace one or more symbols' entire declarations (doc comment " +
			"included), in one transaction, one recheck, one echo — resolved in order. Every " +
			"entry must address a different symbol — two entries targeting the same one, " +
			"identical source or not, are refused before anything is touched. For a member of " +
			"a grouped const/var/type block, pass the member's spec as written inside the " +
			"group. Renaming via replacement is allowed if the new name doesn't collide. For " +
			"a member of a position-dependent const group (iota, or inheriting the previous " +
			"spec's expression), pass the group's whole intended state — every member, not " +
			"just the one you're addressing — since anything less silently drops whatever " +
			"member names aren't mentioned; the symbol you addressed must still be present, " +
			"or the edit is refused (use refactor_move_symbol to rename a group member instead). " +
			"Introducing iota into a member of a group that doesn't already use it is refused. " +
			"Imports are managed by the server — just use the identifiers. If any entry fails, " +
			"the whole batch is discarded and the error names which entry failed — batch " +
			"entries that are independent and already known-good; call once per entry instead " +
			"if you want diagnostics feedback between steps." + keyNote + echoNote,
	}, editSymbol(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "edit_files",
		Annotations: mutates("Edit Files", true),
		Description: "Replace or clear one or more files' package doc comments — the " +
			"comment block directly above \"package X\" — leaving the rest of each file " +
			"untouched, in one transaction, one recheck, one echo — resolved in order. Empty " +
			"doc clears it. Every entry must address a different file — two entries targeting " +
			"the same one, identical doc or not, are refused before anything is touched. If " +
			"any entry fails, the whole batch is discarded and the error names which entry " +
			"failed — batch entries that are independent and already known-good; call once " +
			"per entry instead if you want diagnostics feedback between steps." + echoNote,
	}, editFile(eng, cfg))

	// Deleters
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_symbols",
		Annotations: mutates("Delete Symbols", true),
		Description: "Delete one or more symbols in one transaction, one recheck, one echo — " +
			"resolved in order. A symbol's spec is deleted alone when it lives in a grouped " +
			"block with siblings, unless its value is derived from its position in the group " +
			"(iota, or inheriting the previous spec's expression), in which case the whole " +
			"group is deleted together, since deleting one such member and keeping the rest " +
			"has no single correct resolution (use edit_symbols with the group's whole " +
			"intended state instead). A name sharing a spec with others (`var a, b int`) is " +
			"trimmed from it instead of taking the others down with it; names sharing one " +
			"multi-valued expression (`var a, b = f()`) blank the targeted one to `_` instead, " +
			"since the call's arity can't shrink — deleting every real name this way collapses " +
			"to removing the whole statement. Idempotent: a symbol that's already gone is a " +
			"noop, not an error, so a duplicate target across entries is harmless. If any " +
			"entry fails for a reason other than absence, the whole batch is discarded and the " +
			"error names which entry failed — batch entries that are independent and already " +
			"known-good; call once per entry instead if you want diagnostics feedback between " +
			"steps." + keyNote + echoNote,
	}, deleteSymbol(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_files",
		Annotations: mutates("Delete Files", true),
		Description: "Delete one or more files, each with every declaration in it, in one " +
			"transaction, one recheck, one echo — resolved in order. Idempotent: a file " +
			"that's already gone is a noop, not an error, so a duplicate target across " +
			"entries is harmless. If any entry fails for a reason other than absence, the " +
			"whole batch is discarded and the error names which entry failed." + echoNote,
	}, deleteFile(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_packages",
		Annotations: mutates("Delete Packages", true),
		Description: "Delete one or more whole package directories in one transaction, one " +
			"recheck, one echo — resolved in order. Idempotent: a package that's already gone " +
			"is a noop, not an error, so a duplicate target across entries is harmless. If any " +
			"entry fails for a reason other than absence, the whole batch is discarded and the " +
			"error names which entry failed." + echoNote,
	}, deletePackage(eng, cfg))

	// Refactorings
	mcp.AddTool(server, &mcp.Tool{
		Name:        "refactor_move_symbol",
		Annotations: mutates("Move Symbol", true),
		Description: "Rename a symbol, relocate it to another file, or both, in any " +
			"combination — at least one of new_pkg_path (with new_file_name), new_file_name, " +
			"or new_symbol_key is required. Give symbol_keys instead of symbol_key to relocate " +
			"several symbols to the same destination file in one call — a type together with " +
			"its methods, say — instead of a same-package consolidation move first; " +
			"symbol_keys only relocates (never combines with new_symbol_key — rename with " +
			"symbol_key first, then move the group) and needs at least two keys; give " +
			"symbol_key or symbol_keys, never both. new_symbol_key follows the same grammar " +
			"as symbol_key: a bare identifier for a non-method, \"Recv.Name\" for a method — " +
			"and for a method it must be qualified, with Recv matching the symbol's actual " +
			"receiver exactly, since a rename can never change what a method belongs to. A " +
			"rename to an unexported name is refused outright when any reference from a " +
			"different package still stands — once unexported, that reference can never be " +
			"found again to fix, even by a later revert back to exported, so the tool declines " +
			"rather than leave it silently, permanently stale. A rename propagates to every " +
			"resolved reference across the workspace first (renaming an interface method does " +
			"not chase implementors — broken satisfactions appear in the returned diagnostics " +
			"instead), then the — possibly renamed — declaration is relocated; the " +
			"destination file is created when missing. Cross-package relocation is refused " +
			"when it's provably unsafe: a method whose receiver type isn't moving with it, a " +
			"type being moved while a method on it is left behind (either way, a method and " +
			"its receiver type must share a package — illegal Go, not just risky), a name " +
			"collision at the destination, the symbol depending on an unexported sibling left " +
			"behind, or an unexported symbol being moved while code left behind still needs " +
			"it. Otherwise every reference across the move is repointed automatically, both " +
			"directions: a same-package caller gains the destination's qualifier, a caller " +
			"already in the destination loses its qualifier, any other caller's qualifier is " +
			"repointed to the new package, and the moved declaration's own references to " +
			"exported siblings staying behind gain the original package's qualifier. A member " +
			"of a grouped const/var/type block is extracted as a standalone declaration; a " +
			"member whose value depends on its position in the group (iota, or inheriting the " +
			"previous spec's expression) can be renamed freely — renaming never touches " +
			"position — but relocating it relocates its *whole* group together, in order, " +
			"even if only one member's key was given, since extracting just one member alone " +
			"would break the positions of the rest. Never crosses the test build boundary." + keyNote + echoNote,
	}, moveSymbol(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "refactor_move_file",
		Annotations: mutates("Move File", true),
		Description: "Rename a file within its package, relocate it to another package, " +
			"or both — at least one of new_pkg_path or new_file_name is required. Declarations " +
			"travel with the file unchanged. Relocating into a different package is refused " +
			"when it's provably unsafe: a method and its receiver type ending up split across " +
			"the move (either direction — a method moving without its receiver, or a receiver " +
			"moving while a method on it stays behind — a method must share a package with its " +
			"receiver type, illegal Go otherwise), a name collision at the destination, a " +
			"moving declaration depending on an unexported sibling left behind, or an " +
			"unexported declaration leaving while code left behind still needs it. Otherwise " +
			"every reference across the move is repointed automatically, both directions: " +
			"external callers of the file's exported declarations are requalified exactly as " +
			"refactor_move_symbol does, and the file's own references to exported siblings staying " +
			"behind gain the original package's qualifier." + echoNote,
	}, moveFile(eng, cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "refactor_move_package",
		Annotations: mutates("Move Package", true),
		Description: "Move a package directory, rewriting its import path in every importer. " +
			"When the package name matches the old directory base, the name and every " +
			"unaliased qualifier are renamed too — as is each file's own leading " +
			"\"Package oldname\" doc-comment opening, when it has one." + echoNote,
	}, movePackage(eng, cfg))

	// Disk
	mcp.AddTool(server, &mcp.Tool{
		Name:        "disk_flush",
		Annotations: mutates("Flush to Disk", true),
		Description: "Write every in-memory edit to disk: dirty files are written, deleted and " +
			"renamed-away paths are unlinked. Until flush, the filesystem is untouched.",
	}, flush(eng))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "disk_reload",
		Annotations: mutates("Reload from Disk", true),
		Description: "Rebuild the in-memory workspace from disk, discarding every unflushed " +
			"edit and pending deletion — the inverse of flush. The echo reports what was " +
			"discarded, grouped by package, plus the fresh workspace diagnostics. Use after " +
			"the filesystem changed behind the server.",
	}, reload(eng, cfg))
}

const echoNote = " Returns the files changed, the diagnostics the edit introduced (its blast-" +
	"radius), and the pre-existing diagnostics it resolved; an error means nothing was changed."

// depNote marks the read tools that also serve dependencies.
const depNote = " Dependencies resolve by import path too: read-only, exported API only, loaded on first touch."

// keyNote marks the tools whose SymbolKey input addresses a symbol.
const keyNote = " symbol_key is the symbol's address: its bare name, or \"Type.Name\" for methods."
