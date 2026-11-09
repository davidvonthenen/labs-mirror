package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/instaclustr/labs/insta/internal/platform"
	"github.com/instaclustr/labs/insta/internal/state"
)

const firewallFixturePath = "/cluster-management/v2/resources/network-firewall-rules/v2"
const firewallListFixturePath = "/cluster-management/v2/data-sources/cluster/" + lifecycleID + "/network-firewall-rules/v2"

func writeFirewallList(w http.ResponseWriter, rules map[string]bool) {
	observed := []platform.FirewallRule{}
	for _, kind := range []string{"OPENSEARCH", "OPENSEARCH_DASHBOARDS"} {
		if rules[kind] {
			observed = append(observed, platform.FirewallRule{ClusterID: lifecycleID, Network: "0.0.0.0/0", Type: kind, Status: "GENESIS"})
		}
	}
	json.NewEncoder(w).Encode([]any{map[string]any{"clusterId": lifecycleID, "firewallRules": observed}})
}

type firewallFixture struct {
	rules                         map[string]bool
	posts                         map[string]int
	creates, deletes, lists, gets int
	probes, provisioningGets      int
	clusterStatus                 string
	runningObserved               bool
	afterGet                      func()
	failDashboard                 bool
	acceptBeforeFailure           bool
	deleted                       bool
}

func firewallContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func recordFirewallProbe(t *testing.T, s *session, f *firewallFixture) {
	t.Helper()
	f.probes++
	if !f.runningObserved || !f.rules["OPENSEARCH"] || !f.rules["OPENSEARCH_DASHBOARDS"] {
		t.Fatal("data-plane probe preceded running state and both firewall submissions")
	}
	saved, err := state.Load(s.Root)
	if err != nil || saved.Stack().Platforms["opensearch"].Status != "awaiting-connection" {
		t.Fatal("platform was not durably awaiting data-plane readiness during its probe")
	}
}

