package platform

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestWaitResponsiveDoesNotWaitForDashboardOrManagementReadiness(t *testing.T) {
	gets, probes := 0, 0
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gets++
		if r.URL.Path != clusterPath+testID || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprintf(w, `{"id":%q,"status":"PROVISIONING","currentClusterOperationStatus":"OPERATION_IN_PROGRESS","loadBalancerConnectionUrl":"https://cluster.example.com:9200","dataCentres":[{"status":"PROVISIONING","nodes":[{"status":"RUNNING","nodeRoles":["OPENSEARCH_DATA"]},{"status":"PROVISIONING","nodeRoles":["OPENSEARCH_DASHBOARDS"]}]}]}`, testID)
	})
	c, err := p.WaitResponsive(context.Background(), testID, func(ctx context.Context, c Cluster) error {
		probes++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("probe lacks bounded context")
		}
		if c.Ready() {
			t.Error("fixture accidentally satisfies old management readiness")
		}
		return nil
	})
	if err != nil || c.ID != testID || probes != 1 || gets != 1 {
		t.Fatalf("not data-plane driven: %v, gets=%d probes=%d", err, gets, probes)
	}
}

func TestWaitResponsiveRetriesEndpointPropagationAndTransientFailures(t *testing.T) {
	gets, probes := 0, 0
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gets++
		switch gets {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			fmt.Fprintf(w, `{"id":%q,"status":"PROVISIONING"}`, testID)
		case 3:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			fmt.Fprintf(w, `{"id":%q,"status":"PROVISIONING","publicEndpoint":"cluster.example.com"}`, testID)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := p.WaitResponsive(ctx, testID, func(context.Context, Cluster) error {
		probes++
		if probes < 3 {
			return &ReadinessPendingError{Message: "network or health not ready"}
		}
		return nil
	})
	if err != nil || gets != 6 || probes != 3 {
		t.Fatalf("unexpected polling: %v, gets=%d probes=%d", err, gets, probes)
	}
}

func TestWaitResponsiveRejectsPermanentProbeErrors(t *testing.T) {
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":%q,"status":"RUNNING","publicEndpoint":"cluster.example.com"}`, testID)
	})
	sentinel := errors.New("invalid application credentials")
	probes := 0
	_, err := p.WaitResponsive(context.Background(), testID, func(context.Context, Cluster) error { probes++; return sentinel })
	if !errors.Is(err, sentinel) || probes != 1 {
		t.Fatalf("permanent error retried: %v", err)
	}
	if _, err := p.WaitResponsive(context.Background(), testID, nil); err == nil {
		t.Fatal("nil probe accepted")
	}
}

func TestWaitResponsiveCancellationAndRetryAfter(t *testing.T) {
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":%q,"status":"RUNNING","publicEndpoint":"cluster.example.com"}`, testID)
	})
	probes := 0
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := p.WaitResponsive(ctx, testID, func(context.Context, Cluster) error {
		probes++
		return &ReadinessPendingError{Message: "last health response was red", RetryAfter: time.Minute}
	})
	if !errors.Is(err, context.DeadlineExceeded) || probes != 1 {
		t.Fatalf("cancellation/retry-after failure: %v; probes=%d", err, probes)
	}
}

func TestWaitResponsivePropagatesDeadlineToProbe(t *testing.T) {
	p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":%q,"status":"RUNNING","publicEndpoint":"cluster.example.com"}`, testID)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := p.WaitResponsive(ctx, testID, func(ctx context.Context, _ Cluster) error { <-ctx.Done(); return ctx.Err() })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe context lost: %v", err)
	}
}

func TestWaitResponsiveTerminalManagementFailureDoesNotProbe(t *testing.T) {
	for _, status := range []string{"FAILED", "ERROR", "DELETING", "DELETED"} {
		t.Run(status, func(t *testing.T) {
			p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"id":%q,"status":%q,"publicEndpoint":"cluster.example.com"}`, testID, status)
			})
			_, err := p.WaitResponsive(context.Background(), testID, func(context.Context, Cluster) error { t.Error("failed cluster probed"); return nil })
			if err == nil {
				t.Fatal("terminal failure accepted")
			}
		})
	}
}

func TestWaitResponsiveDoesNotRetryBadEndpointOrManagementCredentials(t *testing.T) {
	for _, forbidden := range []bool{false, true} {
		p := localProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if forbidden {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprintf(w, `{"id":%q,"status":"RUNNING","publicEndpoint":"http://plaintext.example"}`, testID)
		})
		_, err := p.WaitResponsive(context.Background(), testID, func(context.Context, Cluster) error { t.Error("invalid endpoint probed"); return nil })
		if err == nil {
			t.Fatal("permanent error accepted")
		}
	}
}
