package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// sessionFiles writes the MCP configuration and the settings of one session
// in a private directory outside the worktree, mode 0600, so the token is not
// in the command line and the agent cannot rewrite them. The returned
// function removes them; call it when the session ends.
func (rt *Runtime) sessionFiles(spec ports.SessionSpec, harnessID string) (files, func(), error) {
	dir := filepath.Join(rt.cfg.ConfigDir, harnessID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return files{}, nil, fmt.Errorf("create session config dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	f := files{mcp: filepath.Join(dir, "mcp.json"), settings: filepath.Join(dir, "settings.json")}

	mcp := map[string]any{"mcpServers": map[string]any{"kluzo": map[string]any{
		"type": "http", "url": spec.MCPEndpoint,
		"headers": map[string]string{"Authorization": "Bearer " + spec.MCPToken},
	}}}
	for path, doc := range map[string]any{f.mcp: mcp, f.settings: rt.settings(spec)} {
		data, err := json.MarshalIndent(doc, "", "  ")
		if err == nil {
			err = os.WriteFile(path, data, 0o600)
		}
		if err != nil {
			cleanup()
			return files{}, nil, fmt.Errorf("write %s: %w", filepath.Base(path), err)
		}
	}
	return f, cleanup, nil
}

// hookTimeoutSeconds is how long the harness waits for the guard before it
// treats the hook as failed, which blocks the tool.
const hookTimeoutSeconds = 10

// settings is the --settings document of the session: the PreToolUse hook that
// reports every tool call to the guard of the MCP server. onFailure: block
// keeps the guard closed when Kluzo does not answer (ADR 0024, item 6).
func (rt *Runtime) settings(spec ports.SessionSpec) map[string]any {
	return map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "*",
		"hooks": []any{map[string]any{
			"type": "http", "url": spec.HookEndpoint, "timeout": hookTimeoutSeconds, "onFailure": "block",
			"headers": map[string]string{"Authorization": "Bearer " + spec.MCPToken},
		}},
	}}}}
}
