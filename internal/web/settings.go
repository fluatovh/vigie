package web

import (
	"log"

	"vigie/internal/store"
	"vigie/internal/vsphere"
)

// vsphereSettings est ce qui est persisté de la connexion vSphere. Le mot de passe n'est stocké
// que si l'utilisateur l'a demandé (« mémoriser », phase 9), chiffré en AES-256-GCM avec une clé
// propre à l'appliance (/data/vigie/secret.key) : il sert à la reconnexion automatique.
type vsphereSettings struct {
	URL        string `json:"url"`
	Username   string `json:"username"`
	Thumbprint string `json:"thumbprint"`
	Secret     string `json:"secret,omitempty"` // mot de passe chiffré (mémorisé)
}

const vsphereFile = "vsphere.json"

// Settings persiste la configuration de l'appliance dans le store.
type Settings struct {
	store *store.Store
}

// NewSettings ouvre les réglages sur un store.
func NewSettings(s *store.Store) *Settings { return &Settings{store: s} }

// SaveVSphere mémorise URL, utilisateur et empreinte, et le mot de passe chiffré si demandé.
func (s *Settings) SaveVSphere(p vsphere.ConnectParams, remember bool) {
	doc := vsphereSettings{URL: p.URL, Username: p.Username, Thumbprint: p.Thumbprint}
	if remember {
		sealed, err := s.seal(p.Password)
		if err != nil {
			log.Printf("settings: mot de passe non mémorisé : %v", err)
		}
		doc.Secret = sealed
	}
	if err := s.store.Save(vsphereFile, doc); err != nil {
		log.Printf("settings: %v", err)
	}
}

// VSphere renvoie les réglages mémorisés, ou nil.
func (s *Settings) VSphere() *vsphereSettings {
	var doc vsphereSettings
	if err := s.store.Load(vsphereFile, &doc); err != nil {
		return nil
	}
	return &doc
}

// ForgetVSphere efface les réglages mémorisés.
func (s *Settings) ForgetVSphere() {
	if err := s.store.Remove(vsphereFile); err != nil {
		log.Printf("settings: %v", err)
	}
}
