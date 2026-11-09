// Package platform implements the Instaclustr management API. The caller owns
// confirmation, durable ownership journals, and the protected credential store.
package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	APIBaseURL       = "https://api.instaclustr.com"
	clusterPath      = "/cluster-management/v2/resources/applications/opensearch/clusters/v2/"
	clusterListPath  = "/cluster-management/v2/data-sources/clusters/v2"
	maxResponseBytes = 4 << 20
)

var ErrNotFound = errors.New("Instaclustr cluster not found")

// ErrConnectionUnavailable means provisioning has not published an endpoint yet.
var ErrConnectionUnavailable = errors.New("OpenSearch API did not return a usable connection address; configure OPENSEARCH_HOST from the Console connection information")

// HTTPError deliberately omits the response body: error bodies may contain
// echoed credentials. StatusCode remains available for recovery decisions.
type HTTPError struct {
	StatusCode int
	Method     string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	hint := "check the Instaclustr Console for details"
	switch e.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		hint = "check the deployment definition, version, region, node sizes, and account features"
	case http.StatusUnauthorized:
		hint = "check INSTACLUSTR_PROVISION_USERNAME and INSTACLUSTR_PROVISION_API_KEY"
	case http.StatusForbidden:
		hint = "the provisioning API key lacks access to this operation"
	case http.StatusTooManyRequests:
		hint = "rate limit reached; retry the read or reconciliation later"
	}
	return fmt.Sprintf("Instaclustr %s returned HTTP %d: %s", e.Method, e.StatusCode, hint)
}

// AmbiguousCreateError means a POST may have reached the provider. Retain the
// pending-create journal and reconcile by its unique name; never repeat the POST.
type AmbiguousCreateError struct{ Cause error }

func (e *AmbiguousCreateError) Error() string {
	return "OpenSearch create outcome is uncertain; retain the pending request and reconcile with Instaclustr before retrying (no automatic create retry)"
}
func (e *AmbiguousCreateError) Unwrap() error { return e.Cause }

// RecordingError means the server confirmed an ID but the durable callback
// failed. The ID is retained even if the caller cannot save its ordinary state.
type RecordingError struct {
	ClusterID string
	Cause     error
}

func (e *RecordingError) Error() string {
	return fmt.Sprintf("OpenSearch cluster %s was created but its ownership record could not be saved; preserve this ID and do not create again", e.ClusterID)
}
func (e *RecordingError) Unwrap() error { return e.Cause }

type requestError struct{ cause error }

func (e *requestError) Error() string {
	if errors.Is(e.cause, context.Canceled) {
		return "Instaclustr request canceled"
	}
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return "Instaclustr request timed out"
	}
	return "Instaclustr request failed; check network connectivity and TLS configuration"
}
func (e *requestError) Unwrap() error { return e.cause }

// OpenSearch uses documented V2 management and firewall endpoints. The caller
// authorizes changes; management credentials never go to task processes.
type OpenSearch struct {
	baseURL  string
	username string
	key      string
	client   *http.Client
	// PollInterval can be shortened by deterministic tests. Production uses 10s.
	PollInterval time.Duration
}

func NewOpenSearch(username, key string) (*OpenSearch, error) {
	if strings.TrimSpace(username) == "" || strings.TrimSpace(key) == "" {
		return nil, errors.New("set INSTACLUSTR_PROVISION_USERNAME and INSTACLUSTR_PROVISION_API_KEY in the current shell before platform operations")
	}
	if strings.ContainsAny(username, ":\r\n") || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("invalid Instaclustr provisioning credentials")
	}
	return &OpenSearch{baseURL: APIBaseURL, username: username, key: key,
		client:       &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		PollInterval: 10 * time.Second}, nil
}

func (*OpenSearch) Name() string       { return "instaclustr-opensearch" }
func (*OpenSearch) String() string     { return "Instaclustr OpenSearch provider" }
func (p *OpenSearch) GoString() string { return p.String() }

// WithHTTPClient supplies a transport for embedding or deterministic fixtures.
// It does not change the API origin or permit redirects carrying credentials.
func (p *OpenSearch) WithHTTPClient(client *http.Client) *OpenSearch {
	copy := *p
	if client == nil {
		return &copy
	}
	httpCopy := *client
	if httpCopy.Timeout <= 0 || httpCopy.Timeout > 30*time.Second {
		httpCopy.Timeout = 30 * time.Second
	}
	httpCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	copy.client = &httpCopy
	return &copy
}

type Node struct {
	ID              string   `json:"id"`
	Status          string   `json:"status"`
	Roles           []string `json:"nodeRoles,omitempty"`
	PublicEndpoint  string   `json:"publicEndpoint,omitempty"`
	PrivateEndpoint string   `json:"privateEndpoint,omitempty"`
	PublicAddress   string   `json:"publicAddress,omitempty"`
	PrivateAddress  string   `json:"privateAddress,omitempty"`
}

type DataCentre struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Nodes  []Node `json:"nodes,omitempty"`
}

