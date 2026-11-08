package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// fakeClaude is a stand-in for the claude CLI. It replays a fixture of
// testdata/ as the stream-json output, so the adapter is tested without a
// network, a credential or a model. The fixture is named by KLUZO_FAKE_FIXTURE.
//
// A fixture line that starts with "!" is a directive; any other line is
// printed as it is, after replacing the placeholders:
//
//	@@SESSION@@  the id given with --session-id or --resume
//	@@TOOLS@@    the JSON list of the tools the CLI would announce: --tools plus
//	             the MCP tools of the session
//
// Directives:
//
//	!read-prompt       read the first stdin line (the task)
//	!wait-input        read one more stdin line and print it as assistant text
//	!deliver <json>    call kluzo_result on the MCP server named in --mcp-config
//	!stderr <text>     write to stderr
//	!sleep <duration>  wait
//	!exit <code>       end the process
//	!hang              never end (until a signal)
//	!ignore-term       ignore SIGTERM from now on
//	!grandchild        start a child that outlives its parent, and print its pid to stderr
//
// The fake also plays the harness for the guard hook: after an assistant line
// with tool_use blocks, it reads the PreToolUse hook from --settings, posts each
// tool call there with the body Claude Code sends, and prints the
// hook_started and hook_response events that --include-hook-events adds, in
// the recorded format (they name the tool in hook_name, not the tool_use id).
// KLUZO_FAKE_NOHOOK=1 makes it ignore the settings, as a CLI that dropped them
// would. KLUZO_FAKE_HOOKLOG names a file that receives one
// "<tool_use_id> <outcome>" line per call (outcome "allow", "deny" or
// "blocked" when the hook failed and onFailure is "block").
//
// KLUZO_FAKE_UNKNOWN lists flags the fake rejects as an unknown option.
//
// With KLUZO_FAKE_RECORD set, the CLI first writes its argv, environment,
// working directory and the files named by --mcp-config, --settings and
// --append-system-prompt-file there.
func fakeClaude(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "--version":
			fmt.Println(envOr("KLUZO_FAKE_VERSION", "2.1.319") + " (Claude Code)")
			return 0
		case "--help":
			help := "Usage: claude [options]\n"
			for _, f := range strings.Fields(os.Getenv("KLUZO_FAKE_FLAGS")) {
				help += "  " + f + "\n"
			}
			fmt.Print(help)
			return 0
		case "auth":
			if os.Getenv("KLUZO_FAKE_LOGGED_OUT") == "1" {
				fmt.Println(`{"loggedIn": false}`)
				return 1
			}
			fmt.Println(`{"loggedIn": true}`)
			return 0
		}
	}
	for _, a := range args {
		if slices.Contains(strings.Fields(os.Getenv("KLUZO_FAKE_UNKNOWN")), a) {
			fmt.Fprintf(os.Stderr, "error: unknown option '%s'\n", a)
			return 1
		}
		if a == "--append-system-prompt-file" && os.Getenv("KLUZO_FAKE_FIXTURE") == "" {
			fmt.Fprintln(os.Stderr, "Error: Append system prompt file not found")
			return 1
		}
	}
	if rec := os.Getenv("KLUZO_FAKE_RECORD"); rec != "" {
		record(rec, args)
	}
	fixture, err := os.ReadFile(filepath.Join(os.Getenv("KLUZO_FAKE_TESTDATA"), os.Getenv("KLUZO_FAKE_FIXTURE")+".jsonl"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake claude:", err)
		return 2
	}
	stdin := bufio.NewReader(os.Stdin)
	for _, ln := range strings.Split(strings.TrimRight(string(fixture), "\n"), "\n") {
		if ln == "" {
			continue
		}
		if !strings.HasPrefix(ln, "!") {
			ln = substitute(ln, args)
			fmt.Println(ln)
			runHooks(ln, args)
			continue
		}
		verb, rest, _ := strings.Cut(strings.TrimPrefix(ln, "!"), " ")
		if code, done := directive(verb, rest, args, stdin); done {
			return code
		}
	}
	return 0
}

// directive runs one directive; done is true when the process must end.
func directive(verb, rest string, args []string, stdin *bufio.Reader) (code int, done bool) {
	switch verb {
	case "read-prompt":
		_, _ = stdin.ReadString('\n')
	case "wait-input":
		l, _ := stdin.ReadString('\n')
		fmt.Println(assistantText("got: " + userText(l)))
	case "deliver":
		deliver(rest, args)
	case "stderr":
		fmt.Fprintln(os.Stderr, rest)
	case "sleep":
		d, _ := time.ParseDuration(rest)
		time.Sleep(d)
	case "exit":
		var c int
		_, _ = fmt.Sscan(rest, &c)
		return c, true
	case "hang":
		time.Sleep(time.Hour)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
	case "grandchild":
		fmt.Fprintln(os.Stderr, "grandchild", startGrandchild())
	}
	return 0, false
}

