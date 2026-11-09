package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/instaclustr/labs/insta/internal/assistant"
	"github.com/instaclustr/labs/insta/internal/coding"
	"github.com/instaclustr/labs/insta/internal/config"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/runner"
	"github.com/instaclustr/labs/insta/internal/state"
	"golang.org/x/term"
)

// Exec deliberately takes no project lock. Codex and the limited assistant call
// the normal CLI runner, which acquires a fresh scoped lock for each mutation.
func (s *session) Exec(ctx context.Context, options execOptions) error {
	instruction, taskID := options.instruction, options.taskID
	if s.App.JSON {
		return errors.New("insta exec does not support --json")
	}
	if len(instruction) > maxExecPrompt {
		return errors.New("initial instruction exceeds 8192 bytes")
	}
	if taskID == "" {
		taskID = s.Project.Stack().ActiveTask
	}
	var blueprint string
	if taskID != "" {
		task, ok := s.Manifest.Tasks[taskID]
		if !ok {
			return fmt.Errorf("unknown task %q; run insta task list", taskID)
		}
		blueprint = task.Blueprint
	}
	var adapter coding.CodingCLI
	switch s.Values["agent.backend"] {
	case "", "codex":
		adapter = s.App.CodingCLI
		if adapter == nil {
			adapter = coding.NewCodex()
		}
	case "claude-code":
		// Deliberate selection of an unimplemented backend never silently chooses
		// a different provider or the API fallback.
		return &coding.NotImplementedError{Provider: "claude-code"}
	default:
		return errors.New("unknown coding backend; configure agent.backend as codex")
	}
	readiness, err := adapter.Probe(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Non-interactive Codex can use a provider profile or CODEX_API_KEY without
	// a stored login. Let the actual child report provider/authentication errors.
	ready := readiness.State == coding.Ready && readiness.Configured
	if err == nil && (ready || (options.mode != execInteractive && readiness.Installed)) {
		req := coding.SessionRequest{
			Workspace: s.Root, Instructions: s.Manifest.Instructions,
			Instruction: s.redactAgent(instruction), TaskID: taskID, Blueprint: blueprint,
			Environment:     config.TaskEnvironment(s.Manifest, s.Values, s.Root, runner.Venv(s.Root, s.Project.SelectedStack)),
			InstaExecutable: s.App.Executable, Stdout: s.App.Out, Stderr: s.App.Err, Profile: options.profile,
		}
		if options.approve {
			req.TaskApprovalScope, err = s.taskApprovalScope()
			if err != nil {
				return err
			}
		}
		if options.mode == execInteractive {
			req.Stdin = s.App.In
			return codingExit(adapter.RunInteractive(ctx, req))
		}
		if options.mode == execPrompt {
			req.OneShot = true
			_, err := s.runPrompt(ctx, adapter, req, "")
			return err
		}
		return s.promptLoop(ctx, adapter, req)
	}
	if readiness.Diagnostic != "" {
		fmt.Fprintln(s.App.Err, s.redactAgent(readiness.Diagnostic))
	}
	if options.approve {
		return errors.New("insta exec --approve requires Codex; install Codex and configure it for the selected mode")
	}
	if options.mode != execInteractive {
		return errors.New("Codex is unavailable for non-interactive execution; install Codex and run codex login")
	}
	fmt.Fprintln(s.App.Err, coding.Warning)
	if s.Values["assistant.api_key"] == "" || s.Values["assistant.model"] == "" {
		return errors.New("no guided session started; run codex login after installing Codex, or configure assistant.api_key and assistant.model; insta task commands remain available")
	}
	if !agentTerminal(s.App.In, s.App.Out) {
		return errors.New("insta exec requires an interactive terminal for explicit API-assistance consent; use insta task for noninteractive commands")
	}
	answer, err := s.App.Ask("Limited assistance sends your question and selected task/source text (up to 16 KiB per file) to OpenAI and may incur API charges. Use it for this session? [y/N]", false)
	if err != nil {
		return err
	}
	if !affirmative(answer) {
		return errors.New("limited API assistance was not selected; insta task commands remain available")
	}
	client := assistant.Client{APIKey: s.Values["assistant.api_key"], Model: s.Values["assistant.model"]}
	for {
		if instruction == "" {
			instruction, err = s.App.Ask("Ask about this lab (or type exit):", false)
			if err != nil {
				return err
			}
		}
		if strings.EqualFold(strings.TrimSpace(instruction), "exit") {
			return nil
		}
		if strings.TrimSpace(instruction) == "" {
			instruction = ""
			continue
		}
		question := instruction
		if taskID != "" {
			question = "Selected declared task: " + taskID + ". Read its blueprint using read_task when useful.\n" + instruction
		}
		err = client.Run(ctx, assistant.Request{
			Consent: true, Instruction: s.redactAgent(question), Handle: s.assistantTool,
			Output: s.App.Out, Progress: s.App.Err, Redact: s.redactAgent,
		})
		if err != nil {
			return err
		}
		instruction = ""
	}
}

func affirmative(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

func agentTerminal(in io.Reader, out io.Writer) bool {
	i, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(i.Fd())) {
		return false
	}
	o, ok := out.(*os.File)
	return ok && term.IsTerminal(int(o.Fd()))
}

func (s *session) redactAgent(text string) string {
	if s.Redactor != nil {
		return s.Redactor.Text(text)
	}
	return runner.NewRedactor(config.SecretValues(s.Manifest, s.Values)).Text(text)
}

func (s *session) assistantTool(ctx context.Context, call assistant.ToolCall) (string, error) {
	// Revalidate changed executable metadata before each tool, and use current
	// files rather than assuming the launch snapshot remains authoritative.
	m, hash, err := manifest.Load(s.Root)
	if err != nil {
		return "", err
	}
	current, err := state.Load(s.Root)
	if err != nil {
		return "", err
	}
	if current.ID != s.Project.ID || current.SelectedStack != s.Project.SelectedStack {
		return "", errors.New("project identity or selected stack changed; restart insta exec before continuing")
	}
	previousRedactor := s.Redactor
	s.Project, s.Manifest, s.Hash = current, m, hash
	if err := s.Refresh(); err != nil {
		return "", err
	}
	s.Redactor = previousRedactor.WithSecrets(config.SecretValues(s.Manifest, s.Values))
	switch call.Name {
	case "list_tasks":
		tasks := []map[string]any{}
		for _, id := range manifest.Keys(m.Tasks) {
			t := m.Tasks[id]
			tasks = append(tasks, map[string]any{
				"id": id, "blueprint": t.Blueprint, "source": t.Source,
				"operations": manifest.Keys(t.Operations), "requires": t.Requires,
			})
		}
		return s.agentJSON(tasks)
	case "read_task":
		t, ok := m.Tasks[call.TaskID]
		if !ok {
			return "", errors.New("requested task is not declared")
		}
		return s.readAssistantSource(m, t.Blueprint)
	case "read_source":
		return s.readAssistantSource(m, call.RelativePath)
	case "get_lab_status":
		services := []map[string]any{}
		for _, name := range manifest.Keys(current.Stack().Services) {
			record := current.Stack().Services[name]
			status, checkErr := runner.Control(ctx, record, "status")
			entry := map[string]any{"name": name, "owned_process_running": checkErr == nil && status.Running, "ready": false}
			if checkErr != nil {
				entry["observation"] = "owned process could not be reached"
			} else if def, ok := m.Services[name]; ok && status.Running {
				entry["ready"] = runner.CheckProbe(ctx, def.Probe) == nil
			}
			services = append(services, entry)
		}
		platforms := []map[string]any{}
		for _, name := range manifest.Keys(current.Stack().Platforms) {
			record := current.Stack().Platforms[name]
			platforms = append(platforms, map[string]any{
				"name": name, "provider": record.Provider, "id": record.ID,
				"owned": record.Owned, "recorded_status": record.Status,
			})
		}
		return s.agentJSON(map[string]any{
			"lab": current.Lab, "stack": current.SelectedStack, "active_task": current.Stack().ActiveTask,
			"recorded_runtime_status": current.Stack().RuntimeStatus, "platform_records": platforms,
			"observed_services": services, "recorded_operations": current.Stack().Operations,
		})
	case "run_task_operation":
		t, ok := m.Tasks[call.TaskID]
		if !ok {
			return "", errors.New("requested task is not declared")
		}
		op, ok := t.Operations[call.Operation]
		if !ok {
			return "", errors.New("requested operation is not declared")
		}
		if op.Mode == "interactive" {
			return "", errors.New("limited assistance cannot run an interactive task program; run it directly with insta task run")
		}
		// The normal runner displays effects, checks credentials/prerequisites,
		// confirms the operation, and acquires its own short-lived project lock.
		observed, err := s.App.RunTaskAt(ctx, s.Root, call.TaskID, call.Operation)
		if err != nil {
			return "", err
		}
		return s.agentJSON(observed)
	default:
		return "", errors.New("requested assistant tool is not allowed")
	}
}

func (s *session) agentJSON(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("encode assistant tool result")
	}
	return s.redactAgent(string(b)), nil
}

