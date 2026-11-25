package vsphere

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/vmware/govmomi/vim25/soap"
)

// CertInfo décrit le certificat présenté par un serveur vSphere.
type CertInfo struct {
	Host       string    `json:"host"`
	SHA256     string    `json:"sha256"`
	SHA1       string    `json:"sha1"`
	Subject    string    `json:"subject"`
	Issuer     string    `json:"issuer"`
	NotAfter   time.Time `json:"not_after"`
	SelfSigned bool      `json:"self_signed"`
	// Trusted : la chaîne est validée par les autorités du système (nom d'hôte compris). Dans ce cas
	// l'empreinte est une confirmation supplémentaire ; sinon elle est le seul ancrage de confiance.
	Trusted bool `json:"trusted"`
}

// Thumbprint récupère le certificat du serveur sans le vérifier (c'est l'utilisateur qui confirme
// l'empreinte : confiance au premier usage, §5.1 du plan). Aucune authentification n'est envoyée.
func Thumbprint(ctx context.Context, rawURL string) (*CertInfo, error) {
	u, err := soap.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("URL invalide : %w", err)
	}
	addr := hostPort(u)
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // lecture de l'empreinte uniquement
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := d.DialContext(dctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connexion TLS à %s : %w", addr, err)
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("aucun certificat présenté par %s", addr)
	}
	leaf := certs[0]
	trusted := false
	if host, _, err := net.SplitHostPort(addr); err == nil {
		pool := x509.NewCertPool()
		for _, c := range certs[1:] {
			pool.AddCert(c)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: pool}); err == nil {
			trusted = true
		}
	}
	return &CertInfo{
		Host:       u.Host,
		SHA256:     soap.ThumbprintSHA256(leaf),
		SHA1:       soap.ThumbprintSHA1(leaf),
		Subject:    leaf.Subject.String(),
		Issuer:     leaf.Issuer.String(),
		NotAfter:   leaf.NotAfter,
		SelfSigned: leaf.Subject.String() == leaf.Issuer.String(),
		Trusted:    trusted,
	}, nil
}

func hostPort(u *url.URL) string {
	if _, _, err := net.SplitHostPort(u.Host); err == nil {
		return u.Host
	}
	return net.JoinHostPort(u.Host, "443")
}
