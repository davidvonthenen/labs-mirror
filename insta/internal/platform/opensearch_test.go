package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testID = "11111111-2222-4333-8444-555555555555"
const otherID = "66666666-7777-4888-8999-aaaaaaaaaaaa"
const definition = `{"name":"my-rag-dev-1234","slaTier":"NON_PRODUCTION","privateNetworkCluster":false,"pciComplianceMode":false,"opensearchVersion":"3.6.0","clusterManagerNodes":[{"dedicatedManager":true,"nodeSize":"SRH-DM-DEV-t4g.small-5"}],"dataCentres":[{"cloudProvider":"AWS_VPC","name":"AWS_VPC_US_EAST_1","network":"10.0.0.0/16","numberOfRacks":3,"region":"US_EAST_1"}],"dataNodes":[{"nodeCount":3,"nodeSize":"SRH-DEV-t4g.medium-30"}],"aiSearchPlugin":true,"loadBalancer":true}`

func localProvider(t *testing.T, h http.HandlerFunc) *OpenSearch {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	p, err := NewOpenSearch("fixture-user", "fixture-provision-secret")
	if err != nil {
		t.Fatal(err)
	}
	p.baseURL = s.URL
	p.PollInterval = time.Millisecond
	return p
}

func TestCreatePreservesDefinitionAndRecordsImmediately(t *testing.T) {
	var calls atomic.Int32
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != clusterPath {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		user, key, ok := r.BasicAuth()
		if !ok || user != "fixture-user" || key != "fixture-provision-secret" {
			t.Error("wrong Basic authentication")
		}
		b, _ := io.ReadAll(r.Body)
		if !bytes.Equal(b, []byte(definition)) {
			t.Error("deployment definition was changed")
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"id":%q,"name":"my-rag-dev-1234","status":"PROVISIONING","defaultUsername":"fixture-db-user","defaultUserPassword":"fixture-db-password"}`, testID)
	})
	recorded := false
	c, err := p.Create(context.Background(), []byte(definition), func(c Cluster) error {
		if c.ID != testID || calls.Load() != 1 {
			t.Error("record did not happen immediately after create")
		}
		user, password := c.Credentials()
		if user != "fixture-db-user" || password != "fixture-db-password" {
			t.Error("API credentials were lost")
		}
		recorded = true
		return nil
	})
	if err != nil || !recorded || c.ID != testID || calls.Load() != 1 {
		t.Fatalf("create failed or performed extra requests: %v", err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{string(b), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(text, "fixture-db-") {
			t.Fatal("credential exposed by serialization")
		}
	}
}

func TestAmbiguousCreatesAreNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		ambiguous bool
	}{
		{"server-failure", 503, `fixture-provision-secret`, true},
		{"accepted-without-id", 202, `{}`, true},
		{"accepted-invalid-json", 202, `{`, true},
		{"unexpected-success", 201, `{}`, true},
		{"bad-definition", 400, `fixture-provision-secret`, false},
		{"unauthorized", 401, `fixture-provision-secret`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := p.Create(context.Background(), []byte(definition), func(Cluster) error { t.Error("unexpected record callback"); return nil })
			var ambiguous *AmbiguousCreateError
			if err == nil || errors.As(err, &ambiguous) != tc.ambiguous {
				t.Fatalf("wrong classification: %v", err)
			}
			if calls.Load() != 1 || strings.Contains(err.Error(), "fixture-provision-secret") {
				t.Fatal("create retried or secret exposed")
			}
		})
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkErrorIsAmbiguousAndSecretSafe(t *testing.T) {
	p, _ := NewOpenSearch("fixture-user", "fixture-secret")
	calls := 0
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("fixture-secret") })
	_, err := p.Create(context.Background(), []byte(definition), func(Cluster) error { return nil })
	var ambiguous *AmbiguousCreateError
	if !errors.As(err, &ambiguous) || calls != 1 || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("unsafe result: %v", err)
	}
}

func TestRecordingFailurePreservesID(t *testing.T) {
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"id":%q}`, testID)
	})
	c, err := p.Create(context.Background(), []byte(definition), func(Cluster) error { return errors.New("fixture-secret could not be written") })
	var rec *RecordingError
	if !errors.As(err, &rec) || c.ID != testID || rec.ClusterID != testID || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("lost resource recovery information: %v", err)
	}
}

