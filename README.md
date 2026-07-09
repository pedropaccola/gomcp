# gomcp
An MCP server that keeps an in-memory, structurally-aware model of a Go codebase and exposes it to an agent through a small read/write interface. Built on `go/packages` and `go/types`.

Inspired by Kent Beck's [SmalltalkGenie](https://github.com/KentBeck/SmalltalkGenie) (more on his [Substack](https://newsletter.kentbeck.com/p/smalltalk-genie)).

## About
Coding agents today manage the noise problem by going bigger: larger context windows, embedding retrieval, repo maps, compaction. Precise, LSP-style symbol search and definition lookup already exists inside most of them. But it is always one option among many, with grep and whole-file reads as the fallback.

This project isn't introducing scoped access. It's removing everything else.

Ask about a function and the response is that function's declaration as real Go source, and nothing more. Edits flow the same way in reverse: declaration-sized changes applied to the live in-memory model, with nothing touching the filesystem until the session is flushed. There is no browsing mode, no whole-file read, no escape hatch.

Everything runs on Go's own machinery: parsing, type-checking, cross-references, and formatting all come from the standard toolchain (`go/ast`, `go/types`, `go/packages`, `golang.org/x/tools`). No language server, no embedding index, no database, no background daemon — the only dependency outside the Go ecosystem is the MCP protocol SDK. If you can build the project, the server can model it.

### The bet
Every widely used tool keeps the fallback available; this one removes it, betting that an agent can work effectively with precision as the only interface, and that the tokens and attention saved outweigh the lost safety net. Whether that trade holds is the open question this proof of concept tries to answer.

Because the server owns the live model, the loop is: query, edit, see the blast radius, repeat. Every mutation triggers a re-check of the in-memory state and answers with the diagnostics it introduced — a signature change immediately reports the callers it broke, without a rebuild or a second read pass.

### The compromises
- **The unit is the top-level declaration.** Anything between declarations — import blocks, package clauses, floating comments — is not addressable by the agent and must be managed by the server itself.
- **Structural feedback is not correctness.** Diagnostics catch breakage the compiler can see. A type-correct edit can still implement the wrong logic, miss an edge case, or violate a convention no tool checks.
- **Go only, by leaning on Go.** The server delegates to the standard toolchain rather than re-implementing language semantics, tying it to the language but keeping it honest.

## Tools
Small set of 24 tools:

### Read
* Enumerators (consistent sorted output): `list_packages`, `list_files`, `list_symbols`, `list_methods`
* Describers (declarations source): `describe_type`, `describe_function`, `describe_method`
* Finders (grep-like): `search_declarations_like`, `search_source`, `search_implementors`, `search_references`
* Diagnostics (full workspace diagnostics): `diagnostics`

### Write
* Creators (fail if the address already exists; cannot destroy code): `create_package`, `create_file`, `create_declaration`
* Editors (fail if the address doesn't exist): `edit_declaration`, `delete_declaration`, `delete_file`, `delete_package`
* Refactorings (structure-preserving transformations): `move_declaration`, `rename_declaration`, `rename_file`, `rename_package`
* Session (writes the in-memory state to disk): `flush`

More on [tools/tools.go](../main/internal/tools/tools.go)

## Installation
```bash
go install github.com/pedropaccola/gomcp/cmd/mcpgo@latest
```

## MCP configuration
`mcpgo` speaks MCP over stdio. Most clients (Claude Code, Cursor, Windsurf, ...) share the same configuration shape:

```json
{
  "mcpServers": {
    "gomcp": {
      "command": "mcpgo",
      "args": ["-cwd", "/absolute/path/to/your/go/workspace"]
    }
  }
}
```

The workspace root resolves in order: the `-cwd` flag, the `CLAUDE_WORKSPACE` environment variable, then the server process working directory.
