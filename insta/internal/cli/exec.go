package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/instaclustr/labs/insta/internal/coding"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const maxExecPrompt = 8192

type execMode int

const (
	execLoop execMode = iota
	execInteractive
	execPrompt
)

type execOptions struct {
	mode        execMode
	instruction string
	taskID      string
	profile     string
	approve     bool
}

func (a *App) addExec(root *cobra.Command) {
	var options execOptions
	var interactive bool
	c := &cobra.Command{
		Use:   "exec [-p message | -i [instruction]]",
		Short: "Chat in the lab, submit a prompt, or open the Codex terminal",
		Long:  "Without an instruction, read one prompt per line and continue the conversation until /quit, /exit, or EOF. Use -p for one response or -i with an optional initial instruction for the native Codex terminal.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.JSON {
				return failure("usage", 2, errors.New("insta exec does not support --json"))
			}
			switch {
			case cmd.Flags().Changed("prompt"):
				if interactive || len(args) != 0 || strings.TrimSpace(options.instruction) == "" {
					return failure("usage", 2, errors.New("use -p with one nonempty message and without -i or a positional instruction"))
				}
				options.mode = execPrompt
			case interactive:
				options.mode = execInteractive
				if len(args) != 0 {
					options.instruction = args[0]
				}
			case len(args) != 0:
				return failure("usage", 2, errors.New("use -p MESSAGE for one response or -i [instruction] for the native Codex terminal"))
			}
			if cmd.Flags().Changed("profile") && strings.TrimSpace(options.profile) == "" {
				return failure("usage", 2, errors.New("--profile requires a Codex profile name"))
			}
			ctx := cmd.Context()
			if options.mode != execInteractive {
				var stop context.CancelFunc
				ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
				defer stop()
			}
			err := a.withSession(ctx, false, func(s *session) error { return s.Exec(ctx, options) })
			if ctx.Err() != nil {
				return failure("interrupted", 130, ctx.Err())
			}
			return err
		},
	}
	c.Flags().BoolVarP(&interactive, "interactive", "i", false, "Open the native Codex terminal")
	c.Flags().StringVarP(&options.instruction, "prompt", "p", "", "Submit one message and print the response")
	c.Flags().StringVar(&options.taskID, "task", "", "Select a declared teaching task for this session")
	c.Flags().StringVar(&options.profile, "profile", "", "Use an existing Codex configuration profile")
	c.Flags().BoolVar(&options.approve, "approve", false, "Enable workspace writes and network access without Codex shell prompts, and preapprove declared task effects in this project and stack")
	root.AddCommand(c)
}

func (s *session) runPrompt(ctx context.Context, adapter coding.CodingCLI, req coding.SessionRequest, thread string) (string, error) {
	if errFile, ok := s.App.Err.(*os.File); ok && term.IsTerminal(int(errFile.Fd())) {
		stop := make(chan struct{})
		finished := make(chan struct{})
		fmt.Fprintln(errFile, "Thinking…")
		go func() {
			defer close(finished)
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			start := time.Now()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					fmt.Fprintf(errFile, "Codex is still working (%s)…\n", time.Since(start).Round(time.Second))
				}
			}
		}()
		defer func() { close(stop); <-finished }()
	}
	id, err := adapter.RunPrompt(ctx, req, thread)
	return id, codingExit(err)
}

func codingExit(err error) error {
	var child *coding.ChildExitError
	if errors.As(err, &child) {
		code := child.Code
		if code < 1 || code > 255 {
			code = 1
		}
		return &Error{Kind: "coding_cli_exit", Message: child.Error(), Code: code, ChildExitCode: &child.Code}
	}
	return err
}

func (s *session) promptLoop(ctx context.Context, adapter coding.CodingCLI, req coding.SessionRequest) error {
	in := s.App.In
	if f, ok := in.(*os.File); ok {
		in = execInput{ctx, f}
	}
	input := bufio.NewReader(in)
	terminal := isTTY(s.App.In)
	if terminal {
		fmt.Fprintln(s.App.Err, "Codex lab session. Enter a message, or type /quit to leave.")
	}
	var thread string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if terminal {
			fmt.Fprint(s.App.Err, "insta> ")
		}
		line, err := readExecLine(input)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, errPromptTooLong) {
			fmt.Fprintln(s.App.Err, err)
			continue
		}
		if err != nil {
			return err
		}
		switch strings.TrimSpace(line) {
		case "":
			continue
		case "/quit", "/exit":
			return nil
		}
		req.Instruction = s.redactAgent(line)
		next, err := s.runPrompt(ctx, adapter, req, thread)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			fmt.Fprintln(s.App.Err, "Error:", s.redactAgent(err.Error()))
			continue
		}
		thread = next
	}
}

var errPromptTooLong = fmt.Errorf("prompt exceeds %d bytes", maxExecPrompt)

// Drain an oversized line before prompting again. The remaining bytes in the
// same pasted message must never become instructions on a later turn.
func readExecLine(input *bufio.Reader) (string, error) {
	var line strings.Builder
	tooLong := false
	for {
		part, more, err := input.ReadLine()
		if err != nil && !(errors.Is(err, io.EOF) && (line.Len() > 0 || tooLong)) {
			return "", err
		}
		if line.Len()+len(part) > maxExecPrompt {
			tooLong = true
		}
		if !tooLong {
			line.Write(part)
		}
		if !more {
			if tooLong {
				return "", errPromptTooLong
			}
			return line.String(), nil
		}
	}
}

// A terminal or a pipe can remain idle indefinitely. Poll before a blocking
// read so context cancellation also works mid-line, without closing borrowed
// stdin or leaving a goroutine behind to consume another command's input.
type execInput struct {
	ctx context.Context
	f   *os.File
}

func (in execInput) Read(p []byte) (int, error) {
	descriptors := []unix.PollFd{{Fd: int32(in.f.Fd()), Events: unix.POLLIN}}
	for in.ctx.Err() == nil {
		n, err := unix.Poll(descriptors, 200)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n != 0 {
			return in.f.Read(p)
		}
	}
	return 0, in.ctx.Err()
}