func TestWaitReadyChecksOperationAndNodes(t *testing.T) {
	var calls atomic.Int32
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fmt.Fprintf(w, `{"id":%q,"status":"RUNNING","currentClusterOperationStatus":"OPERATION_IN_PROGRESS"}`, testID)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
		case 3:
			fmt.Fprintf(w, `{"id":%q,"status":"RUNNING","currentClusterOperationStatus":"NO_OPERATION","dataCentres":[{"status":"RUNNING","nodes":[{"status":"PROVISIONING"}]}]}`, testID)
		default:
			fmt.Fprintf(w, `{"id":%q,"status":"RUNNING","currentClusterOperationStatus":"NO_OPERATION","dataCentres":[{"status":"RUNNING","nodes":[{"status":"RUNNING"}]}]}`, testID)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	updates := 0
	c, err := p.WaitReady(ctx, testID, func(Cluster) error { updates++; return nil })
	if err != nil || !c.Ready() || calls.Load() != 4 || updates != 3 {
		t.Fatalf("wrong readiness evidence: %v, requests %d, updates %d", err, calls.Load(), updates)
	}
}

func TestWaitReadyCancellationAndFailure(t *testing.T) {
	for _, status := range []string{"PROVISIONING", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			p := localProvider(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"id":%q,"status":%q}`, testID, status) })
			p.PollInterval = time.Hour
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, err := p.WaitReady(ctx, testID, nil)
			if err == nil {
				t.Fatal("did not stop")
			}
			if status == "PROVISIONING" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("context error not preserved: %v", err)
			}
		})
	}
}

func TestDeleteWaitsForAbsenceAndIsRepeatable(t *testing.T) {
	var reads, deletes atomic.Int32
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if deletes.Add(1) == 1 {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		if reads.Add(1) == 1 {
			fmt.Fprintf(w, `{"id":%q,"status":"DELETING"}`, testID)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	if err := p.Delete(context.Background(), testID); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 0 {
		t.Fatal("Delete claimed observed deletion")
	}
	if err := p.WaitDeleted(context.Background(), testID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), testID); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatal("absence not observed")
	}
}

func TestFindByNameRejectsSubstringMatches(t *testing.T) {
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case clusterListPath:
			if r.URL.Query().Get("search") != "name:my-rag-dev-1234 app:OPENSEARCH" {
				t.Error("incorrect search")
			}
			fmt.Fprintf(w, `[{"accountId":"account","clusters":[{"id":%q,"application":"OPENSEARCH"},{"id":%q,"application":"OPENSEARCH"},{"id":"ignored","application":"KAFKA"}]}]`, testID, otherID)
		case clusterPath + testID:
			fmt.Fprintf(w, `{"id":%q,"name":"my-rag-dev-1234"}`, testID)
		case clusterPath + otherID:
			fmt.Fprintf(w, `{"id":%q,"name":"my-rag-dev-1234-copy"}`, otherID)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	cs, err := p.FindByName(context.Background(), "my-rag-dev-1234")
	if err != nil || len(cs) != 1 || cs[0].ID != testID {
		t.Fatalf("wrong reconciliation: %v %v", cs, err)
	}
}

func TestConnectionUsesObservedDNSAndNeverInventsPassword(t *testing.T) {
	var c Cluster
	if err := json.Unmarshal([]byte(`{"loadBalancerConnectionUrl":"https://cluster.example.com:9201","publicEndpoint":"other.example.com"}`), &c); err != nil {
		t.Fatal(err)
	}
	conn, err := c.Connection()
	if err != nil || conn.Host != "cluster.example.com" || conn.Port != 9201 || !conn.SSL {
		t.Fatalf("wrong connection: %+v %v", conn, err)
	}
	u, p := c.Credentials()
	if u != "" || p != "" {
		t.Fatal("invented credentials")
	}
	c.PrivateNetwork = true
	c.PrivateEndpoint = "private.example.com"
	conn, err = c.Connection()
	if err != nil || conn.Host != "private.example.com" {
		t.Fatal("private address not selected")
	}
	for _, v := range []string{"http://insecure.example.com", "https://user:password@example.com", "https://example.com/path", "https://example.com:999999"} {
		if _, err := parseConnection(v); err == nil {
			t.Fatalf("accepted unsafe address %q", v)
		}
	}
	if _, err := (Cluster{}).Connection(); err == nil {
		t.Fatal("invented host")
	}
	c = Cluster{DataCentres: []DataCentre{{Nodes: []Node{
		{Roles: []string{"OPENSEARCH_DASHBOARDS"}, PublicEndpoint: "dashboards.example.com"},
		{Roles: []string{"OPENSEARCH_MASTER"}, PublicEndpoint: "manager.example.com"},
		{Roles: []string{"OPENSEARCH_DATA"}, PublicEndpoint: "data.example.com"},
	}}}}
	conn, err = c.Connection()
	if err != nil || conn.Host != "data.example.com" {
		t.Fatal("selected a non-data-plane service address")
	}
}

func TestRejectsCrossResourceIDsAndRedirects(t *testing.T) {
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"id":%q}`, otherID) })
	if _, err := p.Get(context.Background(), testID); err == nil {
		t.Fatal("accepted another cluster")
	}
	if _, err := p.Get(context.Background(), "../x"); err == nil {
		t.Fatal("accepted invalid ID")
	}
	var redirects atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1) }))
	defer target.Close()
	p = localProvider(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	if _, err := p.Get(context.Background(), testID); err == nil || redirects.Load() != 0 {
		t.Fatal("followed a credential-bearing redirect")
	}
}

