package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// acpClient drives a local agent CLI over the Agent Client Protocol
// (JSON-RPC over stdio). One process per call: connect, do the work, kill.
// Simpler than a connection pool and these are user-initiated, low-frequency
// actions.
type acpClient struct {
	cfg Config
}

// LocalAgents is the ordered list of local CLI agents that can act as model
// sources under provider kind "acp". Claude/Codex/Gemini speak ACP; Grok uses
// headless CLI (see grokCLIClient) because it has no ACP server.
var LocalAgents = []string{"claude", "codex", "gemini", "grok"}

// codexACPAgentPackage is pinned for reproducible SI releases. The release
// workflow compares it with the ACP registry and blocks a release when the
// registry has moved, forcing the adapter upgrade through tests and review.
const codexACPAgentPackage = "@agentclientprotocol/codex-acp@1.1.5"

// LocalAgentLabel is a short UI/docs label for a local agent id.
func LocalAgentLabel(agent string) string {
	switch agent {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex CLI"
	case "gemini":
		return "Gemini CLI"
	case "grok":
		return "Grok CLI"
	default:
		return agent
	}
}

// acpCommand maps an agent name to the argv that starts its ACP server.
// claude/codex go through the npm adapters Zed maintains (npx auto-installs
// on first use and caches after); gemini ships ACP in the CLI itself.
// Grok is not ACP — callers must use grokCLIClient instead.
func acpCommand(agent string) ([]string, error) {
	switch agent {
	// @latest pins force npx to re-resolve the registry instead of serving a
	// stale cache entry — the adapters bundle their own agent core, so an old
	// cached adapter silently means an old model list.
	case "claude":
		return []string{"npx", "-y", "@agentclientprotocol/claude-agent-acp@latest"}, nil
	case "codex":
		return []string{"npx", "-y", codexACPAgentPackage}, nil
	case "gemini":
		return []string{"gemini", "--experimental-acp"}, nil
	default:
		return nil, fmt.Errorf("unsupported acp agent %q", agent)
	}
}

// scratchDirPrefix names the throwaway temp dirs used as ACP session cwd.
const scratchDirPrefix = "session-insight-llm-"

// IsScratchCWD reports whether cwd is one of this package's scratch temp
// dirs. Agent CLIs log their own session files for work done there, and
// those generation byproducts must not surface as user sessions.
func IsScratchCWD(cwd string) bool {
	return strings.Contains(cwd, scratchDirPrefix)
}

// AgentBinary is the executable whose presence gates showing a local agent
// as a provider candidate in the UI.
func AgentBinary(agent string) string {
	switch agent {
	case "claude", "codex", "gemini", "grok":
		return agent
	}
	return ""
}

// DetectACPAgents reports which supported local agent CLIs exist on this machine.
func DetectACPAgents() []string {
	var out []string
	for _, agent := range LocalAgents {
		if _, err := exec.LookPath(AgentBinary(agent)); err == nil {
			out = append(out, agent)
		}
	}
	return out
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("%s (code %d): %s", e.Message, e.Code, string(e.Data))
	}
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type acpConn struct {
	cmd   *exec.Cmd
	stdin *json.Encoder

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage

	// onChunk receives agent_message_chunk text during session/prompt.
	chunkMu sync.Mutex
	onChunk func(text string)

	stderrTail *tailBuffer
	done       chan struct{}
	tmpDir     string
}

// tailBuffer keeps the last N bytes of adapter stderr for error reporting.
type tailBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if len(t.data) > 4096 {
		t.data = t.data[len(t.data)-4096:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.data))
}

