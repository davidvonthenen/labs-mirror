// Package cli implements the user-facing command surface. Go owns lifecycle;
// coding tools receive a writable lab and a terminal, not ownership authority.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/instaclustr/labs/insta/internal/bootstrap"
	"github.com/instaclustr/labs/insta/internal/coding"
	"github.com/instaclustr/labs/insta/internal/config"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/platform"
	"github.com/instaclustr/labs/insta/internal/runner"
	"github.com/instaclustr/labs/insta/internal/state"
)

var Version = "0.0.1"

type App struct {
	In             io.Reader
	Out, Err       io.Writer
	JSON, Yes      bool
	Executable     string
	ConfirmFunc    func(string) error
	PlatformClient *platform.OpenSearch
	DataClient     *http.Client
	CodingCLI      coding.CodingCLI
	Source         *bootstrap.Source
	reader         *bufio.Reader
	command        string
}
type session struct {
	App                    *App
	Root                   string
	Project                *state.Project
	Manifest               *manifest.Manifest
	Hash                   string
	Values                 map[string]string
	View                   map[string]config.Value
	Secrets                map[string]string
	Redactor               *runner.Redactor
	taskEffectsPreapproved bool
}
type Error struct {
	Kind          string
	Message       string
	Code          int
	ChildExitCode *int
}

func (e *Error) Error() string { return e.Message }
func failure(kind string, code int, e error) error {
	return &Error{Kind: kind, Message: e.Error(), Code: code}
}

func New(in io.Reader, out, errOut io.Writer) *App {
	exe, _ := os.Executable()
	return &App{In: in, Out: out, Err: errOut, Executable: exe}
}
func (a *App) Execute(ctx context.Context, args []string) int {
	root := a.Command()
	root.SetArgs(args)
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	if e := root.ExecuteContext(ctx); e != nil {
		code, kind := 1, "operation_failed"
		var child *int
		var ce *Error
		if errors.As(e, &ce) {
			code, kind, child = ce.Code, ce.Kind, ce.ChildExitCode
		}
		message := runner.NewRedactor(nil).Text(e.Error())
		if a.JSON {
			json.NewEncoder(a.Out).Encode(map[string]any{"ok": false, "error": map[string]any{"category": kind, "message": message, "child_exit_code": child}})
		} else {
			fmt.Fprintln(a.Err, "Error:", message)
		}
		return code
	}
	return 0
}
func (a *App) Command() *cobra.Command {
	r := &cobra.Command{Use: "insta", Short: "Learn, run, and build from Instaclustr labs", SilenceUsage: true, SilenceErrors: true, CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true}}
	r.PersistentFlags().BoolVar(&a.JSON, "json", false, "Write structured command results to stdout")
	r.PersistentFlags().BoolVar(&a.Yes, "yes", false, "Confirm the displayed action without prompting")
	r.PersistentPreRun = func(cmd *cobra.Command, args []string) { a.command = cmd.CommandPath() }
	r.AddCommand(&cobra.Command{Use: "version", Short: "Show the CLI version", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return a.Result(map[string]any{"version": Version}, "insta "+Version)
	}})
	a.addCatalog(r)
	a.addNew(r)
	a.addConfig(r)
	a.addTasks(r)
	stack := &cobra.Command{Use: "stack", Short: "Manage environments belonging to this project"}
	stack.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), false, func(s *session) error {
			items := []map[string]any{}
			for _, n := range manifest.Keys(s.Project.Stacks) {
				items = append(items, map[string]any{"name": n, "selected": n == s.Project.SelectedStack})
			}
			return a.Result(items, strings.Join(manifest.Keys(s.Project.Stacks), "\n"))
		})
	}})
	stack.AddCommand(&cobra.Command{Use: "select <stack>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), true, func(s *session) error {
			if s.Project.Stacks[args[0]] == nil {
				return failure("configuration", 2, fmt.Errorf("unknown stack %q; use insta stack list", args[0]))
			}
			s.Project.SelectedStack = args[0]
			if e := s.Save(); e != nil {
				return e
			}
			return a.Result(map[string]string{"stack": args[0]}, "Selected stack "+args[0])
		})
	}})
	r.AddCommand(stack)
	r.AddCommand(&cobra.Command{Use: "up", Short: "Prepare runtime, platform, and declared support services", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), true, func(s *session) error {
			if e := s.Up(cmd.Context()); e != nil {
				return e
			}
			return a.Result(map[string]any{"stack": s.Project.SelectedStack, "runtime": s.Project.Stack().RuntimeStatus, "platforms": s.Project.Stack().Platforms}, "Stack prerequisites are ready. Use insta exec or insta task list.")
		})
	}})
	r.AddCommand(&cobra.Command{Use: "down", Short: "Stop services and delete only this stack's owned platforms", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), true, func(s *session) error {
			if e := s.Down(cmd.Context()); e != nil {
				return e
			}
			return a.Result(map[string]any{"stack": s.Project.SelectedStack, "remaining_platforms": s.Project.Stack().Platforms}, "Owned stack resources have been cleaned up; project files and settings are retained.")
		})
	}})
	a.addExec(r)
	return r
}

