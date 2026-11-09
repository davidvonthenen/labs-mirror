package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/instaclustr/labs/insta/internal/config"
	"github.com/instaclustr/labs/insta/internal/manifest"
	"github.com/instaclustr/labs/insta/internal/platform"
	"github.com/instaclustr/labs/insta/internal/runner"
	"github.com/instaclustr/labs/insta/internal/state"
)

func (s *session) managementClient() (*platform.OpenSearch, error) {
	// These credentials are shell-only even when a test supplies the transport.
	user, key := os.Getenv("INSTACLUSTR_PROVISION_USERNAME"), os.Getenv("INSTACLUSTR_PROVISION_API_KEY")
	p, err := platform.NewOpenSearch(user, key)
	if err != nil {
		return nil, failure("configuration", 2, err)
	}
	if s.App.PlatformClient != nil {
		return s.App.PlatformClient, nil
	}
	return p, nil
}

func (s *session) Up(ctx context.Context) error {
	count := 0
	for _, name := range manifest.Keys(s.Manifest.Platforms) {
		if s.Manifest.Platforms[name].Provider != "instaclustr-opensearch" {
			return fmt.Errorf("%s: %w", name, platform.ErrNotImplemented)
		}
		count++
	}
	if count > 1 {
		return errors.New("this release supports one OpenSearch platform per stack")
	}
	if count > 0 && s.Values["opensearch.mode"] != "attach" {
		if _, err := s.managementClient(); err != nil {
			return err
		}
	}
	if err := s.ApproveManifest(); err != nil {
		return err
	}
	fmt.Fprintln(s.App.Err, "Plan: prepare the managed Python environment and declared platform/support services. Teaching operations run only through insta task run.")
	// Validate the complete plan before installing packages or creating resources.
	for _, step := range s.Manifest.Lifecycle.Up {
		if step.Platform != "" && step.Action != "ensure" {
			return errors.New("up platform steps must use action: ensure")
		}
		if step.Service != "" && step.Action != "start" {
			return errors.New("up service steps must use action: start")
		}
		if step.Platform != "" && s.Values["opensearch.mode"] != "attach" {
			if _, _, err := s.deploymentRequest(step.Platform); err != nil {
				return err
			}
		}
	}
	if err := s.EnsureRuntime(ctx); err != nil {
		return err
	}
	for _, step := range s.Manifest.Lifecycle.Up {
		if step.Platform != "" {
			if err := s.ensurePlatform(ctx, step.Platform); err != nil {
				return err
			}
		}
		if step.Service != "" {
			if err := s.StartService(ctx, step.Service, "stack"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *session) EnsureRuntime(ctx context.Context) error {
	stack := s.Project.Stack()
	digest, err := runner.RuntimeDigest(s.Root, s.Manifest)
	if err != nil {
		return err
	}
	if stack.RuntimeStatus == "ready" && stack.RuntimeDigest == digest {
		if info, err := os.Stat(runner.Python(s.Root, s.Project.SelectedStack)); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return nil
		}
	}
	stack.RuntimeStatus = "preparing"
	if err := s.Save(); err != nil {
		return err
	}
	fmt.Fprintln(s.App.Err, "Preparing Python dependencies and declared model prerequisites...")
	env := config.TaskEnvironment(s.Manifest, s.Values, s.Root, runner.Venv(s.Root, s.Project.SelectedStack))
	if err := runner.Prepare(ctx, s.Root, s.Project.SelectedStack, s.Manifest, env, s.App.Err, s.Redactor); err != nil {
		stack.RuntimeStatus = "failed"
		return errors.Join(err, s.Save())
	}
	stack.RuntimeStatus, stack.RuntimeDigest = "ready", digest
	return s.Save()
}

func (s *session) deploymentRequest(name string) ([]byte, platform.RequestInfo, error) {
	def, ok := s.Manifest.Platforms[name]
	if !ok {
		return nil, platform.RequestInfo{}, errors.New("unknown platform")
	}
	path, err := manifest.Path(s.Root, def.Request, false)
	if err != nil {
		return nil, platform.RequestInfo{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, platform.RequestInfo{}, err
	}
	if _, err := platform.ValidateOpenSearchRequest(raw); err != nil {
		return nil, platform.RequestInfo{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, platform.RequestInfo{}, err
	}
	clusterName := generatedClusterName(filepath.Base(s.Root), s.Project.SelectedStack, s.Project.ID)
	if record := s.Project.Stack().Platforms[name]; record != nil && record.Name != "" {
		clusterName = record.Name
	}
	fields["name"], _ = json.Marshal(clusterName)
	raw, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, platform.RequestInfo{}, err
	}
	info, err := platform.ValidateOpenSearchRequest(raw)
	return raw, info, err
}

func generatedClusterName(project, stack, id string) string {
	var b strings.Builder
	for _, c := range project + "-" + stack {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	prefix := strings.Trim(b.String(), "-_")
	if prefix == "" {
		prefix = "insta"
	}
	if len(prefix) > 35 {
		prefix = prefix[:35]
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return prefix + "-" + id
}

func (s *session) ensurePlatform(ctx context.Context, name string) error {
	stack := s.Project.Stack()
	def, ok := s.Manifest.Platforms[name]
	if !ok {
		return errors.New("unknown platform")
	}
	if def.Provider != "instaclustr-opensearch" {
		return fmt.Errorf("%s: %w", def.Provider, platform.ErrNotImplemented)
	}
	record := stack.Platforms[name]
	firewallApproved := false
	if s.Values["opensearch.mode"] == "attach" {
		if record != nil && record.Owned {
			return errors.New("this stack already owns an OpenSearch cluster; restore provision mode or run insta down before attaching another cluster")
		}
		if err := s.checkDataPlane(ctx); err != nil {
			return err
		}
		stack.Platforms[name] = &state.Platform{Provider: def.Provider, Owned: false, Status: "attached"}
		return s.Save()
	}
	if record != nil && !record.Owned {
		return errors.New("this stack is attached to an existing cluster; keep attach mode or use a new project to provision a different cluster")
	}
	p, err := s.managementClient()
	if err != nil {
		return err
	}
	raw, info, err := s.deploymentRequest(name)
	if err != nil {
		return err
	}
	if record != nil && record.RequestDigest != "" && record.RequestDigest != info.Digest {
		return errors.New("the deployment definition changed after the recorded create; restore that definition or intentionally clean up with insta down before provisioning again")
	}
	if record != nil && record.ID == "" && record.Status != "create-rejected" {
		if err := s.reconcilePending(ctx, name, p, info); err != nil {
			return err
		}
		record = stack.Platforms[name]
	}
	if record == nil || record.Status == "create-rejected" {
		plan := fmt.Sprintf("Provision billable OpenSearch cluster %s (%s, version %s) using %s. This creates the following nodes and enabled components, then ensures OPENSEARCH and OPENSEARCH_DASHBOARDS firewall rules for 0.0.0.0/0 (all IPv4 addresses). WARNING: both services will allow network access from the public Internet; TLS and authentication remain required.\n%s", info.Name, info.SLATier, info.Version, def.Request, s.Redactor.Text(string(raw)))
		if err := s.App.Confirm(plan); err != nil {
			return err
		}
		firewallApproved = true
		record = &state.Platform{Provider: def.Provider, Name: info.Name, Owned: true, Status: "create-pending", RequestDigest: info.Digest, StartedAt: time.Now().UTC()}
		stack.Platforms[name] = record
		if err := s.Save(); err != nil {
			return err
		}
		_, err := p.Create(ctx, raw, func(c platform.Cluster) error {
			record.ID, record.Status, record.Error = c.ID, "provisioning", ""
			// The ID must survive even if saving the returned password fails.
			if err := s.Save(); err != nil {
				return err
			}
			return s.storePlatformCredentials(c)
		})
		if err != nil {
			var ambiguous *platform.AmbiguousCreateError
			if errors.As(err, &ambiguous) {
				record.Status = "create-uncertain"
			} else if record.ID == "" {
				record.Status = "create-rejected"
			}
			record.Error = s.Redactor.Text(err.Error())
			return errors.Join(err, s.Save())
		}
	}
	if record.Status == "deleting" || record.Status == "deleted" {
		return errors.New("this cluster is pending cleanup; run insta down to finish before insta up")
	}
	// Firewall changes are accepted only after provisioning completes. This
	// gate also applies when resuming an interrupted up with a recorded ID.
	record.Status, record.Error = "awaiting-running", ""
	if err := s.Save(); err != nil {
		return err
	}
	fmt.Fprintf(s.App.Err, "Waiting for OpenSearch cluster %s to finish provisioning and reach RUNNING before configuring firewall rules (up to 45 minutes)...\n", record.ID)
	lastStatus := ""
	_, err = p.WaitReady(ctx, record.ID, func(c platform.Cluster) error {
		status := c.Status
		if c.OperationStatus != "" {
			status += " (operation: " + c.OperationStatus + ")"
		}
		if status != lastStatus {
			fmt.Fprintf(s.App.Err, "OpenSearch provisioning status: %s\n", s.Redactor.Text(status))
			lastStatus = status
		}
		return nil
	})
	if err != nil {
		record.Error = s.Redactor.Text(err.Error())
		return errors.Join(fmt.Errorf("OpenSearch %s remains recorded while waiting for RUNNING; rerun insta up to resume, or insta down for cleanup: %w", record.ID, err), s.Save())
	}
	record.Status, record.Error = "awaiting-firewall", ""
	if err := s.Save(); err != nil {
		return err
	}
	if err := s.ensureFirewallRules(ctx, p, record.ID, firewallApproved); err != nil {
		record.Status, record.Error = "awaiting-firewall", s.Redactor.Text(err.Error())
		return errors.Join(fmt.Errorf("OpenSearch %s remains recorded; rerun insta up to reconcile firewall rules, or insta down for cleanup: %w", record.ID, err), s.Save())
	}
	record.Status, record.Error = "awaiting-connection", ""
	if err := s.Save(); err != nil {
		return err
	}
	fmt.Fprintf(s.App.Err, "Waiting for OpenSearch cluster %s to accept authenticated requests after firewall configuration (up to 45 minutes; Dashboards HTTP readiness is not checked)...\n", record.ID)
	_, err = p.WaitResponsive(ctx, record.ID, func(probeCtx context.Context, c platform.Cluster) error {
		if err := s.publishConnection(c); err != nil {
			return err
		}
		user, password, _ := s.connectionKeys()
		if err := config.Require(s.Values, "OPENSEARCH_HOST", user, password); err != nil {
			if c.Status != "RUNNING" {
				return &platform.ReadinessPendingError{Message: "waiting for OpenSearch application credentials"}
			}
			return err
		}
		return s.checkDataPlane(probeCtx)
	})
	if err != nil {
		record.Error = s.Redactor.Text(err.Error())
		return errors.Join(fmt.Errorf("OpenSearch %s remains recorded; resolve the failure and run insta up, or use insta down for cleanup: %w", record.ID, err), s.Save())
	}
	record.Status, record.Error = "ready", ""
	return s.Save()
}

// ensureFirewallRules changes only this stack's provisioned cluster. It reads
// provider state on every up so partial failures do not create duplicate rules.
func (s *session) ensureFirewallRules(ctx context.Context, p *platform.OpenSearch, clusterID string, approved bool) error {
	rules, err := p.ListFirewallRules(ctx, clusterID)
	if err != nil {
		return err
	}
	const network = "0.0.0.0/0"
	var missing []string
	for _, kind := range []string{"OPENSEARCH", "OPENSEARCH_DASHBOARDS"} {
		exists := false
		for _, rule := range rules {
			if rule.Type != kind || rule.Network != network {
				continue
			}
			switch rule.Status {
			case "FAILED", "ERROR", "DELETING", "DELETED":
				return fmt.Errorf("%s firewall rule is %s; resolve it in the Instaclustr Console and rerun insta up", kind, rule.Status)
			}
			exists = true
			break
		}
		if !exists {
			missing = append(missing, kind)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !approved {
		plan := fmt.Sprintf("Add missing firewall rules to owned OpenSearch cluster %s: %s, network %s (all IPv4 addresses). WARNING: this permits network access from the public Internet; TLS and authentication remain required. No cluster will be created.", clusterID, strings.Join(missing, ", "), network)
		if err := s.App.Confirm(plan); err != nil {
			return err
		}
	}
	for _, kind := range missing {
		fmt.Fprintf(s.App.Err, "Ensuring %s firewall rule for %s on cluster %s...\n", kind, network, clusterID)
		if err := p.CreateFirewallRule(ctx, clusterID, kind, network); err != nil {
			return err
		}
	}
	return nil
}

// Keep server-directed polling delays bounded, as for management requests.
func readinessRetryAfter(value string) time.Duration {
	var delay time.Duration
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		if seconds > 300 {
			seconds = 300
		}
		delay = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil {
		delay = time.Until(when)
	}
	if delay < 0 {
		return 0
	}
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func (s *session) reconcilePending(ctx context.Context, name string, p *platform.OpenSearch, info platform.RequestInfo) error {
	record := s.Project.Stack().Platforms[name]
	if record.RequestDigest != info.Digest || record.Name != info.Name {
		return errors.New("pending-create recovery does not match the recorded request; restore its deployment definition")
	}
	matches, err := p.FindByName(ctx, record.Name)
	if err != nil {
		return err
	}
	if len(matches) != 1 {
		return errors.New("the previous create is still uncertain: an exact unique cluster could not be confirmed; inspect the Instaclustr Console and rerun insta up to reconcile, without deleting the pending record or repeating create")
	}
	c := matches[0]
	if info.Version != "" && c.Version != info.Version {
		return errors.New("the recovery candidate has a different OpenSearch version; ownership cannot be confirmed")
	}
	if err := s.App.Confirm(fmt.Sprintf("Recover the prior pending request %s as owned cluster %s. The exact generated name, local request digest, and OpenSearch version match; future insta down will be allowed to delete this cluster.", record.Name, c.ID)); err != nil {
		return err
	}
	record.ID, record.Status, record.Error = c.ID, "provisioning", ""
	if err := s.Save(); err != nil {
		return err
	}
	return s.storePlatformCredentials(c)
}

func (s *session) connectionKeys() (user, password, ssl string) {
	user, password, ssl = "OPENSEARCH_USER", "OPENSEARCH_PASSWORD", "OPENSEARCH_SSL"
	if _, ok := s.Manifest.Config["OPENSEARCH_USERNAME"]; ok {
		user = "OPENSEARCH_USERNAME"
	}
	if _, ok := s.Manifest.Config["OPENSEARCH_PASS"]; ok {
		password = "OPENSEARCH_PASS"
	}
	if _, ok := s.Manifest.Config["OPENSEARCH_USE_SSL"]; ok {
		ssl = "OPENSEARCH_USE_SSL"
	}
	return
}

func (s *session) storePlatformCredentials(c platform.Cluster) error {
	userKey, passwordKey, _ := s.connectionKeys()
	user, password := c.Credentials()
	if s.Secrets == nil {
		s.Secrets = map[string]string{}
	}
	if user != "" {
		s.Secrets["provider."+userKey] = user
	}
	if password != "" {
		s.Secrets["provider."+passwordKey] = password
	}
	if err := state.SaveSecrets(s.Project.ID, s.Project.SelectedStack, s.Secrets); err != nil {
		return err
	}
	return s.Refresh()
}

func (s *session) publishConnection(c platform.Cluster) error {
	connection, err := c.Connection()
	if err != nil {
		return err
	}
	_, _, sslKey := s.connectionKeys()
	outputs := map[string]string{"OPENSEARCH_HOST": connection.Host, "OPENSEARCH_PORT": strconv.Itoa(connection.Port), sslKey: strconv.FormatBool(connection.SSL)}
	for key, observed := range outputs {
		view := s.View[key]
		if (view.Source == "shell" || view.Source == "stack" || view.Source == "secret store") && s.Values[key] != "" && s.Values[key] != observed {
			return fmt.Errorf("%s conflicts with the provisioned cluster's connection settings; remove that shell or stack override before retrying insta up", key)
		}
	}
	for k, v := range outputs {
		s.Project.Stack().Outputs[k] = v
	}
	if err := s.Save(); err != nil {
		return err
	}
	return s.storePlatformCredentials(c)
}

// DataRequest is shared by health and index-evidence checks. It uses application
// credentials, standard TLS verification, a fixed endpoint, and no redirects.
func (s *session) DataRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	userKey, passwordKey, sslKey := s.connectionKeys()
	for _, key := range []string{"OPENSEARCH_HOST", "OPENSEARCH_PORT", sslKey} {
		if observed := s.Project.Stack().Outputs[key]; observed != "" && s.Values[key] != observed {
			return nil, fmt.Errorf("%s conflicts with the recorded platform endpoint; remove the override or use a separate project", key)
		}
	}
	if err := config.Require(s.Values, "OPENSEARCH_HOST", userKey, passwordKey); err != nil {
		return nil, err
	}
	host := s.Values["OPENSEARCH_HOST"]
	if strings.ContainsAny(host, "/@?# \r\n") || strings.Contains(host, "://") {
		return nil, errors.New("invalid OPENSEARCH_HOST")
	}
	if strings.ContainsAny(host, ":[]") && net.ParseIP(strings.Trim(host, "[]")) == nil {
		return nil, errors.New("OPENSEARCH_HOST must not include a port; use OPENSEARCH_PORT")
	}
	host = strings.Trim(host, "[]")
	port := s.Values["OPENSEARCH_PORT"]
	if port == "" {
		port = "9200"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, errors.New("OPENSEARCH_PORT must be between 1 and 65535")
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n") {
		return nil, errors.New("invalid data-plane request path")
	}
	scheme := "https"
	if s.Values[sslKey] == "false" {
		scheme = "http"
	}
	base := scheme + "://" + net.JoinHostPort(host, port)
	relative, err := url.ParseRequestURI(path)
	if err != nil || relative.IsAbs() || relative.Host != "" {
		return nil, errors.New("invalid data-plane request path")
	}
	req, err := http.NewRequestWithContext(ctx, method, base+relative.String(), body)
	if err != nil {
		return nil, errors.New("cannot construct OpenSearch data-plane request")
	}
	req.SetBasicAuth(s.Values[userKey], s.Values[passwordKey])
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Timeout: 10 * time.Second}
	if s.App.DataClient != nil {
		client = *s.App.DataClient
		if client.Timeout <= 0 || client.Timeout > 10*time.Second {
			client.Timeout = 10 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var certificateError *tls.CertificateVerificationError
		if errors.As(err, &certificateError) {
			return nil, errors.New("OpenSearch TLS certificate verification failed; check the endpoint and TLS trust, then rerun insta up")
		}
		return nil, &platform.ReadinessPendingError{Message: "OpenSearch data-plane connection failed; check the endpoint, DNS, and Instaclustr firewall rules"}
	}
	return response, nil
}

func (s *session) checkDataPlane(ctx context.Context) error {
	resp, err := s.DataRequest(ctx, http.MethodGet, "/_cluster/health?wait_for_status=yellow&timeout=5s", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message := fmt.Sprintf("OpenSearch authentication/health check returned HTTP %d; confirm application credentials and network access, then rerun insta up", resp.StatusCode)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
			return &platform.ReadinessPendingError{Message: message, RetryAfter: readinessRetryAfter(resp.Header.Get("Retry-After"))}
		}
		return errors.New(message)
	}
	var health struct {
		Status   string `json:"status"`
		TimedOut bool   `json:"timed_out"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&health) != nil {
		return errors.New("OpenSearch health response is not valid JSON")
	}
	if health.TimedOut || (health.Status != "green" && health.Status != "yellow") {
		return &platform.ReadinessPendingError{Message: "OpenSearch data-plane health is not green or yellow; the recorded platform has not passed readiness"}
	}
	return nil
}

func (s *session) CheckPlatforms(ctx context.Context, names []string) error {
	for _, name := range names {
		record := s.Project.Stack().Platforms[name]
		if record == nil {
			return fmt.Errorf("platform %s is not prepared; run insta up", name)
		}
		if record.Provider != "instaclustr-opensearch" {
			return fmt.Errorf("%s: %w", name, platform.ErrNotImplemented)
		}
		if record.Status != "ready" && record.Status != "attached" {
			return fmt.Errorf("platform %s is not ready (%s); run insta up to reconcile it", name, record.Status)
		}
		if err := s.checkDataPlane(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) StartService(ctx context.Context, name, owner string) error {
	def, ok := s.Manifest.Services[name]
	if !ok {
		return fmt.Errorf("unknown service %s", name)
	}
	if err := config.Require(s.Values, manifest.Required(def.Environment, "start", s.Values)...); err != nil {
		return err
	}
	if err := s.CheckPlatforms(ctx, def.Requires.Platforms); err != nil {
		return err
	}
	if err := s.EnsureRuntime(ctx); err != nil {
		return err
	}
	for _, dependency := range def.Requires.Services {
		if err := s.StartService(ctx, dependency, owner); err != nil {
			return err
		}
	}
	stack := s.Project.Stack()
	if record := stack.Services[name]; record != nil {
		observed, err := runner.Control(ctx, record, "status")
		if err == nil && observed.Running {
			if err := runner.CheckProbe(ctx, def.Probe); err != nil {
				return fmt.Errorf("owned service %s is running but failed readiness: %w", name, err)
			}
			if owner == "stack" {
				record.Owner = "stack"
			} else if record.Owner != "stack" && record.Owner != owner {
				record.Owner = "shared"
			}
			return s.Save()
		}
		if err != nil && (!serviceAbsent(err) || !runner.Complete(record)) {
			return fmt.Errorf("cannot confirm ownership of service %s; preserving its record: %w", name, err)
		}
		if err == nil {
			if err := runner.StopService(ctx, record); err != nil {
				return err
			}
		}
		// Only a matching completion receipt authorizes stale socket cleanup.
		// A missing socket without that receipt may indicate an orphaned worker.
		if !runner.Complete(record) {
			return fmt.Errorf("service %s cleanup is unconfirmed; preserving its ownership record", name)
		}
		if filepath.Dir(record.Socket) != filepath.Join(s.Root, ".insta", "run") {
			return errors.New("service socket is outside this project's control directory")
		}
		if info, statErr := os.Lstat(record.Socket); statErr == nil {
			if info.Mode()&os.ModeSocket == 0 {
				return errors.New("service control path is occupied by an unexpected file; refusing to remove it")
			}
			if removeErr := os.Remove(record.Socket); removeErr != nil {
				return removeErr
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		delete(stack.Services, name)
		if err := s.Save(); err != nil {
			return err
		}
	}
	token, err := state.Token()
	if err != nil {
		return err
	}
	record := &state.Service{Token: token, Owner: owner, Socket: filepath.Join(s.Root, ".insta", "run", s.Project.SelectedStack+"-"+name+".sock"), Log: filepath.Join(s.Root, ".insta", "logs", s.Project.SelectedStack+"-"+name+".log"), StartedAt: time.Now().UTC()}
	stack.Services[name] = record
	if err := s.Save(); err != nil {
		return err
	}
	env := config.TaskEnvironment(s.Manifest, s.Values, s.Root, runner.Venv(s.Root, s.Project.SelectedStack))
	fmt.Fprintf(s.App.Err, "Starting support service %s...\n", name)
	if err := runner.LaunchService(ctx, s.App.Executable, s.Root, s.Project.SelectedStack, name, def, env, record); err != nil {
		return errors.Join(err, s.Save())
	}
	return s.Save()
}

func serviceAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

func (s *session) StopService(ctx context.Context, name string) error {
	stack := s.Project.Stack()
	record := stack.Services[name]
	if record == nil {
		return nil
	}
	for other, otherRecord := range stack.Services {
		if other == name {
			continue
		}
		for _, dependency := range s.Manifest.Services[other].Requires.Services {
			if dependency != name {
				continue
			}
			observed, err := runner.Control(ctx, otherRecord, "status")
			if err == nil && observed.Running || err != nil && (!serviceAbsent(err) || !runner.Complete(otherRecord)) {
				return fmt.Errorf("service %s is still needed by owned service %s; stop its dependent service first", name, other)
			}
		}
	}
	if err := runner.StopService(ctx, record); err != nil {
		return fmt.Errorf("stop %s: %w", name, err)
	}
	delete(stack.Services, name)
	return s.Save()
}

func (s *session) Down(ctx context.Context) error {
	stack := s.Project.Stack()
	owned := []string{}
	for _, name := range manifest.Keys(stack.Platforms) {
		if stack.Platforms[name].Owned {
			owned = append(owned, name)
		}
	}
	var provider *platform.OpenSearch
	var err error
	if len(owned) > 0 {
		provider, err = s.managementClient()
		if err != nil {
			return errors.Join(fmt.Errorf("remote ownership records retained; restore provisioning credentials and rerun insta down: %w", err), s.stopOwnedServices(ctx))
		}
		for _, name := range owned {
			record := stack.Platforms[name]
			if record.Provider != "instaclustr-opensearch" {
				return fmt.Errorf("%s: %w", record.Provider, platform.ErrNotImplemented)
			}
			if record.ID == "" && record.Status != "create-rejected" {
				_, info, err := s.deploymentRequest(name)
				if err != nil {
					return err
				}
				if err := s.reconcilePending(ctx, name, provider, info); err != nil {
					return err
				}
			}
		}
		var lines []string
		for _, name := range owned {
			record := stack.Platforms[name]
			if record.ID != "" {
				lines = append(lines, fmt.Sprintf("  %s: %s (%s)", name, record.ID, record.Name))
			}
		}
		if len(lines) > 0 {
			if err := s.App.Confirm("Permanently delete these owned OpenSearch clusters and their data after stopping dependent services:\n" + strings.Join(lines, "\n")); err != nil {
				return err
			}
		}
	}
	if err := s.stopOwnedServices(ctx); err != nil {
		return err
	}
	var failures []error
	for _, name := range owned {
		record := stack.Platforms[name]
		if record.ID == "" && record.Status == "create-rejected" {
			delete(stack.Platforms, name)
			if err := s.Save(); err != nil {
				return err
			}
			continue
		}
		record.Status, record.Error = "deleting", ""
		if err := s.Save(); err != nil {
			return err
		}
		if err := provider.Delete(ctx, record.ID); err != nil {
			record.Error = s.Redactor.Text(err.Error())
			failures = append(failures, errors.Join(err, s.Save()))
			continue
		}
		if err := provider.WaitDeleted(ctx, record.ID); err != nil {
			record.Error = s.Redactor.Text(err.Error())
			failures = append(failures, errors.Join(err, s.Save()))
			continue
		}
		delete(stack.Platforms, name)
		user, password, ssl := s.connectionKeys()
		for _, key := range []string{"OPENSEARCH_HOST", "OPENSEARCH_PORT", user, password, ssl} {
			delete(stack.Outputs, key)
			delete(s.Secrets, "provider."+key)
		}
		if err := state.SaveSecrets(s.Project.ID, s.Project.SelectedStack, s.Secrets); err != nil {
			failures = append(failures, err)
		}
		if err := s.Save(); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	stack.ActiveTask = ""
	if err := s.Save(); err != nil {
		return err
	}
	return s.Refresh()
}

func (s *session) stopOwnedServices(ctx context.Context) error {
	// Reverse dependency order, with newest records first for removed definitions.
	order := s.serviceOrder()
	var failures []error
	for i := len(order) - 1; i >= 0; i-- {
		if err := s.StopService(ctx, order[i]); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	s.Project.Stack().ActiveTask = ""
	return s.Save()
}

func (s *session) serviceOrder() []string {
	stack := s.Project.Stack()
	names := manifest.Keys(stack.Services)
	sort.SliceStable(names, func(i, j int) bool {
		return stack.Services[names[i]].StartedAt.Before(stack.Services[names[j]].StartedAt)
	})
	seen := map[string]bool{}
	order := []string{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] || stack.Services[name] == nil {
			return
		}
		seen[name] = true
		for _, dep := range s.Manifest.Services[name].Requires.Services {
			visit(dep)
		}
		order = append(order, name)
	}
	for _, name := range names {
		visit(name)
	}
	return order
}
