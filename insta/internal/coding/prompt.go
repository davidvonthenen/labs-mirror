package coding

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var threadIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

// RunPrompt sends a single turn on a dedicated stdin. A fresh loop receives a
// thread ID to resume; a one-shot run has no on-disk Codex conversation.
func (c *Codex) RunPrompt(ctx context.Context, req SessionRequest, threadID string) (string, error) {
	args := append([]string{"exec"}, profileArgs(req.Profile)...)
	// Projects materialized by insta may be outside a Git worktree.
	args = append(args, "--skip-git-repo-check", "--json")
	if req.OneShot {
		args = append(args, "--ephemeral")
	}
	prompt := req.Instruction
	if threadID == "" {
		var err error
		prompt, err = LaunchInstruction(req)
		if err != nil {
			return "", err
		}
	} else {
		if req.OneShot || !threadIDPattern.MatchString(threadID) {
			return "", errors.New("invalid Codex conversation ID for resume")
		}
		if len(prompt) > 8*1024 {
			return "", errors.New("instruction exceeds 8192 bytes")
		}
		args = append(args, "resume", threadID)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := c.command(runCtx, req, append(args, "-"))
	if err != nil {
		return "", err
	}
	cmd.Stdin = strings.NewReader(prompt)
	cmd.WaitDelay = 2 * time.Second
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	done := make(chan error, 1)
	go func() {
		err := cmd.Run()
		writer.Close()
		done <- err
	}()

	turn := execTurn{threadID: threadID}
	parseErr := turn.read(reader, req.Stdout)
	reader.Close()
	if parseErr != nil {
		cancel()
	}
	runErr := <-done
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) && (parseErr == nil || exit.ExitCode() > 0) {
		if req.Stderr != nil {
			if parseErr != nil {
				fmt.Fprintln(req.Stderr, parseErr)
			} else if turn.diagnostic != "" {
				fmt.Fprintln(req.Stderr, turn.diagnostic)
			}
		}
		return "", &ChildExitError{Code: exit.ExitCode()}
	}
	if parseErr != nil {
		return "", parseErr
	}
	if runErr != nil {
		return "", fmt.Errorf("run codex exec: %w", runErr)
	}
	if !turn.completed {
		if turn.diagnostic != "" {
			return "", errors.New(turn.diagnostic)
		}
		return "", errors.New("Codex exited without completing the turn")
	}
	if req.OneShot {
		return "", nil
	}
	if turn.threadID == "" {
		return "", errors.New("Codex did not return a conversation ID; cannot continue this conversation")
	}
	return turn.threadID, nil
}

type execTurn struct {
	threadID   string
	completed  bool
	diagnostic string
}

func (turn *execTurn) read(input io.Reader, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	// Command output events can be much larger than Scanner's 64 KiB default.
	// Keep only one event at a time, with an explicit cap on corrupt input.
	lines := bufio.NewScanner(input)
	lines.Buffer(make([]byte, 32*1024), 32*1024*1024)
	for lines.Scan() {
		var event struct {
			Type     string                      `json:"type"`
			ThreadID string                      `json:"thread_id"`
			Message  string                      `json:"message"`
			Error    struct{ Message string }    `json:"error"`
			Item     struct{ Type, Text string } `json:"item"`
		}
		if err := json.Unmarshal(lines.Bytes(), &event); err != nil {
			return fmt.Errorf("invalid codex exec --json event: %w", err)
		}
		switch event.Type {
		case "thread.started":
			if !threadIDPattern.MatchString(event.ThreadID) || (turn.threadID != "" && event.ThreadID != turn.threadID) {
				return errors.New("Codex returned an invalid or unexpected conversation ID")
			}
			turn.threadID = event.ThreadID
		case "item.completed":
			// Codex exec delivers assistant text in completed message events.
			if event.Item.Type == "agent_message" && event.Item.Text != "" {
				message := event.Item.Text
				if !strings.HasSuffix(message, "\n") {
					message += "\n"
				}
				if _, err := io.WriteString(output, message); err != nil {
					return fmt.Errorf("write Codex reply: %w", err)
				}
			}
		case "error":
			// A connection error can be followed by a successful retry.
			turn.diagnostic = event.Message
		case "turn.failed":
			turn.completed = false
			turn.diagnostic = event.Error.Message
		case "turn.completed":
			turn.completed = true
		}
	}
	if err := lines.Err(); err != nil {
		return fmt.Errorf("read codex exec event stream: %w", err)
	}
	return nil
}