func (a *App) Result(data any, message string) error {
	if a.JSON {
		return json.NewEncoder(a.Out).Encode(map[string]any{"ok": true, "command": a.command, "data": data})
	}
	if message != "" {
		_, e := fmt.Fprintln(a.Out, message)
		return e
	}
	return nil
}
func (a *App) Ask(prompt string, secret bool) (string, error) {
	file, ok := a.In.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", failure("input_required", 2, errors.New("interactive input is required; use declared config subcommands or --yes for an already reviewed action"))
	}
	fmt.Fprint(a.Err, prompt)
	if secret {
		b, e := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(a.Err)
		return string(b), e
	}
	if a.reader == nil {
		a.reader = bufio.NewReader(a.In)
	}
	s, e := a.reader.ReadString('\n')
	if e != nil && !(errors.Is(e, io.EOF) && s != "") {
		return "", e
	}
	return strings.TrimSpace(s), nil
}
func (a *App) Confirm(action string) error {
	fmt.Fprintln(a.Err, action)
	if a.ConfirmFunc != nil {
		return a.ConfirmFunc(action)
	}
	if a.Yes {
		return nil
	}
	answer, e := a.Ask("Continue? [y/N]: ", false)
	if e != nil {
		return e
	}
	if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
		return failure("declined", 4, errors.New("action was not confirmed"))
	}
	return nil
}
func (a *App) withSession(ctx context.Context, mutate bool, fn func(*session) error) error {
	cwd, e := os.Getwd()
	if e != nil {
		return e
	}
	root, e := state.Find(cwd)
	if e != nil {
		return failure("configuration", 2, e)
	}
	return a.atSession(ctx, root, mutate, fn)
}
func (a *App) atSession(ctx context.Context, root string, mutate bool, fn func(*session) error) error {
	if mutate {
		l, e := state.Lock(root)
		if e != nil {
			return failure("busy", 7, e)
		}
		defer l.Close()
	}
	p, e := state.Load(root)
	if e != nil {
		return e
	}
	m, h, e := manifest.Load(root)
	if e != nil {
		return failure("manifest", 2, e)
	}
	if m.ID != p.Lab {
		return failure("manifest", 2, errors.New("manifest lab differs from the project's immutable lab identity"))
	}
	s := &session{App: a, Root: root, Project: p, Manifest: m, Hash: h}
	if e = s.Refresh(); e != nil {
		return failure("configuration", 2, e)
	}
	if e = fn(s); e != nil {
		return s.sanitizeError(e)
	}
	return nil
}
func (s *session) Save() error { return state.Save(s.Root, s.Project) }
func (s *session) Refresh() error {
	secrets, e := state.ReadSecrets(s.Project.ID, s.Project.SelectedStack)
	if e != nil {
		return e
	}
	s.Secrets = secrets
	s.Values, s.View, e = config.Resolve(s.Manifest, s.Project, secrets, nil)
	if e != nil {
		return e
	}
	s.Redactor = runner.NewRedactor(config.SecretValues(s.Manifest, s.Values))
	return nil
}
func (s *session) sanitizeError(e error) error {
	if s.Redactor == nil {
		return e
	}
	var ce *Error
	if errors.As(e, &ce) {
		copy := *ce
		copy.Message = s.Redactor.Text(e.Error())
		return &copy
	}
	return errors.New(s.Redactor.Text(e.Error()))
}
func (s *session) ApproveManifest() error {
	if s.Project.Stack().ApprovedManifest == s.Hash {
		return nil
	}
	var lines []string
	lines = append(lines, "The executable manifest or deployment request changed. Review the current commands and effects:")
	for _, t := range manifest.Keys(s.Manifest.Tasks) {
		for _, op := range manifest.Keys(s.Manifest.Tasks[t].Operations) {
			o := s.Manifest.Tasks[t].Operations[op]
			lines = append(lines, fmt.Sprintf("  %s/%s: %q; effects: %s", t, op, o.Command, strings.Join(o.Effects, ", ")))
		}
	}
	for _, n := range manifest.Keys(s.Manifest.Services) {
		d := s.Manifest.Services[n]
		lines = append(lines, fmt.Sprintf("  service %s: %q", n, d.Command))
	}
	for _, cmd := range s.Manifest.Runtime.Setup {
		lines = append(lines, fmt.Sprintf("  runtime setup: %q", cmd))
	}
	if e := s.App.Confirm(s.Redactor.Text(strings.Join(lines, "\n"))); e != nil {
		return e
	}
	s.Project.Stack().ApprovedManifest = s.Hash
	return s.Save()
}

func (a *App) RunTaskAt(ctx context.Context, root, task, operation string) (state.Operation, error) {
	var result state.Operation
	// Model requests must use the same validations and interactive approvals as a
	// direct user operation, even if --yes was present on the parent command.
	copy := *a
	copy.Yes = false
	e := copy.atSession(ctx, root, true, func(s *session) error { var e error; result, e = s.RunTask(ctx, task, operation); return e })
	return result, e
}

func isTTY(input io.Reader) bool {
	f, ok := input.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
func safeName(s string) bool {
	if s == "" || len(s) > 80 {
		return false
	}
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}
func projectRootPath(name string) (string, error) {
	if !safeName(name) {
		return "", errors.New("project name must contain only letters, digits, '-' or '_' (up to 80 characters)")
	}
	return filepath.Abs(name)
}
