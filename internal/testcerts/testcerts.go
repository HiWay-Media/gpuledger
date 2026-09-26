// Package testcerts makes a throwaway CA and certificates for tests: the unit tests'
// TLS servers and the Nomad agents of the integration matrix use the same ones. Only
// tests import it, so it never reaches the binary.
package testcerts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Set is a CA and a server and a client certificate it signed, written as PEM files.
type Set struct {
	CA, ServerCert, ServerKey, ClientCert, ClientKey string
}

// Write creates the set in dir. The server certificate names 127.0.0.1, localhost and
// the names Nomad checks with verify_server_hostname (server.global.nomad and
// client.global.nomad), so one certificate serves a -dev agent in both roles.
func Write(dir string) (Set, error) {
	s := Set{CA: filepath.Join(dir, "ca.pem"), ServerCert: filepath.Join(dir, "server.pem"), ServerKey: filepath.Join(dir, "server-key.pem"), ClientCert: filepath.Join(dir, "client.pem"), ClientKey: filepath.Join(dir, "client-key.pem")}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gpuledger test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return s, err
	}
	ca, _ := x509.ParseCertificate(caDER)
	if err := writePEM(s.CA, "CERTIFICATE", caDER); err != nil {
		return s, err
	}
	leaf := func(serial int64, cn string, dns []string, ips []net.IP, certPath, keyPath string) error {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), DNSNames: dns, IPAddresses: ips, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		if err != nil {
			return err
		}
		kb, _ := x509.MarshalECPrivateKey(key)
		if err := writePEM(certPath, "CERTIFICATE", der); err != nil {
			return err
		}
		return writePEM(keyPath, "EC PRIVATE KEY", kb)
	}
	if err := leaf(2, "server.global.nomad", []string{"localhost", "server.global.nomad", "client.global.nomad"}, []net.IP{net.ParseIP("127.0.0.1")}, s.ServerCert, s.ServerKey); err != nil {
		return s, err
	}
	return s, leaf(3, "gpuledger", []string{"gpuledger"}, nil, s.ClientCert, s.ClientKey)
}

func writePEM(path, typ string, der []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600)
}
