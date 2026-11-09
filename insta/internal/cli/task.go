package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/instaclustr/labs/insta/internal/coding"
	"github.com/instaclustr/labs/insta/internal/config"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/runner"
	"github.com/instaclustr/labs/insta/internal/state"
	"github.com/spf13/cobra"
)

func (a *App) addTasks(root *cobra.Command) {
	tasks := &cobra.Command{Use: "task", Short: "Run complete teaching exercises through their declared operations"}
	tasks.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), false, func(s *session) error {
			lines := []string{}
			for _, name := range manifest.Keys(s.Manifest.Tasks) {
				t := s.Manifest.Tasks[name]
				lines = append(lines, fmt.Sprintf("%s\n  blueprint: %s\n  operations: %s\n  platforms: %s; services: %s", name, t.Blueprint, strings.Join(manifest.Keys(t.Operations), ", "), strings.Join(t.Requires.Platforms, ", "), strings.Join(append(append([]string{}, t.Requires.Services...), t.Services...), ", ")))
			}
			return a.Result(s.Manifest.Tasks, strings.Join(lines, "\n"))
		})
	}})
	tasks.AddCommand(&cobra.Command{Use: "start <task>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), true, func(s *session) error {
			t, ok := s.Manifest.Tasks[args[0]]
			if !ok {
				return unknownTask(args[0])
			}
			if e := s.ApproveManifest(); e != nil {
				return e
			}
			if e := s.EnsureRuntime(cmd.Context()); e != nil {
				return e
			}
			if e := s.CheckPlatforms(cmd.Context(), t.Requires.Platforms); e != nil {
				return e
			}
			for _, name := range append(append([]string{}, t.Requires.Services...), t.Services...) {
				if e := s.StartService(cmd.Context(), name, args[0]); e != nil {
					return e
				}
			}
			s.Project.Stack().ActiveTask = args[0]
			if e := s.Save(); e != nil {
				return e
			}
			return a.Result(map[string]string{"task": args[0], "blueprint": t.Blueprint}, "Selected "+args[0]+". Read "+t.Blueprint+"; use insta task run to execute an operation.")
		})
	}})
	tasks.AddCommand(&cobra.Command{Use: "run <task> <operation>", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), true, func(s *session) error {
			if inherited := os.Getenv(coding.TaskApprovalEnv); inherited != "" {
				scope, err := s.taskApprovalScope()
				if err != nil {
					return err
				}
				s.taskEffectsPreapproved = inherited == scope
			}
			result, e := s.RunTask(cmd.Context(), args[0], args[1])
			if e != nil {
				return e
			}
			return a.Result(result, fmt.Sprintf("%s/%s exited %d. %s", args[0], args[1], *result.ExitCode, result.Verification))
		})
	}})
	tasks.AddCommand(&cobra.Command{Use: "stop <task>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), true, func(s *session) error {
			if _, ok := s.Manifest.Tasks[args[0]]; !ok {
				return unknownTask(args[0])
			}
			// Stop consumers before their dependencies; shared/stack services remain.
			order := serviceOrder(s.Manifest)
			for i := len(order) - 1; i >= 0; i-- {
				name := order[i]
				service := s.Project.Stack().Services[name]
				if service != nil && service.Owner == args[0] {
					if e := s.StopService(cmd.Context(), name); e != nil {
						return e
					}
				}
			}
			if s.Project.Stack().ActiveTask == args[0] {
				s.Project.Stack().ActiveTask = ""
			}
			if e := s.Save(); e != nil {
				return e
			}
			return a.Result(map[string]string{"task": args[0]}, "Task context ended; its exclusive services were stopped. Shared support services remain owned by the stack.")
		})
	}})
	tasks.AddCommand(&cobra.Command{Use: "status [task]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.withSession(cmd.Context(), false, func(s *session) error {
			filter := ""
			if len(args) > 0 {
				filter = args[0]
				if _, ok := s.Manifest.Tasks[filter]; !ok {
					return unknownTask(filter)
				}
			}
			ops := map[string]state.Operation{}
			for k, v := range s.Project.Stack().Operations {
				if filter == "" || v.Task == filter {
					ops[k] = v
				}
			}
			services := map[string]any{}
			for name, record := range s.Project.Stack().Services {
				status, e := runner.Control(cmd.Context(), record, "status")
				ready := false
				if e == nil && status.Running {
					if def, ok := s.Manifest.Services[name]; ok {
						ready = runner.CheckProbe(cmd.Context(), def.Probe) == nil
					}
				}
				services[name] = map[string]any{"running": e == nil && status.Running, "probe_passed": ready, "owner": record.Owner, "log": record.Log}
			}
			data := map[string]any{"active_task": s.Project.Stack().ActiveTask, "runtime": s.Project.Stack().RuntimeStatus, "services": services, "operations": ops}
			b, _ := json.MarshalIndent(data, "", "  ")
			return a.Result(data, string(b))
		})
	}})
	root.AddCommand(tasks)
}
func unknownTask(task string) error {
	return failure("configuration", 2, fmt.Errorf("unknown task %q; use insta task list", task))
}

