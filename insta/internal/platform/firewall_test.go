package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestListFirewallRulesUsesV2EnvelopeAndAuthentication(t *testing.T) {
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/cluster-management/v2/data-sources/cluster/"+testID+"/network-firewall-rules/v2" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		u, k, ok := r.BasicAuth()
		if !ok || u != "fixture-user" || k != "fixture-provision-secret" {
			t.Error("wrong management credentials")
		}
		fmt.Fprintf(w, `[{"clusterId":%q,"firewallRules":[{"id":%q,"clusterId":%q,"type":"OPENSEARCH","network":"0.0.0.0/0","status":"RUNNING"},{"type":"OPENSEARCH_DASHBOARDS","network":"0.0.0.0/0","status":"GENESIS"}]}]`, testID, otherID, testID)
	})
	rules, err := p.ListFirewallRules(context.Background(), testID)
	if err != nil || len(rules) != 2 {
		t.Fatalf("rules=%v error=%v", rules, err)
	}
	if rules[0].Type != "OPENSEARCH" || rules[1].ClusterID != testID || rules[1].Status != "GENESIS" {
		t.Fatal("lost rule fields")
	}
}

func TestListFirewallRulesRejectsInvalidAndWrongClusterResponses(t *testing.T) {
	for _, body := range []string{
		`{`, `{}`, fmt.Sprintf(`[{"clusterId":%q,"firewallRules":[]}]`, otherID),
		fmt.Sprintf(`[{"clusterId":%q,"firewallRules":[{"clusterId":%q}]}]`, testID, otherID),
	} {
		t.Run(body, func(t *testing.T) {
			p := localProvider(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			if _, err := p.ListFirewallRules(context.Background(), testID); err == nil {
				t.Fatal("invalid list accepted")
			}
		})
	}
}

func TestCreateFirewallRulesExactPayloadAndDuplicateResponse(t *testing.T) {
	for _, kind := range []string{"OPENSEARCH", "OPENSEARCH_DASHBOARDS"} {
		for _, status := range []int{http.StatusAccepted, http.StatusConflict} {
			t.Run(fmt.Sprintf("%s/%d", kind, status), func(t *testing.T) {
				calls := 0
				p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodPost || r.URL.Path != firewallPath {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					}
					u, k, ok := r.BasicAuth()
					if !ok || u != "fixture-user" || k != "fixture-provision-secret" {
						t.Error("wrong management authentication")
					}
					if r.Header.Get("Content-Type") != "application/json" {
						t.Error("missing JSON content type")
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body) != 3 || body["clusterId"] != testID || body["type"] != kind || body["network"] != "0.0.0.0/0" {
						t.Errorf("wrong payload: %v", body)
					}
					w.WriteHeader(status)
					io.WriteString(w, `{"status":"GENESIS"}`)
				})
				if err := p.CreateFirewallRule(context.Background(), testID, kind, "0.0.0.0/0"); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("unexpected requests: %d", calls)
				}
			})
		}
	}
}

func TestFirewallFailuresDoNotRetryPOSTOrExposeResponseSecrets(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				io.WriteString(w, "fixture-provision-secret")
			})
			err := p.CreateFirewallRule(context.Background(), testID, "OPENSEARCH", "0.0.0.0/0")
			var h *HTTPError
			if !errors.As(err, &h) || h.StatusCode != status || calls != 1 {
				t.Fatalf("unexpected failure: %v; calls=%d", err, calls)
			}
			if strings.Contains(err.Error(), "fixture-provision-secret") {
				t.Fatal("response secret leaked")
			}
		})
	}
}

func TestFirewallInputValidationPrecedesRequests(t *testing.T) {
	p := localProvider(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid request contacted provider") })
	if _, err := p.ListFirewallRules(context.Background(), "../invalid"); err == nil {
		t.Fatal("invalid ID accepted")
	}
	for _, args := range [][3]string{{"bad-id", "OPENSEARCH", "0.0.0.0/0"}, {testID, "KAFKA", "0.0.0.0/0"}, {testID, "OPENSEARCH", "not-a-cidr"}} {
		if err := p.CreateFirewallRule(context.Background(), args[0], args[1], args[2]); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestFirewallAmbiguousPOSTReturnsForNextRunReconciliation(t *testing.T) {
	p, _ := NewOpenSearch("fixture-user", "fixture-secret")
	calls := 0
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("fixture-secret connection interrupted")
	})
	err := p.CreateFirewallRule(context.Background(), testID, "OPENSEARCH_DASHBOARDS", "0.0.0.0/0")
	if err == nil || calls != 1 || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("unsafe failure: %v, calls=%d", err, calls)
	}
}