// Cluster contains only observed API data. Credentials are private and omitted
// from JSON and formatted output; save Credentials() only to the secret store.
type Cluster struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Status          string       `json:"status"`
	OperationStatus string       `json:"currentClusterOperationStatus,omitempty"`
	Version         string       `json:"opensearchVersion,omitempty"`
	PrivateNetwork  bool         `json:"privateNetworkCluster,omitempty"`
	PublicEndpoint  string       `json:"publicEndpoint,omitempty"`
	PrivateEndpoint string       `json:"privateEndpoint,omitempty"`
	LoadBalancerURL string       `json:"loadBalancerConnectionUrl,omitempty"`
	DataCentres     []DataCentre `json:"dataCentres,omitempty"`
	username        string
	password        string
}

func (c *Cluster) UnmarshalJSON(data []byte) error {
	type plain Cluster
	v := struct {
		*plain
		Username string `json:"defaultUsername"`
		Password string `json:"defaultUserPassword"`
	}{plain: (*plain)(c)}
	if err := json.Unmarshal(data, &v); err != nil {
		return errors.New("invalid OpenSearch cluster response")
	}
	c.username, c.password = v.Username, v.Password
	return nil
}

func (c Cluster) String() string                           { return fmt.Sprintf("OpenSearch cluster %s (%s)", c.ID, c.Status) }
func (c Cluster) GoString() string                         { return c.String() }
func (c Cluster) Credentials() (username, password string) { return c.username, c.password }

func (c Cluster) Ready() bool {
	if c.Status != "RUNNING" || (c.OperationStatus != "" && c.OperationStatus != "NO_OPERATION") {
		return false
	}
	for _, dc := range c.DataCentres {
		if dc.Status != "" && dc.Status != "RUNNING" {
			return false
		}
		for _, node := range dc.Nodes {
			if node.Status != "RUNNING" {
				return false
			}
		}
	}
	return true
}

// Connection is data-plane metadata, independent of management-API readiness.
// DNS names are preferred so callers can retain TLS certificate verification.
type Connection struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	SSL  bool   `json:"ssl"`
}

func (c Cluster) Connection() (Connection, error) {
	if c.PrivateNetwork {
		if c.PrivateEndpoint != "" {
			return parseConnection(c.PrivateEndpoint)
		}
	} else {
		if c.LoadBalancerURL != "" {
			return parseConnection(c.LoadBalancerURL)
		}
		if c.PublicEndpoint != "" {
			return parseConnection(c.PublicEndpoint)
		}
	}
	for _, dc := range c.DataCentres {
		for _, n := range dc.Nodes {
			isOpenSearch := false
			for _, role := range n.Roles {
				switch role {
				case "OPENSEARCH", "OPENSEARCH_COORDINATOR", "OPENSEARCH_DATA", "OPENSEARCH_INGEST", "OPENSEARCH_DATA_AND_INGEST":
					isOpenSearch = true
				}
			}
			if !isOpenSearch {
				continue
			}
			if c.PrivateNetwork {
				if n.PrivateEndpoint != "" {
					return parseConnection(n.PrivateEndpoint)
				}
				if n.PrivateAddress != "" {
					return parseConnection(n.PrivateAddress)
				}
			} else {
				if n.PublicEndpoint != "" {
					return parseConnection(n.PublicEndpoint)
				}
				if n.PublicAddress != "" {
					return parseConnection(n.PublicAddress)
				}
			}
		}
	}
	return Connection{}, ErrConnectionUnavailable
}

func parseConnection(value string) (Connection, error) {
	if net.ParseIP(value) != nil {
		return Connection{Host: value, Port: 9200, SSL: true}, nil
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return Connection{}, errors.New("OpenSearch API returned an unsupported connection address")
	}
	port := 9200
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return Connection{}, errors.New("OpenSearch API returned an invalid port")
		}
	}
	return Connection{Host: u.Hostname(), Port: port, SSL: true}, nil
}

// Create makes exactly one POST. The caller must journal the pending request
// before calling, then use record to persist the confirmed ID before polling.
func (p *OpenSearch) Create(ctx context.Context, raw []byte, record func(Cluster) error) (Cluster, error) {
	if _, err := ValidateOpenSearchRequest(raw); err != nil {
		return Cluster{}, err
	}
	if record == nil {
		return Cluster{}, errors.New("OpenSearch create requires a durable ownership callback")
	}
	body, err := p.request(ctx, http.MethodPost, clusterPath, raw, http.StatusAccepted)
	if err != nil {
		var h *HTTPError
		if !errors.As(err, &h) || h.StatusCode >= 500 || h.StatusCode == http.StatusRequestTimeout || (h.StatusCode >= 200 && h.StatusCode < 300) {
			return Cluster{}, &AmbiguousCreateError{Cause: err}
		}
		return Cluster{}, err
	}
	var c Cluster
	if err = json.Unmarshal(body, &c); err != nil || !validID(c.ID) {
		return Cluster{}, &AmbiguousCreateError{Cause: errors.New("accepted create response did not contain a valid cluster ID")}
	}
	if err = record(c); err != nil {
		return c, &RecordingError{ClusterID: c.ID, Cause: err}
	}
	return c, nil
}

