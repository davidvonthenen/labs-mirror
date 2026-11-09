package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func apiResponse(status int, value any) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw(value)))}
}

func functionResponse(id, name string, args any) map[string]any {
	return map[string]any{"status": "completed", "output": []any{
		map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}, "encrypted_content": "opaque-reasoning"},
		map[string]any{"type": "function_call", "id": "fc_1", "call_id": id, "name": name, "arguments": string(raw(args))},
	}}
}

func textResponse(text string) map[string]any {
	return map[string]any{"status": "completed", "output": []any{
		map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": text}}},
	}}
}

func TestResponsesLoopCorrelatesToolsAndPreservesReasoning(t *testing.T) {
	requests, operations := 0, 0
	var output, progress bytes.Buffer
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.String() != DefaultEndpoint || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer api-secret" {
			t.Fatalf("incorrect request: %s %s", r.Method, r.URL)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("api-secret")) || bytes.Contains(body, []byte("lab-secret")) {
			t.Fatal("secret transmitted in body")
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["store"] != false || payload["parallel_tool_calls"] != false {
			t.Fatal("unsafe response state/tool settings")
		}
		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 5 {
			t.Fatal("wrong tool list")
		}
		for _, v := range tools {
			tool := v.(map[string]any)
			p := tool["parameters"].(map[string]any)
			if tool["strict"] != true || p["additionalProperties"] != false || p["required"] == nil {
				t.Fatal("non-strict tool schema")
			}
		}
		if requests == 1 {
			return apiResponse(200, functionResponse("call_1", "run_task_operation", map[string]string{"task_id": "baseline-vector-rag", "operation": "validate"})), nil
		}
		if requests != 2 {
			t.Fatal("unexpected request")
		}
		items := payload["input"].([]any)
		if len(items) != 4 {
			t.Fatalf("wrong input count: %#v", items)
		}
		if items[1].(map[string]any)["encrypted_content"] != "opaque-reasoning" {
			t.Fatal("reasoning lost")
		}
		last := items[3].(map[string]any)
		if last["type"] != "function_call_output" || last["call_id"] != "call_1" || !strings.Contains(last["output"].(string), "[REDACTED]") {
			t.Fatalf("wrong tool output correlation: %#v", last)
		}
		return apiResponse(200, textResponse("Observed fixture result: api-secret and lab-secret")), nil
	})
	client := Client{APIKey: "api-secret", Model: "fixture-model", HTTP: &http.Client{Transport: transport}}
	err := client.Run(context.Background(), Request{
		Consent: true, Instruction: "Explain lab-secret without disclosing it", Output: &output, Progress: &progress,
		Redact: func(s string) string { return strings.ReplaceAll(s, "lab-secret", "[REDACTED]") },
		Handle: func(_ context.Context, call ToolCall) (string, error) {
			operations++
			if call.Name != "run_task_operation" || call.TaskID != "baseline-vector-rag" || call.Operation != "validate" {
				t.Fatalf("wrong call %+v", call)
			}
			return "observed exit 0, lab-secret", nil
		},
	})
	if err != nil || requests != 2 || operations != 1 {
		t.Fatalf("requests %d operations %d error %v", requests, operations, err)
	}
	if strings.Contains(output.String(), "secret") || !strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("output not redacted: %s", output.String())
	}
}

func TestConsentAndConfigurationPreventRequests(t *testing.T) {
	client := Client{APIKey: "fixture-key", Model: "fixture-model", HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("network called without consent/configuration")
		return nil, nil
	})}}
	req := Request{Instruction: "Explain this task", Handle: func(context.Context, ToolCall) (string, error) { return "", nil }}
	if err := client.Run(context.Background(), req); err == nil {
		t.Fatal("consent omitted")
	}
	req.Consent = true
	client.APIKey = ""
	if err := client.Run(context.Background(), req); err == nil {
		t.Fatal("missing assistant key accepted")
	}
	client.APIKey = "fixture-key"
	client.Model = ""
	if err := client.Run(context.Background(), req); err == nil {
		t.Fatal("missing assistant model accepted")
	}
}

