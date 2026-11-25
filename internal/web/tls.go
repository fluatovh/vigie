package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"vigie/internal/store"
)

// Fichiers du certificat dans le store.
const (
	tlsCertFile = "tls.crt"
	tlsKeyFile  = "tls.key"
	tlsFPFile   = "tls.fingerprint"
)

// Fingerprint renvoie l'empreinte SHA-256 d'un certificat au format AA:BB:….
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// LoadOrCreateCert charge le certificat auto-signé de l'appliance ou le génère (ECDSA P-256,
// 10 ans, SAN : nom d'hôte, « vigie », localhost et les IP présentes à la génération).
// L'empreinte est écrite dans tls.fingerprint pour la console (§5).
func LoadOrCreateCert(st *store.Store) (tls.Certificate, string, error) {
	if cert, err := tls.LoadX509KeyPair(st.Path(tlsCertFile), st.Path(tlsKeyFile)); err == nil {
		fp := Fingerprint(cert.Certificate[0])
		_ = st.WriteBytes(tlsFPFile, []byte(fp+"\n"), 0o644)
		return cert, fp, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	host, _ := os.Hostname()
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{"vigie"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              uniq([]string{host, "vigie", "localhost"}),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
				tpl.IPAddresses = append(tpl.IPAddresses, ipn.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := st.WriteBytes(tlsKeyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := st.WriteBytes(tlsCertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	fp := Fingerprint(der)
	_ = st.WriteBytes(tlsFPFile, []byte(fp+"\n"), 0o644)
	cert, err := tls.LoadX509KeyPair(st.Path(tlsCertFile), st.Path(tlsKeyFile))
	return cert, fp, err
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
