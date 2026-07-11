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
	Name         = "mcpgo"
	Version      = "1.0.0"
	Instructions = "Structured Go coding tools operating on an in-memory AST of the workspace. " +
		"Never read, navigate, or modify Go source files (.go) through raw file I/O or shell " +
		"commands: the filesystem may be stale relative to this server's state. All lookups " +
		"and mutations must flow through these tools, so every change is AST-validated and " +
		"answered with compiler diagnostics."
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	flagCwd := flag.String("cwd", "", "Workspace root directory")
	flagVerbose := flag.Bool("verbose", false, "Log go/packages loader output to stderr")
	flagDiagLimit := flag.Int("diagnostics-limit", 20, "Limit diagnostics rendered per scoped list_*/describe_* and mutation echo; the diagnostics tool itself always reports the full inventory.")
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