// childEnv strips Claude Code session markers so the adapter's
// nested-session guard doesn't refuse to start when this server itself was
// launched from inside a Claude Code session.
func childEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "CLAUDE_CODE_ENTRYPOINT=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func dialACP(ctx context.Context, agent string) (*acpConn, error) {
	argv, err := acpCommand(agent)
	if err != nil {
		return nil, err
	}
	// Empty scratch cwd: generation is text-only, the agent has no business
	// reading whatever directory the server happens to run from.
	tmpDir, err := os.MkdirTemp("", scratchDirPrefix)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = tmpDir
	cmd.Env = childEnv()
	setProcAttr(cmd)
	cmd.Cancel = func() error { return killProc(cmd) }
	// Even after the group kill, a straggler holding our stderr pipe must not
	// block Wait forever.
	cmd.WaitDelay = 3 * time.Second
	stderrTail := &tailBuffer{}
	cmd.Stderr = stderrTail

	stdin, err := cmd.StdinPipe()
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("start %s: %w", strings.Join(argv, " "), err)
	}

	conn := &acpConn{
		cmd:        cmd,
		stdin:      json.NewEncoder(stdin),
		pending:    make(map[int64]chan rpcMessage),
		stderrTail: stderrTail,
		done:       make(chan struct{}),
		tmpDir:     tmpDir,
	}

	go func() {
		defer close(conn.done)
		// Adapter messages are newline-delimited JSON; a full model list or
		// command list can exceed bufio.Scanner's default 64KB cap.
		reader := bufio.NewReaderSize(stdout, 1<<20)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				var msg rpcMessage
				if json.Unmarshal(line, &msg) == nil {
					conn.dispatch(msg)
				}
			}
			if err != nil {
				return
			}
		}
	}()

	return conn, nil
}

func (c *acpConn) close() {
	killProc(c.cmd)
	c.cmd.Wait()
	os.RemoveAll(c.tmpDir)
}

func (c *acpConn) dispatch(msg rpcMessage) {
	switch {
	case msg.Method != "" && msg.ID != nil:
		c.handleIncomingRequest(msg)
	case msg.Method != "":
		c.handleNotification(msg)
	case msg.ID != nil:
		var id int64
		if json.Unmarshal(*msg.ID, &id) != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

// handleIncomingRequest answers agent→client requests. The only one we
// expect is session/request_permission — always rejected, because AI
// features here are text-only and the agent must never run tools or touch
// files on our behalf.
func (c *acpConn) handleIncomingRequest(msg rpcMessage) {
	respond := func(result any, rpcErr *rpcError) {
		out := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(*msg.ID)}
		if rpcErr != nil {
			out["error"] = rpcErr
		} else {
			out["result"] = result
		}
		c.mu.Lock()
		c.stdin.Encode(out)
		c.mu.Unlock()
	}

	if msg.Method != "session/request_permission" {
		respond(nil, &rpcError{Code: -32601, Message: "method not supported by this client"})
		return
	}

	var params struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	json.Unmarshal(msg.Params, &params)
	for _, opt := range params.Options {
		if opt.Kind == "reject_once" || opt.Kind == "reject_always" {
			respond(map[string]any{
				"outcome": map[string]any{"outcome": "selected", "optionId": opt.OptionID},
			}, nil)
			return
		}
	}
	respond(map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil)
}

func (c *acpConn) handleNotification(msg rpcMessage) {
	if msg.Method != "session/update" {
		return
	}
	var params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}
	if params.Update.SessionUpdate != "agent_message_chunk" || params.Update.Content.Type != "text" {
		return
	}
	c.chunkMu.Lock()
	handler := c.onChunk
	c.chunkMu.Unlock()
	if handler != nil {
		handler(params.Update.Content.Text)
	}
}

func (c *acpConn) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	err := c.stdin.Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%s: write: %w", method, err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", method, ctx.Err())
	case <-c.done:
		return fmt.Errorf("%s: agent process exited: %s", method, c.stderrTail.String())
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("%s: %w", method, msg.Error)
		}
		if out != nil {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return fmt.Errorf("%s: parse result: %w", method, err)
			}
		}
		return nil
	}
}