// A parent and a nested CLI can discover the same project using different
// symlink aliases. Resolve the physical root before comparing their scopes.
func (s *session) taskApprovalScope() (string, error) {
	root, err := filepath.EvalSymlinks(s.Root) // project discovery supplies an absolute path
	if err != nil {
		return "", fmt.Errorf("resolve project for task approval: %w", err)
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%q %q %q %q", root, s.Project.ID, s.Project.SelectedStack, s.Hash)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *session) RunTask(ctx context.Context, taskID, operation string) (state.Operation, error) {
	var result state.Operation
	t, ok := s.Manifest.Tasks[taskID]
	if !ok {
		return result, unknownTask(taskID)
	}
	op, ok := t.Operations[operation]
	if !ok {
		return result, failure("configuration", 2, fmt.Errorf("unknown operation %q for %s", operation, taskID))
	}
	if op.Mode == "interactive" && (!isTTY(s.App.In) || s.App.JSON) {
		return result, failure("terminal_required", 2, errors.New("this task operation is interactive; use a terminal without --json"))
	}
	if e := s.ApproveManifest(); e != nil {
		return result, e
	}
	if e := config.Require(s.Values, append(manifest.Required(t.Environment, operation, s.Values), op.Required...)...); e != nil {
		return result, failure("configuration", 2, e)
	}
	if e := s.EnsureRuntime(ctx); e != nil {
		return result, e
	}
	if e := s.CheckPlatforms(ctx, append(append([]string{}, t.Requires.Platforms...), op.Requires.Platforms...)); e != nil {
		return result, e
	}
	for _, name := range append(append([]string{}, t.Requires.Services...), op.Requires.Services...) {
		record := s.Project.Stack().Services[name]
		if record == nil {
			return result, failure("prerequisite", 3, fmt.Errorf("service %s is required; run insta task start %s", name, taskID))
		}
		status, e := runner.Control(ctx, record, "status")
		if e != nil || !status.Running {
			return result, failure("prerequisite", 3, fmt.Errorf("service %s is not running; run insta task start %s", name, taskID))
		}
		if e := runner.CheckProbe(ctx, s.Manifest.Services[name].Probe); e != nil {
			return result, failure("prerequisite", 3, e)
		}
	}
	for _, index := range op.Indexes {
		if e := s.checkIndex(ctx, index); e != nil {
			return result, failure("prerequisite", 3, e)
		}
	}
	if len(op.Effects) > 0 {
		fmt.Fprint(s.App.Err, s.Redactor.Text(fmt.Sprintf("Operation %s/%s\n  command: %q\n  effects: %s\n", taskID, operation, op.Command, strings.Join(op.Effects, ", "))))
	}
	if approvalEffects(op.Effects) {
		if s.taskEffectsPreapproved {
			fmt.Fprintln(s.App.Err, "Task effects preapproved by insta exec --approve.")
		} else if e := s.App.Confirm("Run this operation with the displayed remote writes, model calls, or destructive effects?"); e != nil {
			return result, e
		}
	}
	cwd := op.Cwd
	if cwd == "" {
		cwd = t.Source
	}
	directory, e := manifest.Path(s.Root, cwd, true)
	if e != nil {
		return result, e
	}
	env := config.TaskEnvironment(s.Manifest, s.Values, s.Root, runner.Venv(s.Root, s.Project.SelectedStack))
	// Resolve Bedrock's supported credential chain instead of requiring static keys.
	if s.Values["LLM_PROVIDER"] == "bedrock" && contains(op.Effects, "remote-model-call") {
		code := "import boto3; c=boto3.Session().get_credentials(); assert c is not None, 'No AWS credentials found'; c.get_frozen_credentials(); print('AWS credential chain resolved')"
		if _, e := runner.Run(ctx, runner.Request{Argv: []string{runner.Python(s.Root, s.Project.SelectedStack), "-c", code}, Cwd: s.Root, Env: env, Output: s.App.Err, Redactor: s.Redactor}); e != nil {
			return result, failure("prerequisite", 3, errors.New("AWS credential chain could not resolve; configure an AWS profile, SSO, workload role, or environment credentials"))
		}
	}
	logRelative := filepath.Join(".insta", "logs", s.Project.SelectedStack+"-"+taskID+"-"+operation+".log")
	logPath := filepath.Join(s.Root, logRelative)
	if e = os.MkdirAll(filepath.Dir(logPath), 0700); e != nil {
		return result, e
	}
	log, e := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return result, e
	}
	defer log.Close()
	result = state.Operation{Task: taskID, Name: operation, StartedAt: time.Now().UTC(), Status: "running", Verification: "Not independently verified.", Log: filepath.ToSlash(logRelative)}
	s.Project.Stack().Operations[taskID+"/"+operation] = result
	if e = s.Save(); e != nil {
		return result, e
	}
	output := s.App.Out
	if s.App.JSON {
		output = s.App.Err
	}
	tail := &tailWriter{limit: 8192}
	runResult, runErr := runner.Run(ctx, runner.Request{Argv: runner.Args(s.Root, s.Project.SelectedStack, op.Command), Cwd: directory, Env: env, Stdin: s.App.In, Output: io.MultiWriter(output, log, tail), Redactor: s.Redactor, Interactive: op.Mode == "interactive"})
	finished := time.Now().UTC()
	result.FinishedAt = &finished
	result.ExitCode = &runResult.ExitCode
	result.OutputTail = tail.String()
	result.Status = "exited"
	if runErr != nil {
		result.Status = "failed"
	} else if op.Verification != "" {
		result.Verification = op.Verification
	}
	s.Project.Stack().Operations[taskID+"/"+operation] = result
	if e = s.Save(); e != nil {
		return result, e
	}
	if runErr != nil {
		return result, &Error{Kind: "child_failed", Code: 6, ChildExitCode: &runResult.ExitCode, Message: fmt.Sprintf("%s/%s failed with child exit %d; inspect %s", taskID, operation, runResult.ExitCode, logRelative)}
	}
	return result, nil
}

