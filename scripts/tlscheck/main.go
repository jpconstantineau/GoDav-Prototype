// Command tlscheck loads a test PEM pair without listening or changing OS trust.
package main

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: tlscheck certificate.pem private-key.pem")
		os.Exit(2)
	}
	pair, err := tls.LoadX509KeyPair(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "PEM pair could not be loaded; verify file access, encoding and matching keys")
		os.Exit(1)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "certificate could not be parsed")
		os.Exit(1)
	}
	ips := make([]string, len(leaf.IPAddresses))
	for i, ip := range leaf.IPAddresses {
		ips[i] = ip.String()
	}
	bits := 0
	if key, ok := leaf.PublicKey.(*rsa.PublicKey); ok {
		bits = key.N.BitLen()
	}
	hash := sha256.Sum256(leaf.Raw)
	// Public certificate metadata only. Loading does not prove OS trust or DAV.
	metadata := map[string]any{
		"dns_names": leaf.DNSNames, "ip_addresses": ips,
		"key_bits": bits, "signature": leaf.SignatureAlgorithm.String(),
		"is_ca": leaf.IsCA, "extended_key_usage": leaf.ExtKeyUsage,
		"key_usage": leaf.KeyUsage, "subject": leaf.Subject.String(),
		"issuer":     leaf.Issuer.String(),
		"not_before": leaf.NotBefore.Format(time.RFC3339),
		"not_after":  leaf.NotAfter.Format(time.RFC3339),
		"sha256":     strings.ToUpper(hex.EncodeToString(hash[:])),
	}
	if err := json.NewEncoder(os.Stdout).Encode(metadata); err != nil {
		os.Exit(1)
	}
}
