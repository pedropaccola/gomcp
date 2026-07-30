# gomcp 🧞‍♂️

An experimental, declaration-scoped MCP server that exposes an in-memory Go compilation loop directly to coding agents.

Inspired by Kent Beck's 3Xs hypothesis (Explore, Expand, Extract) and his work on [SmalltalkGenie](https://github.com/KentBeck/SmalltalkGenie), **gomcp** explores an alternative approach to AI-assisted coding: *What if an agent could interact entirely within an in-memory compilation loop without touching the filesystem?*

Environments like Pharo (Smalltalk) are known for providing immediate developer feedback by bypassing file-based translation. **gomcp** aims to bring a similar style of immediate feedback to Go's compiled environment by leveraging Go’s native compiler packages (`go/parser`, `go/ast`, and `go/types`) to manage code state entirely in memory.

---

## Core Concepts

Standard AI development tools treat LLMs like automated text editors. They require the model to navigate filesystems, calculate line offsets and manage patch blocks, which likely introduces unnecessary cognitive noise and wastes context tokens.

**gomcp** changes this dynamic through three design choices:

1. **In-Memory Operations:** The agent reads from and writes to an in-memory representation of the codebase. Changes do not touch the disk while the agent works.
2. **Declaration Isolation:** Instead of sending an entire file or package context to the LLM, the server isolates and exposes only the specific declaration (such as a single struct, function, or interface) required for the current task.
3. **Immediate Compiler Feedback:** Every write triggers a scoped re-typecheck in memory, sending compiler diagnostics directly back to the agent.


## Operational Characteristics

To optimize the tool for how Large Language Models process text, the server adheres to these specific operational behaviors:

### Human-Readable Source
While the backend manipulates Go's Abstract Syntax Tree (AST), **the model never encounters raw AST structures or JSON nodes**. LLMs are trained on standard source code, and forcing them to parse or generate structural tree nodes would likely require additional reasoning. **gomcp** exposes clean, human-readable Go source text at the declaration level, preserving the exact token relationships the model understands.

### The "Dirty Buffer" Sandbox
If an agent introduces a change that breaks typing rules, **gomcp retains the change**. Instead of rejecting invalid code, it updates the in-memory state like an IDE's unsaved buffer and returns the exact `go/types` diagnostics to the agent. This allows the model to perform multi-step writes where intermediate states are broken, using the compiler's output to iteratively correct its work. Additional refactoring tools exists for safe refactorings.

### Dependency-Aware Reads
The read tools aren't limited to the agent's own module. Any importable package (standard library or third-party) resolves through the same `list_*`/`describe_*` tools by import path, lazy loaded. Only the exported API is indexed, and dependencies are never mutable: they're workspace context, not workspace state.

### Server-Managed Imports
The agent writes identifiers, never import blocks. **gomcp** runs `goimports` on every write, and imports of packages that exist only in memory (invisible to disk-scanning tools) self-repair between rechecks. Import management is one thing the agent never has to think about.

### Opinionated Placement
The agent never specifies a line number or cursor position, only a declaration and its address. Where a *new* declaration lands within its destination file is decided by the server, following one consistent ordering: constants and variables come first, type definitions and their methods follow (methods resolving immediately after their receiver's existing method group or type declaration), and plain functions trail at the bottom of the file. The policy only governs where creations and relocations land, it doesn't alter a file you haven't touched. Adopting **gomcp** on an existing codebase means new growth follows this convention, layered on top of whatever organization was already there.


## Technical Footprint & Independence

* **Independent of any LSP:** This tool operates independently of `gopls` or any running Language Server Protocol instance. The underlying mutation engine handles source parsing, syntax isolation, and error reporting natively through Go's internal compiler packages.
* **Minimal Dependencies:** Two external dependencies: the MCP Go SDK and `golang.org/x/tools` (Go project's own tooling module).


## The Execution Loop

1. **Fetch:** The agent requests a specific declaration by name. **gomcp** extracts it from the in-memory AST and returns it as a plain text Go snippet.
2. **Mutate:** The agent submits a single write or a batch of writes directly to the target declarations.
3. **Type-Check:** **gomcp** applies the changes to the AST and evaluates the module state using `go/types`.
4. **Echo:** The server returns any compiler errors or diagnostics (new or resolved) directly to the agent to guide its next iteration.


---

## Tools
Small set of 25 tools:

### Read
* Enumerators (consistent sorted output): `list_packages`, `list_files`, `list_methods`, `list_symbols`
* Describers (symbol source, any kind — func, method, type, var, or const): `describe_packages`, `describe_files`, `describe_symbols`
* Finders (grep-like): `search_declarations_like`, `search_source`, `search_implementors`, `search_references`
* Diagnostics (full workspace diagnostics): `diagnostics_workspace`

### Write
* Creators (fail if the address already exists; cannot destroy code): `create_packages`, `create_files`, `create_symbols`
* Editors (fail if the address doesn't exist): `edit_symbols`, `edit_files`
* Deleters (noop if the address doesn't exist — deletion is idempotent, so a duplicate target across entries is harmless): `delete_symbols`, `delete_files`, `delete_packages`
* Refactorings (structure-preserving transformations; safe by construction — refuse rather than risk breaking the workspace): `refactor_move_symbol`, `refactor_move_file`, `refactor_move_package`
* Disk (syncs the in-memory state with disk): `disk_flush`, `disk_reload`

More on `internal/tools/tools.go`

---

## Installation

Requires Go 1.26.4 or newer (see `go.mod`). See `Makefile` for the target list

Point your MCP client at the built binary (or at `go run ./cmd/gomcp` directly) over stdio. `.mcp.json` in this repo's own root is a working example, used for **gomcp**'s own self-hosted development:

```json
{
  "mcpServers": {
    "gomcp": {
      "type": "stdio",
      "command": "go",
      "args": ["run", "./cmd/gomcp/main.go"],
      "env": {}
    }
  }
}
```

### Flags

* `-cwd <path>` — workspace root directory. Falls back to the
  `CLAUDE_WORKSPACE` environment variable, then the process's own
  working directory, if unset.
* `-verbose` — log `go/packages` loader output to stderr.
* `-diagnostics-limit <n>` — cap the diagnostics rendered per read/write
  tool call (default 20). The `diagnostics_workspace` tool itself always
  reports the full, uncapped inventory.