func (s *session) checkIndex(ctx context.Context, index manifest.Index) error {
	name := s.Values[index.Config]
	if name == "" {
		name = index.Default
	}
	if name == "" {
		return fmt.Errorf("index setting %s is missing", index.Config)
	}
	if strings.ContainsAny(name, "/*?#, ") {
		return fmt.Errorf("index setting %s must identify one index", index.Config)
	}
	response, e := s.DataRequest(ctx, "GET", "/"+url.PathEscape(name)+"/_count", nil)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("index %s is unavailable (HTTP %d); run the task's ingest operation before query/validation", name, response.StatusCode)
	}
	var body struct {
		Count int64 `json:"count"`
	}
	if e = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); e != nil {
		return errors.New("could not read the index document count")
	}
	if body.Count == 0 {
		return fmt.Errorf("index %s is empty; run the task's ingest operation", name)
	}
	return nil
}
func approvalEffects(effects []string) bool {
	for _, e := range effects {
		if strings.Contains(e, "write") || strings.Contains(e, "delete") || strings.Contains(e, "remote-model-call") || strings.Contains(e, "remote-api-call") {
			if e != "model-cache-write" && e != "local-cache-write" {
				return true
			}
		}
	}
	return false
}
func contains(values []string, needle string) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}
func serviceOrder(m *manifest.Manifest) []string {
	var out []string
	seen := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		for _, dep := range m.Services[n].Requires.Services {
			visit(dep)
		}
		out = append(out, n)
	}
	for _, name := range manifest.Keys(m.Services) {
		visit(name)
	}
	return out
}

type tailWriter struct {
	data  []byte
	limit int
}

func (w *tailWriter) Write(b []byte) (int, error) {
	w.data = append(w.data, b...)
	if len(w.data) > w.limit {
		w.data = w.data[len(w.data)-w.limit:]
	}
	return len(b), nil
}
func (w *tailWriter) String() string { return string(w.data) }
