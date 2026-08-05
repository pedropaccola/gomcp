# The 10 claims under test

Re-verified against the actual current text of `README.md`/`AGENTS.md`/`cmd/gomcp/main.go`
(2026-08-05) — not copied from the original plan draft, since the underlying files have changed
substantially since (a large directive-awareness and file/package/symbol responsibility refactor
landed between the plan being drafted and this benchmark actually being built). Tool count (28)
re-counted directly from `internal/tools/tools.go`'s `Register` and confirmed unchanged.

1. **Raw file/line editing wastes tokens on navigation/line-offset math ("cognitive noise").**
   > "Standard AI development tools treat LLMs like automated text editors. They require the model
   > to navigate filesystems, calculate line offsets and manage patch blocks, which likely
   > introduces unnecessary cognitive noise and wastes context tokens." — `README.md`, Core Concepts
2. **Changes never touch disk while the agent works (in-memory only until `disk_flush`).**
   > "In-Memory Operations: The agent reads from and writes to an in-memory representation of the
   > codebase. Changes do not touch the disk while the agent works." — `README.md`, Core Concepts
3. **Only the specific declaration needed is exposed to the model, not whole files/packages.**
   > "Declaration Isolation: Instead of sending an entire file or package context to the LLM, the
   > server isolates and exposes only the specific declaration (such as a single struct, function,
   > or interface) required for the current task." — `README.md`, Core Concepts
4. **Every write triggers a scoped re-typecheck; compiler diagnostics return every turn.**
   > "Immediate Compiler Feedback: Every write triggers a scoped re-typecheck in memory, sending
   > compiler diagnostics directly back to the agent." — `README.md`, Core Concepts
5. **Invalid code is *retained* (IDE-style unsaved buffer) rather than rejected.**
   > "If an agent introduces a change that breaks typing rules, gomcp retains the change. Instead
   > of rejecting invalid code, it updates the in-memory state like an IDE's unsaved buffer and
   > returns the exact go/types diagnostics to the agent." — `README.md`, The "Dirty Buffer" Sandbox
6. **`goimports` runs on every write; in-memory-only imports self-repair.**
   > "gomcp runs goimports on every write, and imports of packages that exist only in memory
   > (invisible to disk-scanning tools) self-repair between rechecks." — `README.md`,
   > Server-Managed Imports
7. **No line numbers/cursor positions ever — only declaration + address.**
   > "The agent never specifies a line number or cursor position, only a declaration and its
   > address." — `README.md`, Opinionated Placement
8. **`refactor_move_symbol`/`refactor_move_file`/`refactor_move_package` are "safe by
   construction" — renames propagate to every resolved reference automatically.**
   > "Refactorings (structure-preserving transformations; safe by construction — refuse rather
   > than risk breaking the workspace): refactor_move_symbol, refactor_move_file,
   > refactor_move_package." — `README.md`, Tools
9. **Small tool surface: 28 tools total.**
   > "Small set of 28 tools:" — `README.md`, Tools. Recounted directly against
   > `internal/tools/tools.go`'s `Register` (4 enumerators + 3 describers + 4 finders +
   > 4 diagnostics + 3 creators + 2 editors + 3 deleters + 3 refactorings + 2 disk = 28): confirmed
   > unchanged despite the interface-consistency work earlier in this repo's history.
10. **Creators fail if the address already exists (can't destroy code); deleters are idempotent.**
    > "Creators (fail if the address already exists; cannot destroy code): ... Deleters (noop if
    > the address doesn't exist — deletion is idempotent, so a duplicate target across entries is
    > harmless): ..." — `README.md`, Tools

## Admitted limitations (hold gomcp to these fairly, not ignore)

Quoted directly from `AGENTS.md`'s "Working on this repo from a connected gomcp session" section
and `cmd/gomcp/main.go`'s server `Instructions` — both re-read for this benchmark, not assumed
from the original plan:

- **Server state goes stale under any non-gomcp edit.** "the server's own state goes stale the
  moment you edit its source with other tools, and it serves the *running* binary's behavior, not
  your working tree." A `disk_reload` call refreshes it; only behavior/schema changes to the
  server itself require a reconnect.
- **`disk_flush` is mandatory before disk-reading tools/tests see changes, and is silently
  discarded on reconnect.** "reconnects discard any unflushed edit silently, same as
  `disk_reload`... `make test`/`make tidy`/`make vet` read disk, not the in-memory model — flush
  before trusting their output."
- **Tool schema can be stale relative to source until reconnect.** "the connected server's tool
  schema reflects the *running binary*, not source you just edited — parameter names can be stale
  until reconnect." (Directly observed and worked around multiple times during this repo's own
  self-hosted development history.)
- **Scoped write-diagnostics don't guarantee whole-workspace health.** From the server's own
  `Instructions`: "Diagnostics on write tools are scoped to what changed — an empty field means
  nothing wrong there, not that the whole workspace is healthy."
- **No proactive impact-site inventory — the agent must let mutation echoes surface consequence
  sites turn by turn.** "Plan for these tools cause-first, not site-first: file-editing habits say
  inventory every affected site up front (grep, skim, list), but here you plan only the
  declaration and signature changes in dependency order and let the mutation echoes enumerate the
  consequence sites — exactly, per transaction."
