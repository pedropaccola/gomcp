package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestRegister exercises schema generation for every declared tool shape —
// it panics or errors inside the SDK if a shape (e.g. the embedded DiagBlock)
// cannot be turned into a JSON schema.
func TestRegister(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(server, sandboxStore(t), 20)
}

// TestToolAnnotations asserts the annotations exactly as a client sees them
// over the wire: the workspace is a closed world, reads are read-only, and
// only Creators are non-destructive among the mutators.
func TestToolAnnotations(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(server, sandboxStore(t), 20)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools listed")
	}
	for _, tool := range tools.Tools {
		ann := tool.Annotations
		if ann == nil {
			t.Errorf("%s: no annotations", tool.Name)
			continue
		}
		if ann.Title == "" {
			t.Errorf("%s: missing title", tool.Name)
		}
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Errorf("%s: workspace is a closed world", tool.Name)
		}
		if !ann.IdempotentHint {
			t.Errorf("%s: retries never double-apply; must be idempotent", tool.Name)
		}
		isRead := strings.HasPrefix(tool.Name, "list_") || strings.HasPrefix(tool.Name, "describe_") ||
			strings.HasPrefix(tool.Name, "search_") || strings.HasPrefix(tool.Name, "diagnostics_")
		if ann.ReadOnlyHint != isRead {
			t.Errorf("%s: ReadOnlyHint = %v", tool.Name, ann.ReadOnlyHint)
		}
		wantDestructive := !isRead && !strings.HasPrefix(tool.Name, "create_")
		if ann.DestructiveHint == nil || *ann.DestructiveHint != wantDestructive {
			t.Errorf("%s: DestructiveHint = %v, want %v", tool.Name, ann.DestructiveHint, wantDestructive)
		}
	}
}
