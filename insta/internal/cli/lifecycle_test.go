package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/platform"
	"github.com/instaclustr/labs/insta/internal/runner"
	"github.com/instaclustr/labs/insta/internal/state"
)

const lifecycleID = "11111111-2222-4333-8444-555555555555"
const lifecycleDefinition = `{"name":"template-name","slaTier":"NON_PRODUCTION","privateNetworkCluster":false,"pciComplianceMode":false,"opensearchVersion":"3.6.0","clusterManagerNodes":[{"dedicatedManager":true,"nodeSize":"SRH-DM-DEV-t4g.small-5"}],"dataCentres":[{"cloudProvider":"AWS_VPC","name":"AWS_VPC_US_EAST_1","network":"10.0.0.0/16","numberOfRacks":3,"region":"US_EAST_1"}],"dataNodes":[{"nodeCount":3,"nodeSize":"SRH-DEV-t4g.medium-30"}],"loadBalancer":true}`

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func lifecycleClient(handler func(http.ResponseWriter, *http.Request)) *http.Client {
	return &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Result(), nil
	})}
}

func newLifecycleSession(t *testing.T, mode string) *session {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, key := range []string{"OPENSEARCH_HOST", "OPENSEARCH_PORT", "OPENSEARCH_USER", "OPENSEARCH_PASSWORD", "OPENSEARCH_SSL", "INSTA_OPENSEARCH_MODE", "INSTA_ASSISTANT_API_KEY", "INSTA_ASSISTANT_MODEL"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("INSTACLUSTR_PROVISION_USERNAME", "fixture-provision-user")
	t.Setenv("INSTACLUSTR_PROVISION_API_KEY", "fixture-provision-secret")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "support"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "support", "requirements.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deploy-opensearch.json"), []byte(lifecycleDefinition), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := state.New("rag-retrieval", map[string]string{"origin": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	p.Stack().Config["opensearch.mode"] = mode
	p.Stack().ApprovedManifest = "fixture-reviewed-hash"
	m := &manifest.Manifest{SchemaVersion: 1, ID: "rag-retrieval", Name: "Fixture", Runtime: manifest.Runtime{Kind: "python", Python: "3.12", Requirements: "support/requirements.txt"}, Config: map[string]manifest.Definition{
		"OPENSEARCH_HOST": {}, "OPENSEARCH_USER": {}, "OPENSEARCH_PASSWORD": {Secret: true}, "OPENSEARCH_PORT": {Type: "integer", Default: "9200"}, "OPENSEARCH_SSL": {Type: "boolean", Default: "true"},
	}, Platforms: map[string]manifest.Platform{"opensearch": {Provider: "instaclustr-opensearch", Request: "deploy-opensearch.json"}}, Services: map[string]manifest.Service{}, Lifecycle: manifest.Lifecycle{Up: []manifest.Step{{Platform: "opensearch", Action: "ensure"}}}}
	a := New(strings.NewReader(""), io.Discard, io.Discard)
	a.ConfirmFunc = func(string) error { return nil }
	s := &session{App: a, Root: root, Project: p, Manifest: m, Hash: "fixture-reviewed-hash"}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	// This fixture supplies a completed managed runtime, avoiding package/network
	// installation. The separate runtime tests exercise preparation itself.
	py := runner.Python(root, "dev")
	if err := os.MkdirAll(filepath.Dir(py), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(py, []byte("#!/bin/sh\nexit 73\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p.Stack().RuntimeDigest, err = runner.RuntimeDigest(root, m)
	if err != nil {
		t.Fatal(err)
	}
	p.Stack().RuntimeStatus = "ready"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	a.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"green","timed_out":false}`)
	})
	return s
}

func installManagementFixture(t *testing.T, s *session, handler func(http.ResponseWriter, *http.Request)) {
	t.Helper()
	p, err := platform.NewOpenSearch("fixture-provision-user", "fixture-provision-secret")
	if err != nil {
		t.Fatal(err)
	}
	// Keep the original lifecycle fixture focused on cluster requests while
	// supplying realistic, persistent firewall observations for repeated up.
	rules := map[string]bool{}
	p = p.WithHTTPClient(lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == firewallListFixturePath {
			writeFirewallList(w, rules)
			return
		}
		if r.URL.Path == firewallFixturePath {
			var rule platform.FirewallRule
			if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
				t.Error(err)
			}
			rules[rule.Type] = true
			w.WriteHeader(http.StatusAccepted)
			return
		}
		handler(w, r)
	}))
	p.PollInterval = time.Millisecond
	s.App.PlatformClient = p
}

func writeLifecycleCluster(w http.ResponseWriter, name, status string) {
	_, _ = fmt.Fprintf(w, `{"id":%q,"name":%q,"opensearchVersion":"3.6.0","status":%q,"currentClusterOperationStatus":"NO_OPERATION","loadBalancerConnectionUrl":"https://cluster.example.com:9200","defaultUsername":"fixture-data-user","defaultUserPassword":"fixture-data-secret"}`, lifecycleID, name, status)
}

func TestLifecycleProvisionJournalReconcileAndCleanup(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	posts, gets, deletes, confirms := 0, 0, 0, 0
	clusterName := ""
	s.App.ConfirmFunc = func(string) error { confirms++; return nil }
	installManagementFixture(t, s, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			posts++
			p, err := state.Load(s.Root)
			if err != nil {
				t.Error(err)
			} else if p.Stack().Platforms["opensearch"].Status != "create-pending" {
				t.Error("POST preceded pending journal")
			}
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			clusterName, _ = request["name"].(string)
			if clusterName == "template-name" || !strings.Contains(clusterName, s.Project.ID[:12]) {
				t.Error("create name lacks project ownership suffix")
			}
			w.WriteHeader(http.StatusAccepted)
			writeLifecycleCluster(w, clusterName, "PROVISIONING")
		case http.MethodGet:
			gets++
			if deletes > 0 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			p, err := state.Load(s.Root)
			if err != nil {
				t.Error(err)
			} else if p.Stack().Platforms["opensearch"].ID != lifecycleID {
				t.Error("polled before recording ID")
			}
			secrets, err := state.ReadSecrets(s.Project.ID, "dev")
			if err != nil || secrets["provider.OPENSEARCH_PASSWORD"] != "fixture-data-secret" {
				t.Error("initial credentials not preserved before polling")
			}
			writeLifecycleCluster(w, clusterName, "RUNNING")
		case http.MethodDelete:
			deletes++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	s.App.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture-data-user" || password != "fixture-data-secret" {
			t.Error("wrong application credentials")
		}
		if r.URL.Host != "cluster.example.com:9200" || r.URL.Path != "/_cluster/health" {
			t.Error("wrong data-plane endpoint")
		}
		_, _ = io.WriteString(w, `{"status":"green"}`)
	})
	if err := s.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || gets != 4 || confirms != 1 || len(s.Project.Stack().Operations) != 0 {
		t.Fatalf("unexpected effects: posts=%d gets=%d confirmations=%d", posts, gets, confirms)
	}
	record := s.Project.Stack().Platforms["opensearch"]
	if record.ID != lifecycleID || !record.Owned || record.Status != "ready" {
		t.Fatal("missing readiness or ownership")
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, ".insta", "project.json"))
	if strings.Contains(string(b), "fixture-data-secret") || strings.Contains(string(b), "fixture-provision-secret") {
		t.Fatal("state contains credentials")
	}
	if err := os.WriteFile(filepath.Join(s.Root, "learner-edits.py"), []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 || len(s.Project.Stack().Platforms) != 0 || s.Project.Stack().Outputs["OPENSEARCH_HOST"] != "" {
		t.Fatal("owned cleanup incomplete")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "learner-edits.py")); err != nil {
		t.Fatal("cleanup removed learner edits")
	}
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatal("repeated cleanup performed extra delete")
	}
}

func TestLifecycleMissingProvisioningCredentialsPrecedeAllEffects(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	t.Setenv("INSTACLUSTR_PROVISION_USERNAME", "")
	s.Project.Stack().RuntimeStatus = "pending"
	s.Project.Stack().ApprovedManifest = "unapproved"
	s.App.ConfirmFunc = func(string) error { t.Error("prompted without provisioning credentials"); return nil }
	installManagementFixture(t, s, func(http.ResponseWriter, *http.Request) { t.Error("made platform request") })
	if err := s.Up(context.Background()); err == nil {
		t.Fatal("accepted missing credentials")
	}
	if s.Project.Stack().RuntimeStatus != "pending" || len(s.Project.Stack().Platforms) != 0 {
		t.Fatal("started setup before credential gate")
	}
}

func TestLifecycleAttachDoesNotNeedManagementOrDeleteSharedCluster(t *testing.T) {
	s := newLifecycleSession(t, "attach")
	t.Setenv("INSTACLUSTR_PROVISION_USERNAME", "")
	t.Setenv("INSTACLUSTR_PROVISION_API_KEY", "")
	s.Project.Stack().Config["OPENSEARCH_HOST"] = "existing.example.com"
	s.Project.Stack().Config["OPENSEARCH_USER"] = "existing-user"
	if err := state.SaveSecrets(s.Project.ID, "dev", map[string]string{"OPENSEARCH_PASSWORD": "existing-password"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	installManagementFixture(t, s, func(http.ResponseWriter, *http.Request) { t.Error("attach/cleanup used management API") })
	if err := s.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := s.Project.Stack().Platforms["opensearch"]; r.Owned || r.Status != "attached" {
		t.Fatal("attachment was treated as owned")
	}
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Project.Stack().Platforms["opensearch"] == nil || s.Project.Stack().Config["OPENSEARCH_HOST"] != "existing.example.com" {
		t.Fatal("shared configuration not preserved")
	}
}

func TestLifecycleAmbiguousCreateNeverRepeatsAndRecoversExactName(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	posts := 0
	found := false
	installManagementFixture(t, s, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "fixture-provision-secret")
			return
		}
		if strings.Contains(r.URL.Path, "/data-sources/") {
			if !found {
				_, _ = io.WriteString(w, `[]`)
				return
			}
			fmt.Fprintf(w, `[{"clusters":[{"id":%q,"application":"OPENSEARCH"}]}]`, lifecycleID)
			return
		}
		writeLifecycleCluster(w, s.Project.Stack().Platforms["opensearch"].Name, "RUNNING")
	})
	if err := s.Up(context.Background()); err == nil {
		t.Fatal("ambiguous POST was reported ready")
	}
	record := s.Project.Stack().Platforms["opensearch"]
	if record.Status != "create-uncertain" || record.ID != "" || strings.Contains(record.Error, "fixture-provision-secret") {
		t.Fatal("ambiguous ownership or secret handling is incorrect")
	}
	if err := s.Up(context.Background()); err == nil {
		t.Fatal("absence search authorized retry")
	}
	if posts != 1 {
		t.Fatal("billable create retried")
	}
	found = true
	if err := s.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || s.Project.Stack().Platforms["opensearch"].ID != lifecycleID {
		t.Fatal("recovery created another cluster")
	}
}

func TestLifecycleDataPlaneFailureRetainsOwnedCleanup(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	posts, deletes := 0, 0
	installManagementFixture(t, s, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			w.WriteHeader(http.StatusAccepted)
		}
		if r.Method == http.MethodDelete {
			deletes++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet && deletes > 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeLifecycleCluster(w, generatedClusterName(filepath.Base(s.Root), "dev", s.Project.ID), "RUNNING")
	})
	s.App.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	if err := s.Up(context.Background()); err == nil {
		t.Fatal("management readiness mistaken for data-plane readiness")
	}
	r := s.Project.Stack().Platforms["opensearch"]
	if r.ID != lifecycleID || r.Status != "awaiting-connection" || !r.Owned {
		t.Fatal("failed health check lost resource")
	}
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || deletes != 1 {
		t.Fatal("cleanup required data-plane authentication")
	}
}

func TestLifecycleCleanupFailureRetainsJournalAndCanRetry(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	deletes := 0
	s.Project.Stack().Platforms["opensearch"] = &state.Platform{Provider: "instaclustr-opensearch", ID: lifecycleID, Owned: true, Status: "ready"}
	installManagementFixture(t, s, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
			if deletes == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	if err := s.Down(context.Background()); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	if r := s.Project.Stack().Platforms["opensearch"]; r == nil || r.ID != lifecycleID || r.Status != "deleting" {
		t.Fatal("failed cleanup discarded ownership")
	}
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.Project.Stack().Platforms) != 0 || deletes != 2 {
		t.Fatal("cleanup retry failed")
	}
}

func TestLifecycleConflictingEndpointBlocksTaskButNotCleanup(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	s.Project.Stack().Platforms["opensearch"] = &state.Platform{Provider: "instaclustr-opensearch", ID: lifecycleID, Owned: true, Status: "ready"}
	s.Project.Stack().Outputs["OPENSEARCH_HOST"] = "owned.example.com"
	s.Project.Stack().Config["OPENSEARCH_HOST"] = "unrelated.example.com"
	if err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckPlatforms(context.Background(), []string{"opensearch"}); err == nil {
		t.Fatal("combined owned credentials and unrelated endpoint")
	}
	installManagementFixture(t, s, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleReadinessGatesIngestAndQuery(t *testing.T) {
	for _, status := range []string{"awaiting-running", "awaiting-firewall", "awaiting-connection", "ready"} {
		t.Run(status, func(t *testing.T) {
			s := newLifecycleSession(t, "provision")
			s.Project.Stack().Platforms["opensearch"] = &state.Platform{Provider: "instaclustr-opensearch", ID: lifecycleID, Owned: true, Status: status}
			s.Values["OPENSEARCH_HOST"], s.Values["OPENSEARCH_USER"], s.Values["OPENSEARCH_PASSWORD"] = "cluster.example.com", "fixture-user", "fixture-password"
			s.Manifest.Tasks = map[string]manifest.Task{"example": {
				Source: ".", Requires: manifest.Requirements{Platforms: []string{"opensearch"}},
				Operations: map[string]manifest.Operation{
					"ingest": {Command: []string{"python", "-m", "src.ingest"}},
					"query":  {Command: []string{"python", "-m", "src.query"}},
				},
			}}
			probes := 0
			s.App.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				probes++
				if r.URL.Path != "/_cluster/health" {
					t.Fatalf("task reached data access before readiness: %s", r.URL)
				}
				io.WriteString(w, `{"status":"red","timed_out":false}`)
			})
			for _, operation := range []string{"ingest", "query"} {
				if _, err := s.RunTask(firewallContext(t), "example", operation); err == nil {
					t.Fatalf("%s ran before platform readiness", operation)
				}
			}
			if len(s.Project.Stack().Operations) != 0 {
				t.Fatal("ingest or query process started before platform readiness")
			}
			wantProbes := 0
			if status == "ready" {
				wantProbes = 2
			}
			if probes != wantProbes {
				t.Fatalf("unexpected data-plane checks: got %d, want %d", probes, wantProbes)
			}
		})
	}
}

func TestLifecycleDependencyOrderAndStaleServiceNeverKillsBarePID(t *testing.T) {
	s := newLifecycleSession(t, "attach")
	s.Manifest.Services = map[string]manifest.Service{"ner": {}, "news": {Requires: manifest.Requirements{Services: []string{"ner"}}}, "agent": {Requires: manifest.Requirements{Services: []string{"news"}}}}
	for _, name := range []string{"agent", "ner", "news"} {
		s.Project.Stack().Services[name] = &state.Service{PID: os.Getpid(), Token: strings.Repeat("a", 32), Socket: filepath.Join(s.Root, "missing-"+name+".sock")}
	}
	order := s.serviceOrder()
	if strings.Join(order, ",") != "ner,news,agent" {
		t.Fatalf("wrong dependency order: %v", order)
	}
	if err := s.StopService(context.Background(), "ner"); err == nil {
		t.Fatal("unconfirmed stale services were discarded")
	}
	if len(s.Project.Stack().Services) != 3 {
		t.Fatal("uncertain ownership records were lost")
	}
	for _, record := range s.Project.Stack().Services {
		if err := state.WriteJSON(record.Socket+".done", map[string]string{"token": record.Token}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.StopService(context.Background(), "ner"); err != nil {
		t.Fatal("matching cleanup receipts were not recognized", err)
	}
	if s.Project.Stack().Services["ner"] != nil {
		t.Fatal("stale service not reconciled")
	}
}

func TestLifecycleDownStopsLocalServicesWithoutManagementCredentials(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	s.Project.Stack().Platforms["opensearch"] = &state.Platform{Provider: "instaclustr-opensearch", ID: lifecycleID, Owned: true, Status: "ready"}
	s.Manifest.Services["ner"] = manifest.Service{}
	s.Project.Stack().Services["ner"] = &state.Service{PID: os.Getpid(), Token: strings.Repeat("a", 32), Socket: filepath.Join(s.Root, "missing.sock"), Owner: "stack"}
	record := s.Project.Stack().Services["ner"]
	if err := state.WriteJSON(record.Socket+".done", map[string]string{"token": record.Token}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INSTACLUSTR_PROVISION_API_KEY", "")
	if err := s.Down(context.Background()); err == nil {
		t.Fatal("remote cleanup reported successful without credentials")
	}
	if len(s.Project.Stack().Services) != 0 || s.Project.Stack().Platforms["opensearch"].ID != lifecycleID {
		t.Fatal("local cleanup or remote ownership retention failed")
	}
}

func TestLifecycleRestartPreservesUnconfirmedWorker(t *testing.T) {
	s := newLifecycleSession(t, "attach")
	s.Manifest.Services["ner"] = manifest.Service{Cwd: ".", Command: []string{"python", "service.py"}, Probe: manifest.Probe{URL: "http://127.0.0.1:9999/health"}}
	record := &state.Service{PID: os.Getpid(), Token: strings.Repeat("a", 32), Socket: filepath.Join(s.Root, ".insta", "run", "ner.sock"), Owner: "stack"}
	s.Project.Stack().Services["ner"] = record
	if err := s.StartService(context.Background(), "ner", "stack"); err == nil {
		t.Fatal("restarted after a missing socket without proof of cleanup")
	}
	if s.Project.Stack().Services["ner"] != record {
		t.Fatal("unconfirmed owner was replaced")
	}
}
