package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"

	"github.com/confluentinc/kcp/internal/types"
)

// forceAuth rewrites the msk-credentials.yaml at path so that only the given auth method has
// use: true for the cluster identified by clusterArn. Without this, kcp may pick an
// unauthenticated plaintext listener and return no ACLs.
func forceAuth(path, clusterArn, method string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}
	var creds types.Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		// yaml errors quote source lines, which can contain secrets; do not wrap them.
		return errors.New("credentials file is not valid YAML")
	}

	var cluster *types.ClusterAuth
	for i := range creds.Regions {
		for j := range creds.Regions[i].Clusters {
			if creds.Regions[i].Clusters[j].Arn == clusterArn {
				cluster = &creds.Regions[i].Clusters[j]
			}
		}
	}
	if cluster == nil {
		return fmt.Errorf("cluster %q not found in credentials file", clusterArn)
	}

	am := &cluster.AuthMethod
	use := map[string]*bool{}
	if am.IAM != nil {
		use["iam"] = &am.IAM.Use
	}
	if am.SASLScram != nil {
		use["sasl_scram"] = &am.SASLScram.Use
	}
	if am.SASLPlain != nil {
		use["sasl_plain"] = &am.SASLPlain.Use
	}
	if am.TLS != nil {
		use["tls"] = &am.TLS.Use
	}
	if am.UnauthenticatedTLS != nil {
		use["unauthenticated_tls"] = &am.UnauthenticatedTLS.Use
	}
	if am.UnauthenticatedPlaintext != nil {
		use["unauthenticated_plaintext"] = &am.UnauthenticatedPlaintext.Use
	}
	if _, ok := use[method]; !ok {
		return fmt.Errorf("method %q not in credentials file for cluster %q", method, clusterArn)
	}
	for name, u := range use {
		*u = name == method
	}

	if err := creds.WriteToFile(path); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}
	return nil
}

// applySASLSecret sets username/password on the sasl_scram or sasl_plain entry of the cluster
// identified by clusterArn, creating the entry if discover did not emit it.
func applySASLSecret(path, clusterArn, method, username, password string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}
	var creds types.Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		// yaml errors quote source lines, which can contain secrets; do not wrap them.
		return errors.New("credentials file is not valid YAML")
	}
	var cluster *types.ClusterAuth
	for i := range creds.Regions {
		for j := range creds.Regions[i].Clusters {
			if creds.Regions[i].Clusters[j].Arn == clusterArn {
				cluster = &creds.Regions[i].Clusters[j]
			}
		}
	}
	if cluster == nil {
		return fmt.Errorf("cluster %q not found in credentials file", clusterArn)
	}
	am := &cluster.AuthMethod
	switch method {
	case "sasl_scram":
		if am.SASLScram == nil {
			am.SASLScram = &types.SASLScramConfig{}
		}
		am.SASLScram.Username, am.SASLScram.Password = username, password
	case "sasl_plain":
		if am.SASLPlain == nil {
			am.SASLPlain = &types.SASLPlainConfig{}
		}
		am.SASLPlain.Username, am.SASLPlain.Password = username, password
	default:
		return fmt.Errorf("method %q does not take a username and password", method)
	}
	if err := creds.WriteToFile(path); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}
	return nil
}

// redactCredentials blanks every secret field of an msk-credentials.yaml document.
func redactCredentials(data []byte) ([]byte, error) {
	var creds types.Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		// yaml errors quote source lines, which can contain secrets; do not wrap them.
		return nil, errors.New("credentials are not valid YAML")
	}
	for i := range creds.Regions {
		for j := range creds.Regions[i].Clusters {
			am := &creds.Regions[i].Clusters[j].AuthMethod
			if am.SASLScram != nil {
				am.SASLScram.Username, am.SASLScram.Password = "", ""
			}
			if am.SASLPlain != nil {
				am.SASLPlain.Username, am.SASLPlain.Password = "", ""
			}
			if am.TLS != nil {
				am.TLS.ClientKey = ""
			}
		}
	}
	out, err := creds.ToYaml()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// credentialSecrets returns the secret values (passwords and the TLS client key; usernames are not treated as secret) found in a credentials document.
func credentialSecrets(data []byte) []string {
	var creds types.Credentials
	if len(data) == 0 || yaml.Unmarshal(data, &creds) != nil {
		return nil
	}
	var out []string
	for _, rg := range creds.Regions {
		for _, c := range rg.Clusters {
			am := c.AuthMethod
			var vals []string
			if am.SASLScram != nil {
				vals = append(vals, am.SASLScram.Password)
			}
			if am.SASLPlain != nil {
				vals = append(vals, am.SASLPlain.Password)
			}
			if am.TLS != nil {
				vals = append(vals, am.TLS.ClientKey)
			}
			for _, v := range vals {
				if v != "" {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// filterCredentials writes to dst a copy of the credentials at src that keeps the region entry of
// clusterArn but only that cluster.
func filterCredentials(src, dst, clusterArn string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}
	var creds types.Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return errors.New("credentials file is not valid YAML")
	}
	var out types.Credentials
	for _, rg := range creds.Regions {
		for _, c := range rg.Clusters {
			if c.Arn == clusterArn {
				rg.Clusters = []types.ClusterAuth{c}
				out.Regions = append(out.Regions, rg)
				break
			}
		}
	}
	if len(out.Regions) == 0 {
		return fmt.Errorf("cluster %q not found in credentials file", clusterArn)
	}
	if err := out.WriteToFile(dst); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}
	return nil
}
