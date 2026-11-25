package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/vmware/govmomi/vim25/types"

	"vigie/internal/vsphere"
)

const apiTimeout = 90 * time.Second

func (s *Server) ctx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), apiTimeout)
}

// session renvoie la session vSphere ou écrit une erreur 409.
func (s *Server) session(w http.ResponseWriter) (*vsphere.Session, bool) {
	sess, err := s.vs.Session()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return nil, false
	}
	return sess, true
}

func (s *Server) vsphereThumbprint(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" {
		writeError(w, http.StatusBadRequest, "paramètre url manquant")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	info, err := vsphere.Thumbprint(ctx, u)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type connectRequest struct {
	vsphere.ConnectParams
	Remember bool `json:"remember"`
}

func (s *Server) vsphereConnect(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corps JSON invalide : "+err.Error())
		return
	}
	if req.URL == "" || req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "url, username et password sont requis")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	if _, err := s.vs.Connect(ctx, req.ConnectParams); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if s.settings != nil {
		s.settings.SaveVSphere(req.ConnectParams, req.Remember)
	}
	writeJSON(w, http.StatusOK, s.vs.Status())
}

func (s *Server) vsphereStatus(w http.ResponseWriter, r *http.Request) {
	st := s.vs.Status()
	if !st.Connected && s.settings != nil {
		if saved := s.settings.VSphere(); saved != nil {
			st.URL, st.Username, st.Thumbprint = saved.URL, saved.Username, saved.Thumbprint
		}
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) vsphereDisconnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	s.vs.Disconnect(ctx)
	if s.settings != nil {
		s.settings.ForgetSecret() // déconnexion volontaire : plus de reconnexion automatique
		if r.URL.Query().Get("forget") == "1" {
			s.settings.ForgetVSphere()
		}
	}
	writeJSON(w, http.StatusOK, s.vs.Status())
}

func (s *Server) self(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w)
	if !ok {
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	self, err := s.identify(ctx, sess)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, self)
}

// identify met en cache l'identité (elle ne change qu'avec un vMotion, qui relit runtime.host).
func (s *Server) identify(ctx context.Context, sess *vsphere.Session) (*vsphere.Self, error) {
	s.selfMu.Lock()
	defer s.selfMu.Unlock()
	if s.selfCache != nil && s.selfSess == sess && time.Since(s.selfAt) < 30*time.Second {
		return s.selfCache, nil
	}
	self, err := vsphere.Identify(ctx, sess)
	if err != nil {
		return nil, err
	}
	s.selfCache, s.selfSess, s.selfAt = self, sess, time.Now()
	return self, nil
}

// refParam lit une référence d'objet (« type:valeur ») dans la query ; vide = valeur par défaut.
func refParam(r *http.Request, name string) (types.ManagedObjectReference, bool, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return types.ManagedObjectReference{}, false, nil
	}
	ref, ok := vsphere.Ref(v)
	if !ok {
		return ref, false, errors.New("paramètre " + name + " invalide (attendu type:valeur)")
	}
	return ref, true, nil
}

func (s *Server) datastores(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w)
	if !ok {
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	hostRef, given, err := refParam(r, "host")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !given {
		self, err := s.identify(ctx, sess)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		hostRef, _ = vsphere.Ref(self.Host.Ref)
	}
	list, err := vsphere.Datastores(ctx, sess, hostRef)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"host": hostRef.String(), "datastores": list})
}

func (s *Server) hosts(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w)
	if !ok {
		return
	}
	dsRef, given, err := refParam(r, "datastore")
	if err != nil || !given {
		writeError(w, http.StatusBadRequest, "paramètre datastore requis (type:valeur)")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	list, err := vsphere.Hosts(ctx, sess, dsRef)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"datastore": dsRef.String(), "hosts": list})
}

func (s *Server) path(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w)
	if !ok {
		return
	}
	dsRef, given, err := refParam(r, "datastore")
	if err != nil || !given {
		writeError(w, http.StatusBadRequest, "paramètre datastore requis (type:valeur)")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	hostRef, given, err := refParam(r, "host")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !given {
		self, err := s.identify(ctx, sess)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		hostRef, _ = vsphere.Ref(self.Host.Ref)
	}
	p, err := vsphere.HostPath(ctx, sess, hostRef, dsRef)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) privileges(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w)
	if !ok {
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	self, err := s.identify(ctx, sess)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	ents := map[string]types.ManagedObjectReference{}
	if ref, ok := vsphere.Ref(self.VM.Ref); ok {
		ents["vm"] = ref
	}
	if ref, ok := vsphere.Ref(self.Host.Ref); ok {
		ents["host"] = ref
	}
	if self.Cluster != nil {
		if ref, ok := vsphere.Ref(self.Cluster.Ref); ok {
			ents["cluster"] = ref
		}
	}
	if dsRef, given, err := refParam(r, "datastore"); err == nil && given {
		ents["datastore"] = dsRef
	}
	rep, err := vsphere.Privileges(ctx, sess, ents)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// migrateCheck : prévalidation du vMotion de l'appliance vers chaque hôte qui monte le datastore.
func (s *Server) migrateCheck(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(w)
	if !ok {
		return
	}
	dsRef, given, err := refParam(r, "datastore")
	if err != nil || !given {
		writeError(w, http.StatusBadRequest, "paramètre datastore requis (type:valeur)")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	self, err := s.identify(ctx, sess)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	hosts, err := vsphere.Hosts(ctx, sess, dsRef)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	vmRef, _ := vsphere.Ref(self.VM.Ref)
	out := []vsphere.MigrationCheck{}
	for _, h := range hosts {
		ref, _ := vsphere.Ref(h.Ref)
		out = append(out, vsphere.CheckMigration(ctx, sess, vmRef, ref, &dsRef))
	}
	drs, _ := vsphere.DRSOf(ctx, sess, self)
	writeJSON(w, http.StatusOK, map[string]any{"current": self.Host, "checks": out, "drs": drs})
}
