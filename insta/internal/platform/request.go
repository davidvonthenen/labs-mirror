package platform

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"strings"
)

var (
	namePattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{2,47}$`)
	idPattern      = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

func validName(s string) bool { return namePattern.MatchString(s) }
func validID(s string) bool   { return idPattern.MatchString(s) }

// RequestInfo describes the exact bytes validated. The provider forwards those
// bytes unchanged; it does not substitute a different tier or instance size.
type RequestInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"sha256"`
	SLATier string `json:"sla_tier"`
}

// ValidateOpenSearchRequest checks the local contract without pretending to
// know dynamic regional availability or the account's enabled features. Those
// checks remain authoritative at the provider. All errors omit payload values.
func ValidateOpenSearchRequest(raw []byte) (RequestInfo, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return RequestInfo{}, errors.New("OpenSearch definition must be a JSON object no larger than 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueJSON(d, 0); err != nil {
		return RequestInfo{}, errors.New("OpenSearch definition has invalid JSON, duplicate keys, or excessive nesting")
	}
	if _, err := d.Token(); err != io.EOF {
		return RequestInfo{}, errors.New("OpenSearch definition must contain exactly one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return RequestInfo{}, errors.New("OpenSearch definition must be a JSON object")
	}
	for _, field := range []string{"id", "status", "defaultUsername", "defaultUserPassword", "publicEndpoint", "privateEndpoint", "loadBalancerConnectionUrl", "currentClusterOperationStatus"} {
		if _, present := fields[field]; present {
			return RequestInfo{}, fmt.Errorf("OpenSearch definition must not set response-only field %s", field)
		}
	}
	var r struct {
		Name         string `json:"name"`
		Version      string `json:"opensearchVersion"`
		MajorVersion *int   `json:"opensearchMajorVersion"`
		SLATier      string `json:"slaTier"`
		PCI          *bool  `json:"pciComplianceMode"`
		Private      *bool  `json:"privateNetworkCluster"`
		Managers     []struct {
			Size      string `json:"nodeSize"`
			Dedicated *bool  `json:"dedicatedManager"`
		} `json:"clusterManagerNodes"`
		DataCentres []struct {
			Provider string `json:"cloudProvider"`
			Name     string `json:"name"`
			Network  string `json:"network"`
			Region   string `json:"region"`
			Racks    int    `json:"numberOfRacks"`
		} `json:"dataCentres"`
		DataNodes []struct {
			Size  string `json:"nodeSize"`
			Count int    `json:"nodeCount"`
		} `json:"dataNodes"`
		IngestNodes []struct {
			Size  string `json:"nodeSize"`
			Count int    `json:"nodeCount"`
		} `json:"ingestNodes"`
		Dashboards []struct {
			Size string `json:"nodeSize"`
		} `json:"opensearchDashboards"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return RequestInfo{}, errors.New("OpenSearch definition contains a field with an invalid type")
	}
	if !validName(r.Name) {
		return RequestInfo{}, errors.New("OpenSearch name must be 3–48 letters, digits, underscores, or hyphens and start with a letter or digit")
	}
	if r.Version == "" && r.MajorVersion == nil {
		return RequestInfo{}, errors.New("OpenSearch definition requires opensearchVersion or opensearchMajorVersion")
	}
	if r.Version != "" && !versionPattern.MatchString(r.Version) {
		return RequestInfo{}, errors.New("OpenSearch opensearchVersion must use major.minor.patch format")
	}
	if r.MajorVersion != nil && (*r.MajorVersion < 1 || *r.MajorVersion > 3) {
		return RequestInfo{}, errors.New("OpenSearch opensearchMajorVersion must be 1, 2, or 3")
	}
	if r.SLATier != "PRODUCTION" && r.SLATier != "NON_PRODUCTION" {
		return RequestInfo{}, errors.New("OpenSearch slaTier must be PRODUCTION or NON_PRODUCTION")
	}
	if r.PCI == nil || r.Private == nil {
		return RequestInfo{}, errors.New("OpenSearch definition requires boolean pciComplianceMode and privateNetworkCluster")
	}
	if len(r.Managers) != 1 || r.Managers[0].Size == "" || r.Managers[0].Dedicated == nil {
		return RequestInfo{}, errors.New("OpenSearch clusterManagerNodes requires one entry with nodeSize and dedicatedManager")
	}
	if len(r.DataCentres) != 1 {
		return RequestInfo{}, errors.New("OpenSearch dataCentres requires exactly one entry")
	}
	dc := r.DataCentres[0]
	if dc.Provider == "" || dc.Name == "" || dc.Region == "" || dc.Racks < 2 || dc.Racks > 5 {
		return RequestInfo{}, errors.New("OpenSearch data centre requires cloudProvider, name, region, and numberOfRacks between 2 and 5")
	}
	prefix, err := netip.ParsePrefix(dc.Network)
	if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || prefix.Bits() < 16 || prefix.Bits() > 26 {
		return RequestInfo{}, errors.New("OpenSearch data centre network must be a private IPv4 CIDR with a /16 to /26 prefix")
	}
	if len(r.DataNodes) > 1 || len(r.IngestNodes) > 1 || len(r.Dashboards) > 1 {
		return RequestInfo{}, errors.New("OpenSearch dataNodes, ingestNodes, and opensearchDashboards each allow at most one definition")
	}
	for _, n := range r.DataNodes {
		if n.Size == "" || n.Count < 1 {
			return RequestInfo{}, errors.New("OpenSearch dataNodes requires nodeSize and a positive nodeCount")
		}
	}
	for _, n := range r.IngestNodes {
		if n.Size == "" || n.Count < 1 {
			return RequestInfo{}, errors.New("OpenSearch ingestNodes requires nodeSize and a positive nodeCount")
		}
	}
	for _, n := range r.Dashboards {
		if n.Size == "" {
			return RequestInfo{}, errors.New("OpenSearch opensearchDashboards requires nodeSize")
		}
	}
	for field, value := range fields {
		if strings.HasSuffix(field, "Plugin") || field == "loadBalancer" {
			var enabled bool
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &enabled) != nil {
				return RequestInfo{}, fmt.Errorf("OpenSearch %s must be a boolean", field)
			}
		}
	}
	h := sha256.Sum256(raw)
	return RequestInfo{Name: r.Name, Version: r.Version, Digest: hex.EncodeToString(h[:]), SLATier: r.SLATier}, nil
}

// Reject duplicate object keys, including nested objects, before decoding into
// Go structs. A reviewed field must not be shadowed by a later JSON occurrence.
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("nesting")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := t.(string)
			if !ok || seen[key] {
				return errors.New("duplicate or invalid key")
			}
			seen[key] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid object")
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid array")
		}
	default:
		return errors.New("unexpected delimiter")
	}
	return nil
}