func (p *OpenSearch) Get(ctx context.Context, id string) (Cluster, error) {
	if !validID(id) {
		return Cluster{}, errors.New("invalid OpenSearch cluster ID; expected a UUID")
	}
	body, err := p.request(ctx, http.MethodGet, clusterPath+id, nil, http.StatusOK)
	if err != nil {
		return Cluster{}, err
	}
	var c Cluster
	if err := json.Unmarshal(body, &c); err != nil {
		return Cluster{}, err
	}
	if c.ID != id {
		return Cluster{}, errors.New("OpenSearch response cluster ID does not match the requested ID")
	}
	return c, nil
}

// FindByName uses the documented account-level search followed by exact name
// matching. An empty result does not authorize a repeated billable create.
func (p *OpenSearch) FindByName(ctx context.Context, name string) ([]Cluster, error) {
	if !validName(name) {
		return nil, errors.New("invalid cluster name for reconciliation")
	}
	q := url.Values{"search": {"name:" + name + " app:OPENSEARCH"}}
	body, err := p.request(ctx, http.MethodGet, clusterListPath+"?"+q.Encode(), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var accounts []struct {
		Clusters []struct {
			ID          string `json:"id"`
			Application string `json:"application"`
		} `json:"clusters"`
	}
	if json.Unmarshal(body, &accounts) != nil {
		return nil, errors.New("invalid account cluster list response")
	}
	var found []Cluster
	seen := map[string]bool{}
	count := 0
	for _, account := range accounts {
		for _, item := range account.Clusters {
			if item.Application != "OPENSEARCH" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			count++
			if count > 100 {
				return nil, errors.New("too many clusters matched the recovery name; reconcile in the Instaclustr Console")
			}
			c, err := p.Get(ctx, item.ID)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if c.Name == name {
				found = append(found, c)
			}
		}
	}
	return found, nil
}

// WaitReady honors cancellation and uses read-only retries for transient HTTP
// failures. A successful return does not prove firewall or data-plane access.
func (p *OpenSearch) WaitReady(ctx context.Context, id string, onUpdate func(Cluster) error) (Cluster, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	for {
		c, err := p.Get(ctx, id)
		if err == nil {
			if onUpdate != nil {
				if err = onUpdate(c); err != nil {
					return c, err
				}
			}
			if c.OperationStatus == "OPERATION_FAILED" || c.Status == "FAILED" || c.Status == "ERROR" || c.Status == "DELETED" || c.Status == "DELETING" {
				return c, errors.New("OpenSearch provisioning failed or the cluster is being deleted; inspect the Instaclustr Console and retain the ownership record")
			}
			if c.Ready() {
				return c, nil
			}
		} else if !retryableRead(err) {
			return c, err
		}
		if err := p.pause(ctx, err); err != nil {
			return c, err
		}
	}
}

// Delete requests deletion of an ID. Only the caller's durable owned records
// authorize this method. A 204 is acceptance; WaitDeleted confirms disappearance.
func (p *OpenSearch) Delete(ctx context.Context, id string) error {
	if !validID(id) {
		return errors.New("invalid OpenSearch cluster ID; expected a UUID")
	}
	_, err := p.request(ctx, http.MethodDelete, clusterPath+id, nil, http.StatusNoContent)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (p *OpenSearch) WaitDeleted(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	for {
		_, err := p.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil && !retryableRead(err) {
			return err
		}
		if err := p.pause(ctx, err); err != nil {
			return err
		}
	}
}

func retryableRead(err error) bool {
	var h *HTTPError
	return errors.As(err, &h) && (h.StatusCode == http.StatusTooManyRequests || h.StatusCode >= 500)
}

func (p *OpenSearch) pause(ctx context.Context, cause error) error {
	d := p.PollInterval
	if d <= 0 {
		d = 10 * time.Second
	}
	var h *HTTPError
	if errors.As(cause, &h) && h.RetryAfter > d {
		d = h.RetryAfter
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *OpenSearch) request(ctx context.Context, method, path string, body []byte, want int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("could not construct Instaclustr API request")
	}
	req.SetBasicAuth(p.username, p.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "insta-cli/1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, &requestError{cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && method != http.MethodPost {
		return nil, ErrNotFound
	}
	if resp.StatusCode != want {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Method: method, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &requestError{cause: err}
	}
	if len(b) > maxResponseBytes {
		return nil, errors.New("Instaclustr response exceeds the supported size")
	}
	return b, nil
}

func retryAfter(s string) time.Duration {
	if seconds, err := strconv.Atoi(s); err == nil && seconds > 0 {
		if seconds > 300 {
			seconds = 300
		}
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(s); err == nil {
		d := time.Until(t)
		if d > 300*time.Second {
			d = 300 * time.Second
		}
		if d > 0 {
			return d
		}
	}
	return 0
}
