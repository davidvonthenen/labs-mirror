package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
)

const firewallPath = "/cluster-management/v2/resources/network-firewall-rules/v2"

// FirewallRule is observed management metadata, not evidence of reachability.
type FirewallRule struct {
	ID        string `json:"id"`
	ClusterID string `json:"clusterId"`
	Network   string `json:"network"`
	Type      string `json:"type"`
	Status    string `json:"status"`
}

// ListFirewallRules reads the documented array of per-cluster rule envelopes.
// The caller uses these observations rather than a local "already created" flag.
func (p *OpenSearch) ListFirewallRules(ctx context.Context, clusterID string) ([]FirewallRule, error) {
	if !validID(clusterID) {
		return nil, errors.New("invalid OpenSearch cluster ID; expected a UUID")
	}
	path := "/cluster-management/v2/data-sources/cluster/" + clusterID + "/network-firewall-rules/v2"
	body, err := p.request(ctx, http.MethodGet, path, nil, http.StatusOK)
	if err != nil {
		return nil, fmt.Errorf("list OpenSearch firewall rules: %w", err)
	}
	var groups []struct {
		ClusterID string         `json:"clusterId"`
		Rules     []FirewallRule `json:"firewallRules"`
	}
	if json.Unmarshal(body, &groups) != nil {
		return nil, errors.New("invalid OpenSearch firewall list response")
	}
	var rules []FirewallRule
	for _, group := range groups {
		if group.ClusterID != clusterID {
			return nil, errors.New("firewall list cluster ID does not match the requested ID")
		}
		for _, rule := range group.Rules {
			if rule.ClusterID == "" {
				rule.ClusterID = group.ClusterID
			}
			if rule.ClusterID != clusterID {
				return nil, errors.New("firewall rule cluster ID does not match the requested ID")
			}
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// CreateFirewallRule makes one POST. A documented HTTP 409 means the identical
// type/network already exists (for example, a concurrent or interrupted run).
// Other failures are returned; the next up lists remote rules before retrying.
func (p *OpenSearch) CreateFirewallRule(ctx context.Context, clusterID, ruleType, network string) error {
	if !validID(clusterID) {
		return errors.New("invalid OpenSearch cluster ID; expected a UUID")
	}
	if ruleType != "OPENSEARCH" && ruleType != "OPENSEARCH_DASHBOARDS" {
		return errors.New("unsupported OpenSearch firewall rule type")
	}
	if _, _, err := net.ParseCIDR(network); err != nil {
		return errors.New("firewall network must be a CIDR")
	}
	body, err := json.Marshal(struct {
		ClusterID string `json:"clusterId"`
		Network   string `json:"network"`
		Type      string `json:"type"`
	}{clusterID, network, ruleType})
	if err != nil {
		return err
	}
	_, err = p.request(ctx, http.MethodPost, firewallPath, body, http.StatusAccepted)
	var response *HTTPError
	if errors.As(err, &response) && response.StatusCode == http.StatusConflict {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create %s firewall rule for %s: %w", ruleType, clusterID, err)
	}
	return nil
}