func newFirewallFixture(t *testing.T, s *session, existing bool) *firewallFixture {
	t.Helper()
	f := &firewallFixture{rules: map[string]bool{}, posts: map[string]int{}, provisioningGets: 1}
	if existing {
		_, info, err := s.deploymentRequest("opensearch")
		if err != nil {
			t.Fatal(err)
		}
		s.Project.Stack().Platforms["opensearch"] = &state.Platform{Provider: "instaclustr-opensearch", ID: lifecycleID, Name: info.Name, Owned: true, Status: "awaiting-connection", RequestDigest: info.Digest}
		if err := s.Save(); err != nil {
			t.Fatal(err)
		}
	}
	p, err := platform.NewOpenSearch("fixture-provision-user", "fixture-provision-secret")
	if err != nil {
		t.Fatal(err)
	}
	p = p.WithHTTPClient(lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
		user, key, ok := r.BasicAuth()
		if !ok || user != "fixture-provision-user" || key != "fixture-provision-secret" {
			t.Error("incorrect management authentication")
		}
		if r.URL.Path == firewallListFixturePath || r.URL.Path == firewallFixturePath {
			if !f.runningObserved {
				t.Fatal("firewall request preceded management RUNNING state")
			}
			saved, err := state.Load(s.Root)
			if err != nil || saved.Stack().Platforms["opensearch"].ID != lifecycleID || saved.Stack().Platforms["opensearch"].Status != "awaiting-firewall" {
				t.Fatal("firewall request preceded durable ownership and awaiting-firewall state")
			}
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == firewallListFixturePath:
			f.lists++
			writeFirewallList(w, f.rules)
		case r.Method == http.MethodPost && r.URL.Path == firewallFixturePath:
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 3 || payload["clusterId"] != lifecycleID || payload["network"] != "0.0.0.0/0" {
				t.Errorf("incorrect firewall payload: %v", payload)
			}
			kind := payload["type"]
			if kind != "OPENSEARCH" && kind != "OPENSEARCH_DASHBOARDS" {
				t.Fatalf("unexpected type %q", kind)
			}
			f.posts[kind]++
			if kind == "OPENSEARCH_DASHBOARDS" && f.failDashboard {
				if f.acceptBeforeFailure {
					f.rules[kind] = true
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				io.WriteString(w, "fixture-provision-secret")
				return
			}
			f.rules[kind] = true
			w.WriteHeader(http.StatusAccepted)
			io.WriteString(w, `{"status":"GENESIS"}`)
		case strings.HasPrefix(r.URL.Path, "/cluster-management/v2/resources/applications/opensearch/clusters/v2/"):
			switch r.Method {
			case http.MethodPost:
				f.creates++
				saved, err := state.Load(s.Root)
				if err != nil || saved.Stack().Platforms["opensearch"].Status != "create-pending" {
					t.Fatal("create preceded journal")
				}
				w.WriteHeader(http.StatusAccepted)
				writeLifecycleCluster(w, s.Project.Stack().Platforms["opensearch"].Name, "PROVISIONING")
			case http.MethodDelete:
				f.deletes++
				f.deleted = true
				w.WriteHeader(http.StatusNoContent)
			case http.MethodGet:
				f.gets++
				if f.deleted {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				status := f.clusterStatus
				if status == "" {
					status = "RUNNING"
					if f.gets <= f.provisioningGets {
						status = "PROVISIONING"
					}
				}
				operation := "NO_OPERATION"
				if status == "PROVISIONING" {
					operation = "OPERATION_IN_PROGRESS"
				}
				if !f.runningObserved {
					saved, err := state.Load(s.Root)
					if err != nil || saved.Stack().Platforms["opensearch"].ID != lifecycleID || saved.Stack().Platforms["opensearch"].Status != "awaiting-running" {
						t.Fatal("management polling preceded durable ownership and awaiting-running state")
					}
				}
				f.runningObserved = status == "RUNNING"
				fmt.Fprintf(w, `{"id":%q,"status":%q,"currentClusterOperationStatus":%q,"loadBalancerConnectionUrl":"https://cluster.example.com:9200","defaultUsername":"fixture-data-user","defaultUserPassword":"fixture-data-secret","dataCentres":[{"status":%q,"nodes":[{"status":"RUNNING","nodeRoles":["OPENSEARCH_DATA"]},{"status":%q,"nodeRoles":["OPENSEARCH_DASHBOARDS"]}]}]}`, lifecycleID, status, operation, status, status)
				if f.afterGet != nil {
					f.afterGet()
				}
			default:
				t.Errorf("unexpected method %s", r.Method)
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			t.Errorf("unexpected management request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	p.PollInterval = time.Millisecond
	s.App.PlatformClient = p
	s.App.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
		recordFirewallProbe(t, s, f)
		io.WriteString(w, `{"status":"green","timed_out":false}`)
	})
	return f
}

func TestFirewallExistingClusterWaitsForRunningThenDataPlane(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	f := newFirewallFixture(t, s, true)
	confirms, probes := 0, 0
	s.App.ConfirmFunc = func(plan string) error {
		confirms++
		for _, text := range []string{lifecycleID, "OPENSEARCH", "OPENSEARCH_DASHBOARDS", "0.0.0.0/0", "public Internet", "No cluster will be created"} {
			if !strings.Contains(plan, text) {
				t.Errorf("missing approval detail: %s", text)
			}
		}
		return nil
	}
	s.App.DataClient = &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		probes++
		recordFirewallProbe(t, s, f)
		if r.URL.Host != "cluster.example.com:9200" || r.URL.Path != "/_cluster/health" {
			t.Fatalf("unexpected endpoint %s", r.URL)
		}
		user, key, ok := r.BasicAuth()
		if !ok || user != "fixture-data-user" || key != "fixture-data-secret" {
			t.Fatal("wrong data-plane credentials")
		}
		if probes == 1 {
			return nil, errors.New("connection refused")
		}
		status, body := http.StatusOK, `{"status":"yellow","timed_out":false}`
		if probes == 2 {
			status, body = http.StatusServiceUnavailable, "unavailable"
		}
		if probes == 3 {
			body = `{"status":"red","timed_out":true}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if err := s.Up(firewallContext(t)); err != nil {
		t.Fatal(err)
	}
	if err := s.Up(firewallContext(t)); err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 || f.posts["OPENSEARCH"] != 1 || f.posts["OPENSEARCH_DASHBOARDS"] != 1 || confirms != 1 || probes != 5 {
		t.Fatalf("unexpected effects: %+v confirms=%d probes=%d", f, confirms, probes)
	}
	if record := s.Project.Stack().Platforms["opensearch"]; record.Status != "ready" || record.ID != lifecycleID {
		t.Fatal("resume changed ownership or lost readiness")
	}
	s.App.ConfirmFunc = func(string) error { return nil }
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 1 || len(s.Project.Stack().Platforms) != 0 {
		t.Fatal("owned cleanup failed")
	}
}

func TestFirewallNewClusterRecordsIDAndApprovesBothRulesBeforeCreate(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	f := newFirewallFixture(t, s, false)
	confirms := 0
	s.App.ConfirmFunc = func(plan string) error {
		confirms++
		for _, text := range []string{"Provision billable", "OPENSEARCH", "OPENSEARCH_DASHBOARDS", "0.0.0.0/0", "WARNING"} {
			if !strings.Contains(plan, text) {
				t.Errorf("missing approval detail %q", text)
			}
		}
		if f.creates != 0 || len(f.posts) != 0 {
			t.Fatal("effects preceded approval")
		}
		return nil
	}
	if err := s.Up(firewallContext(t)); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 || confirms != 1 || f.posts["OPENSEARCH"] != 1 || f.posts["OPENSEARCH_DASHBOARDS"] != 1 {
		t.Fatalf("unexpected effects: %+v confirmations=%d", f, confirms)
	}
}

func TestFirewallPartialFailureReconcilesAfterReloadWithoutAnotherCluster(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprintf("accepted-before-failure=%v", accepted), func(t *testing.T) {
			s := newLifecycleSession(t, "provision")
			f := newFirewallFixture(t, s, true)
			f.failDashboard, f.acceptBeforeFailure = true, accepted
			if err := s.Up(firewallContext(t)); err == nil {
				t.Fatal("firewall failure hidden")
			}
			if f.probes != 0 {
				t.Fatal("data-plane readiness started after failed firewall submission")
			}
			p, err := state.Load(s.Root)
			if err != nil {
				t.Fatal(err)
			}
			record := p.Stack().Platforms["opensearch"]
			if record.ID != lifecycleID || !record.Owned || record.Status != "awaiting-firewall" {
				t.Fatal("partial failure lost owned cluster")
			}
			if strings.Contains(record.Error, "fixture-provision-secret") {
				t.Fatal("error leaked credentials")
			}
			s.Project = p
			if err := s.Refresh(); err != nil {
				t.Fatal(err)
			}
			f.failDashboard = false
			if err := s.Up(firewallContext(t)); err != nil {
				t.Fatal(err)
			}
			expectedDashboardPosts := 2
			if accepted {
				expectedDashboardPosts = 1
			}
			if f.creates != 0 || f.posts["OPENSEARCH"] != 1 || f.posts["OPENSEARCH_DASHBOARDS"] != expectedDashboardPosts {
				t.Fatalf("incorrect reconciliation: %+v", f)
			}
		})
	}
}

func TestFirewallDeclinedConfirmationRetainsClusterWithoutOpeningAccess(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	f := newFirewallFixture(t, s, true)
	s.App.ConfirmFunc = func(string) error { return errors.New("action was not confirmed") }
	if err := s.Up(firewallContext(t)); err == nil {
		t.Fatal("declined action succeeded")
	}
	if f.creates != 0 || len(f.posts) != 0 || f.probes != 0 {
		t.Fatalf("declined approval had effects: %+v", f)
	}
	saved, err := state.Load(s.Root)
	if err != nil || saved.Stack().Platforms["opensearch"].ID != lifecycleID {
		t.Fatal("decline lost ownership")
	}
}

func TestFirewallReadinessCancellationRetainsRetryableOwnership(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	f := newFirewallFixture(t, s, true)
	f.provisioningGets = 0
	f.rules["OPENSEARCH"], f.rules["OPENSEARCH_DASHBOARDS"] = true, true
	s.App.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
		recordFirewallProbe(t, s, f)
		io.WriteString(w, `{"status":"red"}`)
	})
	s.App.PlatformClient.PollInterval = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := s.Up(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if record := s.Project.Stack().Platforms["opensearch"]; record.ID != lifecycleID || record.Status != "awaiting-connection" {
		t.Fatal("failed polling discarded ownership")
	}
	if len(f.posts) != 0 || f.creates != 0 {
		t.Fatal("existing resources recreated")
	}
}

func TestFirewallProvisioningInterruptionResumesTheRecordedCluster(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancellation), func(t *testing.T) {
			s := newLifecycleSession(t, "provision")
			f := newFirewallFixture(t, s, false)
			f.clusterStatus = "PROVISIONING"
			s.App.PlatformClient.PollInterval = time.Hour
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			want := context.DeadlineExceeded
			if cancellation {
				f.afterGet = cancel
				want = context.Canceled
			}
			err := s.Up(ctx)
			if !errors.Is(err, want) {
				t.Fatalf("provisioning wait lost cancellation: %v", err)
			}
			if f.creates != 1 || f.gets != 1 || f.lists != 0 || len(f.posts) != 0 || f.probes != 0 {
				t.Fatalf("provisioning interruption performed premature work: %+v", f)
			}
			saved, err := state.Load(s.Root)
			if err != nil {
				t.Fatal(err)
			}
			record := saved.Stack().Platforms["opensearch"]
			if record.ID != lifecycleID || !record.Owned || record.Status != "awaiting-running" || record.Error == "" {
				t.Fatal("interrupted provisioning did not preserve owned recovery state")
			}
			// A new invocation resumes management polling for the original ID.
			s.Project = saved
			if err := s.Refresh(); err != nil {
				t.Fatal(err)
			}
			f.clusterStatus, f.afterGet = "", nil
			s.App.PlatformClient.PollInterval = time.Millisecond
			if err := s.Up(firewallContext(t)); err != nil {
				t.Fatal(err)
			}
			if f.creates != 1 || f.posts["OPENSEARCH"] != 1 || f.posts["OPENSEARCH_DASHBOARDS"] != 1 || f.probes != 1 {
				t.Fatalf("resumed provisioning duplicated resources or skipped readiness: %+v", f)
			}
			record = s.Project.Stack().Platforms["opensearch"]
			if record.ID != lifecycleID || !record.Owned || record.Status != "ready" || record.Error != "" {
				t.Fatal("resumed cluster did not reach ready with its original ownership")
			}
		})
	}
}

func TestFirewallTerminalProvisioningFailureRetainsOwnedCleanup(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	f := newFirewallFixture(t, s, false)
	f.clusterStatus = "FAILED"
	if err := s.Up(firewallContext(t)); err == nil {
		t.Fatal("terminal provisioning failure was reported ready")
	}
	if f.creates != 1 || f.gets != 1 || f.lists != 0 || len(f.posts) != 0 || f.probes != 0 {
		t.Fatalf("failed provisioning reached firewall or data-plane operations: %+v", f)
	}
	saved, err := state.Load(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	record := saved.Stack().Platforms["opensearch"]
	if record.ID != lifecycleID || !record.Owned || record.Status != "awaiting-running" || record.Error == "" {
		t.Fatal("terminal provisioning failure discarded owned recovery state")
	}
	if err := s.Down(firewallContext(t)); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 1 || len(s.Project.Stack().Platforms) != 0 {
		t.Fatal("failed provisioning prevented owned cleanup")
	}
}

func TestDataPlaneReadinessFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		body              string
		pending, succeeds bool
	}{
		{"green", 200, `{"status":"green"}`, false, true},
		{"yellow", 200, `{"status":"yellow"}`, false, true},
		{"red", 200, `{"status":"red"}`, true, false},
		{"server-timeout", 200, `{"status":"yellow","timed_out":true}`, true, false},
		{"bad-json", 200, `{`, false, false},
		{"unauthorized", 401, "no", false, false},
		{"forbidden", 403, "no", false, false},
		{"throttled", 429, "no", true, false},
		{"request-timeout", 408, "no", true, false},
		{"startup", 503, "no", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLifecycleSession(t, "attach")
			s.Values["OPENSEARCH_HOST"], s.Values["OPENSEARCH_USER"], s.Values["OPENSEARCH_PASSWORD"] = "cluster.example.com", "user", "password"
			s.App.DataClient = lifecycleClient(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "999999")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			err := s.checkDataPlane(context.Background())
			var pending *platform.ReadinessPendingError
			if (err == nil) != tc.succeeds || errors.As(err, &pending) != tc.pending {
				t.Fatalf("incorrect classification: %v", err)
			}
			if pending != nil && pending.RetryAfter > 5*time.Minute {
				t.Fatal("unbounded retry-after")
			}
		})
	}
}

func TestDataPlaneNetworkRetryButNotCertificateFailure(t *testing.T) {
	for _, tlsFailure := range []bool{false, true} {
		s := newLifecycleSession(t, "attach")
		s.Values["OPENSEARCH_HOST"], s.Values["OPENSEARCH_USER"], s.Values["OPENSEARCH_PASSWORD"] = "cluster.example.com", "user", "password"
		s.App.DataClient = &http.Client{Transport: lifecycleTransport(func(*http.Request) (*http.Response, error) {
			if tlsFailure {
				return nil, &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
			}
			return nil, errors.New("fixture-data-secret: connection refused")
		})}
		err := s.checkDataPlane(context.Background())
		var pending *platform.ReadinessPendingError
		if err == nil || errors.As(err, &pending) == tlsFailure || strings.Contains(err.Error(), "fixture-data-secret") {
			t.Fatalf("unsafe classification: %v", err)
		}
	}
}

func TestFirewallFailureStillAllowsOwnedClusterCleanup(t *testing.T) {
	s := newLifecycleSession(t, "provision")
	f := newFirewallFixture(t, s, true)
	f.failDashboard = true
	if err := s.Up(firewallContext(t)); err == nil {
		t.Fatal("expected partial firewall failure")
	}
	if err := os.WriteFile(filepath.Join(s.Root, "learner.txt"), []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 1 || len(s.Project.Stack().Platforms) != 0 {
		t.Fatal("cleanup failed")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "learner.txt")); err != nil {
		t.Fatal("cleanup deleted source")
	}
}