func TestToolAllowlistAndArgumentValidation(t *testing.T) {
	tests := []struct{ name, args string }{
		{"shell", `{"command":"rm -rf ."}`},
		{"run_task_operation", `{"task_id":"baseline-vector-rag","operation":"ingest","command":"unsafe"}`},
		{"run_task_operation", `{"task_id":"../other","operation":"ingest"}`},
		{"run_task_operation", `{"task_id":"task","operation":"validate; shell"}`},
		{"read_task", `{"task_id":null}`},
		{"read_task", `{"task_id":3}`},
		{"list_tasks", `{"extra":"x"}`},
		{"get_lab_status", `null`},
		{"read_source", `{"relative_path":"../.env"}`},
		{"read_source", `{"relative_path":".insta/secrets.json"}`},
		{"read_source", `{"relative_path":"/etc/passwd"}`},
		{"read_source", `{"relative_path":"support/../.env"}`},
		{"read_source", `{"relative_path":"C:\\private.txt"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name+tt.args, func(t *testing.T) {
			if _, err := parseCall(tt.name, tt.args); err == nil {
				t.Fatalf("accepted forbidden call %s %s", tt.name, tt.args)
			}
		})
	}
	for _, tt := range []struct{ name, args string }{
		{"list_tasks", `{}`}, {"get_lab_status", `{}`}, {"read_task", `{"task_id":"baseline-vector-rag"}`},
		{"read_source", `{"relative_path":"support/common/config.py"}`},
		{"run_task_operation", `{"task_id":"baseline-vector-rag","operation":"validate"}`},
	} {
		if _, err := parseCall(tt.name, tt.args); err != nil {
			t.Fatalf("valid call rejected: %v", err)
		}
	}
}

func TestUnknownToolNeverReachesHandler(t *testing.T) {
	client := Client{APIKey: "fixture-key", Model: "fixture-model", HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return apiResponse(200, functionResponse("call_1", "shell", map[string]string{"command": "unsafe"})), nil
	})}}
	err := client.Run(context.Background(), Request{Consent: true, Instruction: "Explain", Handle: func(context.Context, ToolCall) (string, error) { t.Fatal("unknown tool ran"); return "", nil }})
	if err == nil || !strings.Contains(err.Error(), "undeclared tool") {
		t.Fatalf("wrong rejection %v", err)
	}
}

func TestNoExecutionAfterTurnLimitOrRepeatedCall(t *testing.T) {
	for _, maxTurns := range []int{1, 4} {
		t.Run(string(rune('0'+maxTurns)), func(t *testing.T) {
			operations := 0
			client := Client{APIKey: "fixture-key", Model: "fixture-model", MaxTurns: maxTurns, HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return apiResponse(200, functionResponse("same_call", "list_tasks", map[string]string{})), nil
			})}}
			err := client.Run(context.Background(), Request{Consent: true, Instruction: "Explain", Handle: func(context.Context, ToolCall) (string, error) { operations++; return "tasks", nil }})
			if err == nil {
				t.Fatal("unbounded loop")
			}
			want := 1
			if maxTurns == 1 {
				want = 0
			}
			if operations != want {
				t.Fatalf("executed %d operations; wanted %d", operations, want)
			}
		})
	}
}

func TestToolFailureIsVisibleAndRedacted(t *testing.T) {
	requests := 0
	var progress bytes.Buffer
	client := Client{APIKey: "fixture-key", Model: "fixture-model", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return apiResponse(200, functionResponse("call_1", "read_task", map[string]string{"task_id": "missing"})), nil
		}
		data, _ := io.ReadAll(r.Body)
		if bytes.Contains(data, []byte("fixture-key")) || !bytes.Contains(data, []byte("denied")) {
			t.Fatalf("bad tool error: %s", data)
		}
		return apiResponse(200, textResponse("The operation was denied.")), nil
	})}}
	err := client.Run(context.Background(), Request{Consent: true, Instruction: "Explain", Progress: &progress, Handle: func(context.Context, ToolCall) (string, error) { return "", errors.New("denied fixture-key") }})
	if err != nil || !strings.Contains(progress.String(), "denied [REDACTED]") {
		t.Fatalf("failure not visible/redacted: %v %s", err, progress.String())
	}
}

func TestHTTPErrorsDoNotExposeBodiesOrRetry(t *testing.T) {
	for _, status := range []int{401, 429, 500, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			client := Client{APIKey: "fixture-key", Model: "fixture-model", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				res := apiResponse(status, map[string]string{"error": "echo fixture-key"})
				res.Header.Set("Location", "https://example.com/stolen")
				return res, nil
			})}}
			err := client.Run(context.Background(), Request{Consent: true, Instruction: "Explain", Handle: func(context.Context, ToolCall) (string, error) { return "", nil }})
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || requests != 1 || strings.Contains(err.Error(), "fixture-key") {
				t.Fatalf("unsafe error handling: %v (%d requests)", err, requests)
			}
		})
	}
}

func TestRequestTimeoutAndCancellation(t *testing.T) {
	client := Client{APIKey: "fixture-key", Model: "fixture-model", RequestTimeout: 10 * time.Millisecond, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}}
	req := Request{Consent: true, Instruction: "Explain", Handle: func(context.Context, ToolCall) (string, error) { return "", nil }}
	if err := client.Run(context.Background(), req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request timeout lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Run(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestResponseAndToolBounds(t *testing.T) {
	t.Run("oversize response", func(t *testing.T) {
		client := Client{APIKey: "fixture-key", Model: "fixture-model", HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return apiResponse(200, textResponse(strings.Repeat("x", maxResponseBytes))), nil
		})}}
		err := client.Run(context.Background(), Request{Consent: true, Instruction: "Explain", Handle: func(context.Context, ToolCall) (string, error) { return "", nil }})
		if err == nil || !strings.Contains(err.Error(), "2 MiB") {
			t.Fatalf("oversize response accepted: %v", err)
		}
	})
	t.Run("bounded tool output", func(t *testing.T) {
		requests := 0
		client := Client{APIKey: "fixture-key", Model: "fixture-model", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				return apiResponse(200, functionResponse("call_1", "list_tasks", map[string]string{})), nil
			}
			data, _ := io.ReadAll(r.Body)
			if len(data) > maxToolBytes+10000 || !bytes.Contains(data, []byte("truncated")) {
				t.Fatal("tool output unbounded")
			}
			return apiResponse(200, textResponse("Observed a truncated result.")), nil
		})}}
		if err := client.Run(context.Background(), Request{Consent: true, Instruction: "Explain", Handle: func(context.Context, ToolCall) (string, error) { return strings.Repeat("x", maxToolBytes*2), nil }}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEndpointRequiresTLSOrLoopback(t *testing.T) {
	for _, endpoint := range []string{"http://remote.example/responses", "https://user:pass@api.openai.com/v1/responses", "https://api.openai.com/v1/responses?key=x", "not a url"} {
		if err := validateEndpoint(endpoint); err == nil {
			t.Fatalf("accepted %q", endpoint)
		}
	}
	for _, endpoint := range []string{DefaultEndpoint, "http://127.0.0.1:1234/responses", "http://[::1]:1234/responses"} {
		if err := validateEndpoint(endpoint); err != nil {
			t.Fatal(err)
		}
	}
}
