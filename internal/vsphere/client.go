// Package vsphere encapsule l'accès à l'API vSphere (govmomi) : connexion, identification de
// l'appliance, inventaire, chemin réseau et privilèges. Lecture seule dans cette phase.
package vsphere

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// ErrNotConnected est renvoyée quand aucune session vSphere n'est ouverte.
var ErrNotConnected = errors.New("pas de connexion vSphere")

// ConnectParams sont les paramètres de connexion saisis par l'utilisateur.
type ConnectParams struct {
	URL        string `json:"url"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Thumbprint string `json:"thumbprint"` // SHA-256 (ou SHA-1) confirmée par l'utilisateur
}

// ServerInfo décrit le serveur connecté.
type ServerInfo struct {
	Name       string `json:"name"`
	FullName   string `json:"full_name"`
	Version    string `json:"version"`
	Build      string `json:"build"`
	APIType    string `json:"api_type"` // VirtualCenter ou HostAgent
	IsVCenter  bool   `json:"is_vcenter"`
	InstanceID string `json:"instance_uuid"`
}

// Status est l'état de la connexion exposé à l'API.
type Status struct {
	Connected   bool        `json:"connected"`
	URL         string      `json:"url,omitempty"`
	Username    string      `json:"username,omitempty"`
	Thumbprint  string      `json:"thumbprint,omitempty"`
	Server      *ServerInfo `json:"server,omitempty"`
	ConnectedAt time.Time   `json:"connected_at,omitempty"`
}

// Session est une connexion vSphere ouverte.
type Session struct {
	Client     *vim25.Client
	URL        *url.URL
	Username   string
	Thumbprint string
	Server     ServerInfo
	SessionKey string
	Since      time.Time
	mgr        *session.Manager
}

// Manager détient la session courante (une seule à la fois).
type Manager struct {
	mu   sync.RWMutex
	cur  *Session
	Logf func(string, ...any)
}

// NewManager crée un gestionnaire sans session.
func NewManager() *Manager { return &Manager{Logf: log.Printf} }

// Connect ouvre une session. Le certificat est vérifié par la chaîne de confiance du système, sinon
// par l'empreinte fournie ; jamais en mode « insecure » silencieux.
func (m *Manager) Connect(ctx context.Context, p ConnectParams) (*Session, error) {
	u, err := soap.ParseURL(p.URL)
	if err != nil {
		return nil, fmt.Errorf("URL invalide : %w", err)
	}
	if p.Thumbprint == "" {
		return nil, errors.New("empreinte du certificat manquante : la confirmer avant de se connecter")
	}
	u.User = nil
	// L'empreinte confirmée par l'utilisateur doit être celle du certificat réellement présenté, même
	// si ce certificat est par ailleurs reconnu par une autorité (govmomi ne la consulterait pas).
	cert, err := Thumbprint(ctx, p.URL)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(p.Thumbprint, cert.SHA256) && !strings.EqualFold(p.Thumbprint, cert.SHA1) {
		return nil, fmt.Errorf("l'empreinte confirmée ne correspond pas au certificat présenté par %s (SHA-256 %s)", u.Host, cert.SHA256)
	}
	sc := soap.NewClient(u, false)
	sc.SetThumbprint(u.Host, p.Thumbprint)
	sc.Timeout = 60 * time.Second
	vc, err := vim25.NewClient(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("connexion à %s : %w", u.Host, err)
	}
	vc.RoundTripper = session.KeepAlive(vc.RoundTripper, 5*time.Minute)
	mgr := session.NewManager(vc)
	if err := mgr.Login(ctx, url.UserPassword(p.Username, p.Password)); err != nil {
		return nil, fmt.Errorf("authentification refusée par %s : %w", u.Host, err)
	}
	us, err := mgr.UserSession(ctx)
	if err != nil {
		return nil, err
	}
	about := vc.ServiceContent.About
	s := &Session{
		Client:     vc,
		URL:        u,
		Username:   us.UserName,
		Thumbprint: p.Thumbprint,
		Server: ServerInfo{
			Name: about.Name, FullName: about.FullName, Version: about.Version, Build: about.Build,
			APIType: about.ApiType, IsVCenter: about.ApiType == "VirtualCenter", InstanceID: about.InstanceUuid,
		},
		SessionKey: us.Key,
		Since:      time.Now(),
		mgr:        mgr,
	}
	m.mu.Lock()
	old := m.cur
	m.cur = s
	m.mu.Unlock()
	if old != nil {
		old.close(context.Background())
	}
	m.Logf("vsphere: connecté à %s (%s %s build %s) en tant que %s", u.Host, about.Name, about.Version, about.Build, us.UserName)
	return s, nil
}

func (s *Session) close(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = s.mgr.Logout(ctx)
}

// Disconnect ferme la session courante s'il y en a une.
func (m *Manager) Disconnect(ctx context.Context) {
	m.mu.Lock()
	s := m.cur
	m.cur = nil
	m.mu.Unlock()
	if s != nil {
		s.close(ctx)
		m.Logf("vsphere: déconnecté de %s", s.URL.Host)
	}
}

// Session renvoie la session courante ou ErrNotConnected.
func (m *Manager) Session() (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cur == nil {
		return nil, ErrNotConnected
	}
	return m.cur, nil
}

// Alive vérifie que le serveur reconnaît encore la session courante. Une session que le serveur ne
// reconnaît plus (vCenter redémarré, session expirée ou fermée par un administrateur) est oubliée :
// Status redevient « non connecté » et la reconnexion automatique peut prendre le relais. Une erreur
// réseau ne permet pas de conclure : la session est gardée.
func (m *Manager) Alive(ctx context.Context) bool {
	s, err := m.Session()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	us, err := s.mgr.UserSession(ctx) // nil, nil : NotAuthenticated
	if err != nil || us != nil {
		return true
	}
	m.mu.Lock()
	if m.cur == s {
		m.cur = nil
	}
	m.mu.Unlock()
	m.Logf("vsphere: session expirée sur %s (non reconnue par le serveur)", s.URL.Host)
	return false
}

// Status décrit l'état courant sans secret.
func (m *Manager) Status() Status {
	s, err := m.Session()
	if err != nil {
		return Status{}
	}
	return Status{Connected: true, URL: s.URL.String(), Username: s.Username, Thumbprint: s.Thumbprint, Server: &s.Server, ConnectedAt: s.Since}
}

// Ref construit une référence d'objet géré à partir de « type:valeur » (ex. HostSystem:host-12).
func Ref(s string) (types.ManagedObjectReference, bool) {
	var r types.ManagedObjectReference
	if !r.FromString(s) {
		return r, false
	}
	return r, true
}