// sourceAllowed allows declared code and teaching material, not arbitrary files
// under the project. Both the requested path and symlink-resolved target must
// pass this check, preventing a README symlink from exposing .insta or .env.
func sourceAllowed(m *manifest.Manifest, relative string) bool {
	if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || strings.ContainsAny(relative, "\\\x00\r\n:") {
		return false
	}
	for _, part := range strings.Split(strings.ToLower(relative), "/") {
		if strings.HasPrefix(part, ".") || part == "" {
			return false
		}
		switch part {
		case "node_modules", "__pycache__", "venv", "logs", "log", "data", "datasets", "dataset", "recipes", "cache", "output", "outputs", "dist", "build", "credentials", "secrets":
			return false
		}
	}
	base := strings.ToLower(path.Base(relative))
	if strings.Contains(base, "credential") || strings.Contains(base, "secret") || strings.HasPrefix(base, "env.") || base == "env" || strings.Contains(base, "token") || strings.Contains(base, "policy_history") {
		return false
	}
	if relative == m.Instructions || relative == "INSTA.md" || relative == "README.md" || relative == "insta.yaml" || relative == m.Runtime.Requirements {
		return true
	}
	for _, t := range m.Tasks {
		if relative == t.Blueprint {
			return true
		}
	}
	ext := strings.ToLower(path.Ext(relative))
	if ext == ".md" {
		for _, prefix := range []string{"tasks/", "concepts/", "references/", "troubleshooting/"} {
			if strings.HasPrefix(relative, prefix) {
				return true
			}
		}
	}
	if relative == "references/lab.json" || relative == "references/lab.schema.json" {
		return true
	}
	if ext != ".py" && ext != ".go" && ext != ".rs" && ext != ".js" && ext != ".ts" {
		return false
	}
	if strings.HasPrefix(relative, "support/common/") {
		return true
	}
	for _, t := range m.Tasks {
		if strings.HasPrefix(relative, strings.TrimSuffix(t.Source, "/")+"/") {
			return true
		}
	}
	return false
}

