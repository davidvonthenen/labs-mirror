// Package coding adapts a coding CLI without owning lab lifecycle.
package coding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

type State string

const (
	Missing      State = "missing"
	Unconfigured State = "unconfigured"
	Ready        State = "ready"
	Unsupported  State = "unsupported"
	Uncertain    State = "uncertain"
)

const Warning = "Codex is unavailable or not configured; install it and sign in for the best guided learning and coding experience. Basic lab commands still work, and separately configured OpenAI assistance can explain exercises and request declared task operations."

// TaskApprovalEnv carries the scope that a nested insta task run must match.
const TaskApprovalEnv = "INSTA_EXEC_TASK_APPROVAL"

type Readiness struct {
	State      State  `json:"state"`
	Installed  bool   `json:"installed"`
	Configured bool   `json:"configured"`
	Version    string `json:"version,omitempty"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

type SessionRequest struct {
	Profile           string
	TaskApprovalScope string
	OneShot           bool
	Workspace         string
	Instruction       string
	TaskID            string
	Blueprint         string
	Instructions      string
	InstaExecutable   string
	Environment       []string
	Stdin             io.Reader
	Stdout            io.Writer
	Stderr            io.Writer
}

type CodingCLI interface {
	Name() string
	Probe(context.Context) (Readiness, error)
	RunInteractive(context.Context, SessionRequest) error
	RunPrompt(context.Context, SessionRequest, string) (string, error)
}

type NotImplementedError struct{ Provider string }

func (e *NotImplementedError) Error() string { return e.Provider + " integration is not implemented" }

// ChildExitError retains the native process exit code independently of CLI errors.
type ChildExitError struct{ Code int }

func (e *ChildExitError) Error() string { return fmt.Sprintf("Codex exited with status %d", e.Code) }

type ClaudeCode struct{}

func (ClaudeCode) Name() string { return "claude-code" }
func (ClaudeCode) Probe(context.Context) (Readiness, error) {
	err := &NotImplementedError{Provider: "claude-code"}
	return Readiness{State: Unsupported, Diagnostic: err.Error()}, err
}
func (ClaudeCode) RunInteractive(context.Context, SessionRequest) error {
	return &NotImplementedError{Provider: "claude-code"}
}

func (ClaudeCode) RunPrompt(context.Context, SessionRequest, string) (string, error) {
	return "", &NotImplementedError{Provider: "claude-code"}
}

// Codex has optional diagnostic settings for local executable fixtures. The CLI
// uses NewCodex, which locates codex on PATH and bounds each diagnostic to 5s.
type Codex struct {
	Executable   string
	ProbeTimeout time.Duration
	isTerminal   func(io.Reader, io.Writer) bool
}

func NewCodex() *Codex      { return &Codex{} }
func (*Codex) Name() string { return "codex" }

func (c *Codex) executable() (string, error) {
	name := c.Executable
	if name == "" {
		name = "codex"
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

var versionPattern = regexp.MustCompile(`(?m)\bcodex(?:-cli)?\s+v?([0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?)\b`)

func (c *Codex) Probe(ctx context.Context) (Readiness, error) {
	p, err := c.executable()
	if err != nil {
		return Readiness{State: Missing, Diagnostic: "codex is not on PATH; install Codex, then run codex login"}, nil
	}
	r := Readiness{Installed: true, State: Uncertain}
	out, code, err := c.diagnostic(ctx, p, "--version")
	if err != nil {
		r.Diagnostic = "codex --version did not complete; check the executable and retry"
		return r, nil
	}
	if code != 0 {
		r.Diagnostic = "codex --version failed; update Codex and retry"
		if unsupportedDiagnostic(out) {
			r.State = Unsupported
		}
		return r, nil
	}
	m := versionPattern.FindStringSubmatch(out)
	if len(m) != 2 {
		r.Diagnostic = "codex --version returned an unrecognized version format; check the installed executable"
		return r, nil
	}
	r.Version = m[1]
	out, code, err = c.diagnostic(ctx, p, "login", "status")
	if err != nil {
		r.Diagnostic = "codex login status did not complete; run that command in your terminal"
		return r, nil
	}
	if code == 0 {
		r.State, r.Configured = Ready, true
		r.Diagnostic = "stored authentication is configured; provider access has not been tested"
		return r, nil
	}
	lower := strings.ToLower(out)
	switch {
	case unsupportedDiagnostic(out):
		r.State = Unsupported
		r.Diagnostic = "this Codex version does not support login status; update Codex"
	case strings.Contains(lower, "not logged in"), strings.Contains(lower, "not authenticated"), strings.Contains(lower, "no authentication"):
		r.State = Unconfigured
		r.Diagnostic = "Codex has no configured authentication; run codex login"
	default:
		r.Diagnostic = "codex login status failed without confirming authentication; run that command in your terminal"
	}
	return r, nil
}

func unsupportedDiagnostic(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "unrecognized") || strings.Contains(s, "unknown subcommand") || strings.Contains(s, "unexpected argument")
}

func (c *Codex) diagnostic(ctx context.Context, executable string, args ...string) (string, int, error) {
	timeout := c.ProbeTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = SanitizeEnvironment(os.Environ())
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", -1, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode(), nil
	}
	return out.String(), 0, err
}

// RunInteractive preserves the attached terminal. The caller must not cancel
// ctx on terminal SIGINT: Codex receives that signal in the foreground process
// group and decides whether it interrupts a turn or exits the application.
func (c *Codex) RunInteractive(ctx context.Context, req SessionRequest) error {
	checkTerminal := c.isTerminal
	if checkTerminal == nil {
		checkTerminal = attachedTerminal
	}
	if !checkTerminal(req.Stdin, req.Stdout) {
		return errors.New("insta exec -i requires an interactive terminal on stdin and stdout; use insta exec -p for a single prompt")
	}
	prompt, err := LaunchInstruction(req)
	if err != nil {
		return err
	}
	args := append(profileArgs(req.Profile), "--", prompt)
	cmd, err := c.command(ctx, req, args)
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout = req.Stdin, req.Stdout
	cmd.WaitDelay = 5 * time.Second
	// The child remains in the terminal's foreground process group. Consume the
	// parent's copy of Ctrl-C so it does not exit before Codex restores its TTY.
	interrupts := make(chan os.Signal, 8)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	err = cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return &ChildExitError{Code: exit.ExitCode()}
	}
	if err != nil {
		return errors.New("could not start interactive Codex")
	}
	return nil
}

func profileArgs(profile string) []string {
	if profile == "" {
		return nil
	}
	return []string{"--profile", profile}
}

// All launch modes use the same workspace, credential filtering and nested insta.
func (c *Codex) command(ctx context.Context, req SessionRequest, args []string) (*exec.Cmd, error) {
	p, err := c.executable()
	if err != nil {
		return nil, errors.New("codex is not on PATH; install Codex and run codex login")
	}
	root, err := filepath.Abs(req.Workspace)
	if err != nil {
		return nil, errors.New("resolve Codex workspace")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("Codex workspace is not a directory")
	}
	env := req.Environment
	if env == nil {
		env = os.Environ()
	}
	env = SanitizeEnvironment(env)
	if req.InstaExecutable != "" {
		insta, err := filepath.Abs(req.InstaExecutable)
		if err != nil {
			return nil, errors.New("resolve insta executable")
		}
		info, err := os.Stat(insta)
		if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			return nil, errors.New("insta executable is unavailable for nested lab commands")
		}
		env = prependPath(env, filepath.Dir(insta))
	}
	if req.TaskApprovalScope != "" {
		// Global -c overrides apply to the TUI, exec, and exec resume. Set the
		// marker on Codex shell tools even when they use inherit=core or none.
		overrides := []string{
			"-c", `approval_policy="never"`,
			"-c", `sandbox_mode="workspace-write"`,
			"-c", "sandbox_workspace_write.network_access=true",
			"-c", "shell_environment_policy.set." + TaskApprovalEnv + "=" + strconv.Quote(req.TaskApprovalScope),
		}
		args = append(overrides, args...)
	}
	cmd := exec.CommandContext(ctx, p, args...)
	cmd.Dir, cmd.Env, cmd.Stderr = root, env, req.Stderr
	// Give Codex an opportunity to stop any active tools before WaitDelay
	// enforces termination. os/exec calls Cancel only after a successful start.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	return cmd, nil
}

func attachedTerminal(in io.Reader, out io.Writer) bool {
	i, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(i.Fd())) {
		return false
	}
	o, ok := out.(*os.File)
	return ok && term.IsTerminal(int(o.Fd()))
}

// LaunchInstruction points at the existing blueprint; it never reads credentials
// or inlines the entire repository. The caller supplies only nonsecret context.
func LaunchInstruction(req SessionRequest) (string, error) {
	if len(req.Instruction) > 8*1024 {
		return "", errors.New("initial instruction exceeds 8192 bytes")
	}
	instructions := req.Instructions
	if instructions == "" {
		instructions = "AGENTS.md"
	}
	for _, p := range []string{instructions, req.Blueprint} {
		if p != "" && (!filepath.IsLocal(p) || strings.ContainsAny(p, "\r\n\x00")) {
			return "", errors.New("instruction and blueprint paths must stay inside the project")
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Work in this lab project. Read INSTA.md for this project's CLI workflow. Read %s and insta.yaml before choosing work. Use insta task list and the selected task blueprint plus existing source references to understand the exercise. Use insta for declared task operations and lifecycle actions. Inspect source before adapting it. Do not treat a coding-session exit as proof that an exercise passed. Approval decisions belong to the user and insta; a tool request does not authorize itself. Do not disclose credentials.\n", instructions)
	if req.TaskApprovalScope != "" {
		b.WriteString("The user started this session with insta exec --approve. Run requested declared task operations directly with insta task run. Their displayed effects are preapproved within this lab scope. Do not ask for confirmation again or add --yes. For an operation declared mode: interactive, allocate a PTY in the shell tool. Let insta report any separate manifest, lifecycle, or prerequisite checks.\n")
	}
	if req.TaskID != "" {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,100}$`).MatchString(req.TaskID) {
			return "", errors.New("invalid task ID")
		}
		fmt.Fprintf(&b, "Selected task: %s. Blueprint: %s.\n", req.TaskID, req.Blueprint)
	}
	if req.Instruction != "" {
		fmt.Fprintf(&b, "User instruction:\n%s", req.Instruction)
	}
	return b.String(), nil
}

// SanitizeEnvironment excludes management credentials and the API fallback key.
// Native Codex credentials are still resolved by Codex's own authentication.
func SanitizeEnvironment(env []string) []string {
	values := make(map[string]string, len(env))
	for _, pair := range env {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" || strings.ContainsRune(key, '\x00') {
			continue
		}
		upper := strings.ToUpper(key)
		if upper == TaskApprovalEnv || strings.HasPrefix(upper, "INSTACLUSTR_PROVISION_") || strings.HasPrefix(upper, "INSTA_ASSISTANT_") || strings.HasPrefix(upper, "ASSISTANT.") {
			continue
		}
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func prependPath(env []string, directory string) []string {
	result := append([]string(nil), env...)
	for i, pair := range result {
		if strings.HasPrefix(pair, "PATH=") {
			result[i] = "PATH=" + directory + string(os.PathListSeparator) + strings.TrimPrefix(pair, "PATH=")
			return result
		}
	}
	return append(result, "PATH="+directory)
}

type limitedBuffer struct{ data []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 8192 - len(b.data); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *limitedBuffer) String() string { return string(b.data) }

var _ CodingCLI = (*Codex)(nil)
var _ CodingCLI = ClaudeCode{}