func startGrandchild() int {
	p, err := os.StartProcess("/bin/sleep", []string{"sleep", "60"}, &os.ProcAttr{Files: []*os.File{nil, nil, nil}})
	if err != nil {
		return -1
	}
	return p.Pid
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func substitute(ln string, args []string) string {
	id := flagValue(args, "--session-id")
	if id == "" {
		id = flagValue(args, "--resume")
	}
	tools := append(strings.Split(flagValue(args, "--tools"), ","), "mcp__kluzo__kluzo_result")
	b, _ := json.Marshal(tools)
	return strings.NewReplacer("@@SESSION@@", id, "@@TOOLS@@", string(b)).Replace(ln)
}

func assistantText(text string) string {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"id": "msg_input", "content": []map[string]string{{"type": "text", "text": text}},
	}})
	return string(b)
}

func userText(line string) string {
	var m struct {
		Message struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal([]byte(line), &m)
	if len(m.Message.Content) == 0 {
		return ""
	}
	return m.Message.Content[0].Text
}

// deliver makes the MCP call an agent makes to hand in its result.
func deliver(arguments string, args []string) {
	cfg, err := os.ReadFile(flagValue(args, "--mcp-config"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake claude: mcp config:", err)
		return
	}
	var doc struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	_ = json.Unmarshal(cfg, &doc)
	srv := doc.Servers["kluzo"]
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kluzo_result","arguments":` + arguments + `}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range srv.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake claude: deliver:", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	fmt.Fprintln(os.Stderr, "deliver", resp.StatusCode, strings.TrimSpace(string(out)))
}

// record writes what the CLI was given, for the tests to inspect.
func record(path string, args []string) {
	wd, _ := os.Getwd()
	info := map[string]any{"argv": args, "env": os.Environ(), "cwd": wd}
	for _, f := range []string{"--mcp-config", "--settings", "--append-system-prompt-file"} {
		file := flagValue(args, f)
		if st, err := os.Stat(file); err == nil {
			data, _ := os.ReadFile(file)
			info[f] = map[string]any{"path": file, "mode": st.Mode().Perm().String(), "content": string(data)}
		}
	}
	b, _ := json.Marshal(info)
	_ = os.WriteFile(path, b, 0o600)
}

// runHooks reports the tool_use blocks of an assistant line to the hook.
func runHooks(line string, args []string) {
	if os.Getenv("KLUZO_FAKE_NOHOOK") == "1" {
		return
	}
	var l struct {
		Type    string
		Message struct{ Content []block } `json:"message"`
	}
	if json.Unmarshal([]byte(line), &l) != nil || l.Type != "assistant" {
		return
	}
	settings, err := os.ReadFile(flagValue(args, "--settings"))
	if err != nil {
		return
	}
	var doc struct {
		Hooks struct {
			PreToolUse []struct {
				Hooks []struct {
					URL       string            `json:"url"`
					Headers   map[string]string `json:"headers"`
					OnFailure string            `json:"onFailure"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if json.Unmarshal(settings, &doc) != nil || len(doc.Hooks.PreToolUse) == 0 || len(doc.Hooks.PreToolUse[0].Hooks) == 0 {
		return
	}
	hook := doc.Hooks.PreToolUse[0].Hooks[0]
	session := flagValue(args, "--session-id") + flagValue(args, "--resume")
	for _, b := range l.Message.Content {
		if b.Type != "tool_use" {
			continue
		}
		hookEvent := func(subtype string, extra map[string]any) {
			ev := map[string]any{"type": "system", "subtype": subtype, "hook_id": "hook-" + b.ID,
				"hook_name": "PreToolUse:" + b.Name, "hook_event": "PreToolUse", "uuid": "uuid-" + subtype + "-" + b.ID, "session_id": session}
			for k, v := range extra {
				ev[k] = v
			}
			out, _ := json.Marshal(ev)
			fmt.Println(string(out))
		}
		hookEvent("hook_started", nil)
		outcome, output := callHook(hook.URL, hook.Headers, hook.OnFailure, session, b)
		hookEvent("hook_response", map[string]any{"output": output, "stdout": output, "stderr": "", "exit_code": 200, "outcome": "success"})
		if log := os.Getenv("KLUZO_FAKE_HOOKLOG"); log != "" {
			f, _ := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			_, _ = fmt.Fprintf(f, "%s %s\n", b.ID, outcome)
			_ = f.Close()
		}
	}
}

// callHook posts the PreToolUse body Claude Code 2.1.319 sends
// and returns the outcome and the raw answer.
func callHook(url string, headers map[string]string, onFailure, session string, b block) (string, string) {
	wd, _ := os.Getwd()
	body, _ := json.Marshal(map[string]any{
		"session_id": session, "transcript_path": "/home/.claude/projects/x/" + session + ".jsonl", "cwd": wd,
		"prompt_id": "prompt-1", "permission_mode": "dontAsk", "effort": map[string]string{"level": "high"},
		"hook_event_name": "PreToolUse", "tool_name": b.Name, "tool_input": b.Input, "tool_use_id": b.ID,
	})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	failed := func() (string, string) {
		if onFailure == "block" {
			return "blocked", ""
		}
		return "allow-on-failure", ""
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return failed()
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Hook struct {
			Decision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil {
		return failed()
	}
	if out.Hook.Decision == "deny" {
		return "deny", string(raw)
	}
	return "allow", string(raw)
}
