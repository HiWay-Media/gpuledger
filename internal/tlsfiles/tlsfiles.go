// Package tlsfiles turns certificate paths — a CA file or directory, a client
// certificate and key, a server name — into a tls.Config, for the Nomad agent and for
// Consul alike. Paths only: key material never passes through a flag.
package tlsfiles

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Files are the paths and the server name to verify.
type Files struct {
	CACert, CAPath, ClientCert, ClientKey, ServerName string
}

// FromEnv reads the variables a HashiCorp CLI reads under prefix: NOMAD_CACERT,
// NOMAD_CAPATH, NOMAD_CLIENT_CERT, NOMAD_CLIENT_KEY, NOMAD_TLS_SERVER_NAME — and the
// same with CONSUL_.
func FromEnv(prefix string) Files {
	g := func(k string) string { return os.Getenv(prefix + "_" + k) }
	return Files{CACert: g("CACERT"), CAPath: g("CAPATH"), ClientCert: g("CLIENT_CERT"), ClientKey: g("CLIENT_KEY"), ServerName: g("TLS_SERVER_NAME")}
}

// Config is nil when nothing is set: the system's roots, no client certificate. prefix
// is the flags' ("nomad", "consul"), so an error names the flag to fix.
func (t Files) Config(prefix string) (*tls.Config, error) {
	if t == (Files{}) {
		return nil, nil
	}
	cfg := &tls.Config{ServerName: t.ServerName, MinVersion: tls.VersionTLS12}
	if t.CACert != "" || t.CAPath != "" {
		pool := x509.NewCertPool()
		files := []string{}
		if t.CACert != "" {
			files = append(files, t.CACert)
		}
		if t.CAPath != "" {
			entries, err := os.ReadDir(t.CAPath)
			if err != nil {
				return nil, fmt.Errorf("--%s-ca-path %s: %w", prefix, t.CAPath, err)
			}
			n := 0
			for _, e := range entries {
				if !e.IsDir() && (strings.HasSuffix(e.Name(), ".pem") || strings.HasSuffix(e.Name(), ".crt")) {
					files = append(files, filepath.Join(t.CAPath, e.Name()))
					n++
				}
			}
			if n == 0 {
				return nil, fmt.Errorf("--%s-ca-path %s: no .pem or .crt file", prefix, t.CAPath)
			}
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, fmt.Errorf("--%s-ca-cert %s: %w", prefix, f, err)
			}
			if !pool.AppendCertsFromPEM(b) {
				return nil, fmt.Errorf("--%s-ca-cert %s: no PEM certificate in it", prefix, f)
			}
		}
		cfg.RootCAs = pool
	}
	if (t.ClientCert == "") != (t.ClientKey == "") {
		return nil, fmt.Errorf("--%s-client-cert and --%s-client-key go together", prefix, prefix)
	}
	if t.ClientCert != "" {
		cert, err := tls.LoadX509KeyPair(t.ClientCert, t.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("--%s-client-cert %s / --%s-client-key: %w", prefix, t.ClientCert, prefix, err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