func (s *session) readAssistantSource(m *manifest.Manifest, relative string) (string, error) {
	if !sourceAllowed(m, relative) {
		return "", errors.New("source read denied: choose a declared task blueprint or approved source/documentation file")
	}
	real, err := manifest.Path(s.Root, relative, false)
	if err != nil {
		return "", errors.New("source read denied: file must stay inside this project")
	}
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return "", errors.New("resolve project for source read")
	}
	resolved, err := filepath.Rel(root, real)
	if err != nil || !sourceAllowed(m, filepath.ToSlash(resolved)) {
		return "", errors.New("source read denied: symlink target is outside approved source/documentation")
	}
	f, err := os.Open(real)
	if err != nil {
		return "", errors.New("open approved source file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("source read requires a regular text file")
	}
	const limit = 16 << 10
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || bytesContainNUL(b) {
		return "", errors.New("source read requires valid text")
	}
	truncated := len(b) > limit
	if truncated {
		b = b[:limit]
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
			// A split UTF-8 codepoint needs at most three bytes removed. If the
			// file contains invalid bytes elsewhere, reject it below.
			if len(b) < limit-3 {
				return "", errors.New("source read requires UTF-8 text")
			}
		}
	}
	if !utf8.Valid(b) {
		return "", errors.New("source read requires UTF-8 text")
	}
	text := s.redactAgent(string(b))
	if truncated {
		text += "\n[File truncated at 16 KiB; inspect the linked source for a focused follow-up.]"
	}
	return text, nil
}

func bytesContainNUL(b []byte) bool {
	for _, value := range b {
		if value == 0 {
			return true
		}
	}
	return false
}