// acpSession is the parsed session/new response: where models come from
// differs per agent (claude advertises `models`, codex a `configOptions`
// select), so both shapes are captured.
type acpSession struct {
	SessionID string `json:"sessionId"`
	Models    *struct {
		AvailableModels []struct {
			ModelID     string `json:"modelId"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"availableModels"`
		CurrentModelID string `json:"currentModelId"`
	} `json:"models"`
	ConfigOptions []struct {
		ID       string `json:"id"`
		Category string `json:"category"`
		Options  []struct {
			Value       string `json:"value"`
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"configOptions"`
	Modes *struct {
		CurrentModeID  string `json:"currentModeId"`
		AvailableModes []struct {
			ID string `json:"id"`
		} `json:"availableModes"`
	} `json:"modes"`
}

func (s *acpSession) modelList() []Model {
	// Modern adapters may expose both configOptions (base model plus separate
	// reasoning effort) and legacy models (one entry per model/effort pair).
	// Prefer configOptions so SI presents the same base-model list as Zed.
	for _, opt := range s.ConfigOptions {
		if opt.ID != "model" && opt.Category != "model" {
			continue
		}
		models := make([]Model, 0, len(opt.Options))
		for _, o := range opt.Options {
			models = append(models, Model{ID: o.Value, Label: o.Name, Description: o.Description})
		}
		return models
	}
	if s.Models != nil && len(s.Models.AvailableModels) > 0 {
		models := make([]Model, 0, len(s.Models.AvailableModels))
		for _, m := range s.Models.AvailableModels {
			models = append(models, Model{ID: m.ModelID, Label: m.Name, Description: m.Description})
		}
		return models
	}
	return nil
}

// modelConfigID returns the config option id carrying model selection, for
// agents that expose models via configOptions instead of `models`.
func (s *acpSession) modelConfigID() string {
	for _, opt := range s.ConfigOptions {
		if opt.ID == "model" || opt.Category == "model" {
			return opt.ID
		}
	}
	return ""
}

// safestModeID prefers a mode that restricts tool execution WITHOUT changing
// the agent's output behavior. We only want a JSON answer, so a true read-only
// mode is ideal. Crucially, "plan" mode is NOT acceptable: for coding agents
// (e.g. claude-code-acp) it turns the agent into a planner that refuses to
// produce direct output and instead writes plan files — which made insight
// generation fail. When no genuine read-only mode exists, fall back to the
// agent's default mode (empty), whose normal question-answering behavior is far
// safer for a tool-less text task than plan mode.
func (s *acpSession) safestModeID() string {
	if s.Modes == nil {
		return ""
	}
	available := make(map[string]bool, len(s.Modes.AvailableModes))
	for _, m := range s.Modes.AvailableModes {
		available[m.ID] = true
	}
	for _, want := range []string{"read-only", "readonly"} {
		if available[want] {
			return want
		}
	}
	return ""
}

func (c *acpClient) open(ctx context.Context, onStatus StatusFunc) (*acpConn, *acpSession, error) {
	if onStatus != nil {
		onStatus("启动适配器")
	}
	conn, err := dialACP(ctx, c.cfg.Agent)
	if err != nil {
		return nil, nil, err
	}
	if onStatus != nil {
		onStatus("初始化适配器")
	}

	initParams := map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{"readTextFile": false, "writeTextFile": false},
		},
	}
	if err := conn.call(ctx, "initialize", initParams, nil); err != nil {
		conn.close()
		return nil, nil, fmt.Errorf("ACP initialize failed: %w", err)
	}

	if onStatus != nil {
		onStatus("创建模型会话")
	}
	var sess acpSession
	newParams := map[string]any{"cwd": conn.tmpDir, "mcpServers": []any{}}
	if err := conn.call(ctx, "session/new", newParams, &sess); err != nil {
		conn.close()
		return nil, nil, fmt.Errorf("ACP session/new failed: %w", err)
	}
	return conn, &sess, nil
}

func (c *acpClient) ListModels(ctx context.Context) ([]Model, error) {
	conn, sess, err := c.open(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer conn.close()

	models := sess.modelList()
	if len(models) == 0 {
		return nil, fmt.Errorf("agent %q did not advertise any models over ACP", c.cfg.Agent)
	}
	return models, nil
}

func (c *acpClient) Generate(ctx context.Context, prompt string, onStatus StatusFunc) (string, error) {
	if c.cfg.ModelID == "" {
		return "", fmt.Errorf("no model selected for this provider")
	}
	conn, sess, err := c.open(ctx, onStatus)
	if err != nil {
		return "", err
	}
	defer conn.close()

	if mode := sess.safestModeID(); mode != "" && sess.Modes != nil && sess.Modes.CurrentModeID != mode {
		if onStatus != nil {
			onStatus("设置安全执行模式")
		}
		conn.call(ctx, "session/set_mode",
			map[string]any{"sessionId": sess.SessionID, "modeId": mode}, nil)
	}

	if onStatus != nil {
		onStatus("选择模型")
	}
	if err := c.applyModel(ctx, conn, sess); err != nil {
		return "", err
	}

	if onStatus != nil {
		onStatus("提交生成请求")
	}
	var text strings.Builder
	var receivedOutput bool
	conn.chunkMu.Lock()
	conn.onChunk = func(chunk string) {
		if !receivedOutput && chunk != "" {
			receivedOutput = true
			if onStatus != nil {
				onStatus("接收模型输出")
			}
		}
		text.WriteString(chunk)
	}
	conn.chunkMu.Unlock()

	var result struct {
		StopReason string `json:"stopReason"`
	}
	promptParams := map[string]any{
		"sessionId": sess.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	}
	if err := conn.call(ctx, "session/prompt", promptParams, &result); err != nil {
		return "", err
	}
	if onStatus != nil {
		onStatus("整理模型结果")
	}

	output := strings.TrimSpace(text.String())
	if output == "" {
		return "", fmt.Errorf("agent returned empty content (stop reason: %s; stderr: %s)",
			result.StopReason, conn.stderrTail.String())
	}
	if result.StopReason == "refusal" || result.StopReason == "cancelled" {
		return "", fmt.Errorf("agent did not complete the request (stop reason: %s)", result.StopReason)
	}
	return output, nil
}

// applyModel sets the user's explicit model choice on the fresh session,
// whichever selection mechanism the agent exposes. Failing to set the model
// is an error — silently generating with the agent's default is exactly the
// behavior this feature exists to avoid.
func (c *acpClient) applyModel(ctx context.Context, conn *acpConn, sess *acpSession) error {
	method, params, err := c.modelSelectionRequest(sess)
	if err != nil || method == "" {
		return err
	}
	return conn.call(ctx, method, params, nil)
}

func (c *acpClient) modelSelectionRequest(sess *acpSession) (string, map[string]any, error) {
	models := sess.modelList()
	if sess.modelConfigID() != "" || sess.Models != nil {
		available := make([]string, 0, len(models))
		selected := false
		for _, model := range models {
			available = append(available, model.ID)
			if model.ID == c.cfg.ModelID {
				selected = true
			}
		}
		if !selected {
			return "", nil, &ModelUnavailableError{
				ModelID: c.cfg.ModelID, Agent: c.cfg.Agent, Available: available,
			}
		}
	}
	if configID := sess.modelConfigID(); configID != "" {
		return "session/set_config_option",
			map[string]any{"sessionId": sess.SessionID, "configId": configID, "value": c.cfg.ModelID}, nil
	}
	if sess.Models != nil {
		if sess.Models.CurrentModelID == c.cfg.ModelID {
			return "", nil, nil
		}
		return "session/set_model",
			map[string]any{"sessionId": sess.SessionID, "modelId": c.cfg.ModelID}, nil
	}
	return "", nil, fmt.Errorf("agent %q offers no model selection over ACP; refresh the model list", c.cfg.Agent)
}
