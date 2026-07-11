package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pedropaccola/gomcp/internal/engine"
	"github.com/pedropaccola/gomcp/internal/tools"
)

const (
	Name         = "gomcp"
	Version      = "0.0.1"
	Instructions = "Structured Go coding tools operating on an in-memory AST of the workspace. " +
		"Never read, navigate, or modify Go source files (.go) through raw file I/O or shell " +
		"commands: the filesystem may be stale relative to this server's state. All reads and " +
		"writes must flow through these tools, so every change is AST-validated and answered " +
		"with compiler diagnostics. Diagnostics on read/write tools are scoped to what was " +
		"read or changed — an empty field means nothing wrong there, not that the whole " +
		"workspace is healthy; call diagnostics for the full inventory. Comments must attach " +
		"to a declaration or sit directly above a package clause: one floating between " +
		"declarations is not part of the tracked syntax tree and can silently vanish under a " +
		"later edit. reload discards every unflushed edit — call flush first if you want to " +
		"keep them. Prefer move_symbol over edit_symbol when renaming: move_symbol propagates " +
		"the rename to every resolved reference across the workspace automatically; " +
		"edit_symbol's replacement only changes the declaration itself, leaving every other " +
		"reference to the old name broken until a diagnostics call happens to catch it."
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	flagCwd := flag.String("cwd", "", "Workspace root directory")
	flagVerbose := flag.Bool("verbose", false, "Log go/packages loader output to stderr")
	flagDiagLimit := flag.Int("diagnostics-limit", 20, "Limit diagnostics rendered per read/write tool; the diagnostics tool always reports the full inventory.")
	flag.Parse()

	var cwd string
	switch {
	case *flagCwd != "":
		cwd = *flagCwd
		log.Printf("[Init] Using workspace root from command flag: %s", cwd)
	case os.Getenv("CLAUDE_WORKSPACE") != "":
		cwd = os.Getenv("CLAUDE_WORKSPACE")
		log.Printf("[Init] Using workspace root CLAUDE_WORKSPACE: %s", cwd)
	default:
		osCwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("Fatal: failed to determine process CWD: %v", err)
		}
		cwd = osCwd
		log.Printf("[Init] Using workspace root from CWD: %s", cwd)
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		log.Fatalf("[Fatal] Failed to resolve absolute path for %s: %v", cwd, err)
	}

	var logf func(string, ...any)
	if *flagVerbose {
		logf = log.Printf
	}

	eng := engine.NewEngine(absCwd, logf)

	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    Name,
			Title:   Name,
			Version: Version,
		},
		&mcp.ServerOptions{
			Instructions: Instructions,
			InitializedHandler: func(ctx context.Context, _ *mcp.InitializedRequest) {
				if err := eng.Bootstrap(ctx); err != nil {
					log.Fatalf("[Fatal] Failed to bootstrap: %v", err)
				}
			},
			// SubscribeHandler: func(context.Context, *mcp.SubscribeRequest) error
			// UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error
		},
	)

	tools.SetDiagLimit(*flagDiagLimit)
	tools.Register(server, eng)

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("[Fatal] Server execution stopped: %v", err)
	}
}
