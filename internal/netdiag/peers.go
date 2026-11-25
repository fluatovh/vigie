package netdiag

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"vigie/internal/store"
)

// Peer est une autre appliance vigie (pour les tests de débit iperf3 à la demande).
type Peer struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`             // https://<ip>
	Token       string    `json:"token,omitempty"` // jeton d'API du pair (jamais renvoyé par l'API)
	Fingerprint string    `json:"fingerprint"`     // SHA-256 du certificat du pair (confiance au premier usage)
	Added       time.Time `json:"added"`
}

// Host renvoie l'adresse IP du pair (pour le client iperf3).
func (p Peer) Host() string {
	u, err := url.Parse(p.URL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Peers gère la liste des pairs (persistée, jetons compris, dans /data en mode 600).
type Peers struct {
	store *store.Store
	mu    sync.Mutex
	list  []Peer
}

const peersFile = "net-peers.json"

// NewPeers charge les pairs.
func NewPeers(s *store.Store) *Peers {
	p := &Peers{store: s}
	if s != nil {
		_ = s.Load(peersFile, &p.list)
	}
	return p
}

// List renvoie les pairs sans leur jeton.
func (p *Peers) List() []Peer {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Peer, len(p.list))
	for i, x := range p.list {
		x.Token = ""
		out[i] = x
	}
	return out
}

// Get renvoie un pair (avec son jeton).
func (p *Peers) Get(id string) (Peer, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, x := range p.list {
		if x.ID == id {
			return x, true
		}
	}
	return Peer{}, false
}

// PeerFingerprint lit l'empreinte SHA-256 du certificat présenté par https://ip.
func PeerFingerprint(ctx context.Context, rawURL string) (string, string, error) {
	u, err := normalizeURL(rawURL)
	if err != nil {
		return "", "", err
	}
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // lecture de l'empreinte, confirmée par l'utilisateur
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := d.DialContext(cctx, "tcp", net.JoinHostPort(u.Hostname(), portOr(u, "443")))
	if err != nil {
		return "", "", fmt.Errorf("connexion à %s : %w", u.Host, err)
	}
	defer c.Close()
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", "", errors.New("aucun certificat")
	}
	return fingerprint(certs[0].Raw), u.String(), nil
}

func portOr(u *url.URL, def string) string {
	if p := u.Port(); p != "" {
		return p
	}
	return def
}

func normalizeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, errors.New("URL du pair attendue : https://<ip>")
	}
	if ip := net.ParseIP(u.Hostname()); ip == nil || ip.To4() == nil {
		return nil, errors.New("le pair doit être désigné par son adresse IPv4")
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return u, nil
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// Add vérifie l'empreinte confirmée et le jeton (appel authentifié au pair), puis enregistre le pair.
func (p *Peers) Add(ctx context.Context, name, rawURL, token, confirmed string) (Peer, error) {
	fp, u, err := PeerFingerprint(ctx, rawURL)
	if err != nil {
		return Peer{}, err
	}
	if !strings.EqualFold(fp, confirmed) {
		return Peer{}, fmt.Errorf("l'empreinte confirmée ne correspond pas au certificat présenté (%s)", fp)
	}
	if !strings.HasPrefix(token, "vg_") {
		return Peer{}, errors.New("jeton d'API du pair attendu (vg_…, créé dans Réglages sur le pair)")
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	peer := Peer{ID: hex.EncodeToString(b), Name: strings.TrimSpace(name), URL: u, Token: token, Fingerprint: fp, Added: time.Now().UTC()}
	if peer.Name == "" {
		peer.Name = peer.Host()
	}
	var about struct {
		Hostname string `json:"hostname"`
		Version  string `json:"version"`
	}
	if err := peer.Call(ctx, http.MethodGet, "/api/about", nil, &about); err != nil {
		return Peer{}, fmt.Errorf("le pair refuse le jeton ou ne répond pas : %w", err)
	}
	if name == "" && about.Hostname != "" {
		peer.Name = about.Hostname
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, x := range p.list {
		if x.URL == peer.URL {
			return Peer{}, errors.New("ce pair est déjà enregistré")
		}
	}
	p.list = append(p.list, peer)
	if p.store != nil {
		if err := p.store.Save(peersFile, p.list); err != nil {
			p.list = p.list[:len(p.list)-1]
			return Peer{}, err
		}
	}
	peer.Token = ""
	return peer, nil
}

// Remove supprime un pair.
func (p *Peers) Remove(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, x := range p.list {
		if x.ID == id {
			p.list = append(p.list[:i], p.list[i+1:]...)
			if p.store != nil {
				return p.store.Save(peersFile, p.list)
			}
			return nil
		}
	}
	return errors.New("pair inconnu")
}

// Call appelle l'API du pair en vérifiant son certificat par l'empreinte enregistrée.
func (peer Peer) Call(ctx context.Context, method, path string, body, out any) error {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, //nolint:gosec // vérification par empreinte ci-dessous
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509Cert) error {
			if len(raw) == 0 || !strings.EqualFold(fingerprint(raw[0]), peer.Fingerprint) {
				return errors.New("empreinte du certificat du pair différente de celle enregistrée")
			}
			return nil
		}}}
	cl := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, peer.URL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+peer.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("HTTP %d : %s", resp.StatusCode, e.Error)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