func TestMissingCredentialsAndPlaceholders(t *testing.T) {
	for _, pair := range [][2]string{{"", "x"}, {"x", ""}, {" ", "x"}, {"a:b", "x"}} {
		if _, err := NewOpenSearch(pair[0], pair[1]); err == nil {
			t.Fatal("accepted missing/invalid management credentials")
		}
	}
	for _, name := range []string{"instaclustr-kafka", "instaclustr-cassandra", "instaclustr-clickhouse"} {
		p, err := New(name, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Create(context.Background(), nil, nil); !errors.Is(err, ErrNotImplemented) {
			t.Fatal("placeholder executed")
		}
		if err := p.Delete(context.Background(), "any"); !errors.Is(err, ErrNotImplemented) {
			t.Fatal("placeholder executed")
		}
	}
}

func TestDefinitionValidation(t *testing.T) {
	info, err := ValidateOpenSearchRequest([]byte(definition))
	if err != nil || info.Name != "my-rag-dev-1234" || info.Version != "3.6.0" || len(info.Digest) != 64 {
		t.Fatalf("valid definition rejected: %v", err)
	}
	for _, tc := range []struct{ name, value string }{
		{"duplicate", strings.Replace(definition, `"loadBalancer":true`, `"loadBalancer":true,"loadBalancer":false`, 1)},
		{"nested-duplicate", strings.Replace(definition, `"nodeCount":3`, `"nodeCount":3,"nodeCount":6`, 1)},
		{"trailing", definition + `{}`},
		{"wrong-top-level", `[]`},
		{"null", `null`},
		{"readonly", strings.Replace(definition, `"loadBalancer":true`, `"defaultUserPassword":"fixture-secret"`, 1)},
		{"bool-type", strings.Replace(definition, `"loadBalancer":true`, `"loadBalancer":"yes"`, 1)},
		{"name", strings.Replace(definition, `"my-rag-dev-1234"`, `"../escape"`, 1)},
		{"network", strings.Replace(definition, `"10.0.0.0/16"`, `"0.0.0.0/0"`, 1)},
		{"version", strings.Replace(definition, `"3.6.0"`, `"latest"`, 1)},
		{"count", strings.Replace(definition, `"nodeCount":3`, `"nodeCount":0`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateOpenSearchRequest([]byte(tc.value)); err == nil {
				t.Fatal("invalid definition accepted")
			} else if strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("validation error exposed a value")
			}
		})
	}
}
