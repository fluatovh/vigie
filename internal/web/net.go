package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"vigie/internal/jobs"
	"vigie/internal/netdiag"
)

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "corps JSON invalide : "+err.Error())
		return false
	}
	return true
}

func (s *Server) netLocal(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, netdiag.LocalInfo())
}

type pingRequest struct {
	Target     string `json:"target"`
	Count      int    `json:"count"`
	IntervalMs int    `json:"interval_ms"`
}

func (s *Server) netPing(w http.ResponseWriter, r *http.Request) {
	var req pingRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Count <= 0 {
		req.Count = 10
	}
	if req.Count > 100 {
		writeError(w, http.StatusBadRequest, "100 pings au maximum")
		return
	}
	if req.IntervalMs <= 0 {
		req.IntervalMs = 200
	}
	if req.IntervalMs < 100 {
		writeError(w, http.StatusBadRequest, "intervalle minimal 100 ms")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.Count)*time.Duration(req.IntervalMs)*time.Millisecond+10*time.Second)
	defer cancel()
	ip, err := netdiag.Resolve(ctx, req.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rtts, err := netdiag.Ping(ctx, ip, netdiag.PingOptions{Count: req.Count, Interval: time.Duration(req.IntervalMs) * time.Millisecond})
	if err != nil && len(rtts) == 0 {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target": req.Target, "ip": ip.String(), "on_link": netdiag.OnLink(ip, netdiag.LocalInfo()),
		"stats": netdiag.Compute(rtts), "rtts_ms": netdiag.Clean(rtts), "interval_ms": req.IntervalMs,
	})
}

func (s *Server) netTraceroute(w http.ResponseWriter, r *http.Request) {
	var req pingRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	ip, err := netdiag.Resolve(ctx, req.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hops, err := netdiag.Traceroute(ctx, ip, 30, 3, 1500*time.Millisecond)
	if err != nil && len(hops) == 0 {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	for i := range hops {
		hops[i].RTTs = netdiag.Clean(hops[i].RTTs)
	}
	reached := len(hops) > 0 && hops[len(hops)-1].Reached
	writeJSON(w, http.StatusOK, map[string]any{"target": req.Target, "ip": ip.String(), "hops": hops, "reached": reached})
}

func (s *Server) netARP(w http.ResponseWriter, r *http.Request) {
	e, err := netdiag.ARP()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": e})
}

func (s *Server) netARPDelete(w http.ResponseWriter, r *http.Request) {
	if err := netdiag.DeleteARP(r.PathValue("ip")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.netARP(w, r)
}

func (s *Server) netARPFlush(w http.ResponseWriter, r *http.Request) {
	if err := netdiag.DeleteARP(""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.netARP(w, r)
}

func (s *Server) netTargets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"targets": s.netMon.List(), "max": netdiag.MaxTargets})
}

func (s *Server) netTargetAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host  string `json:"host"`
		Label string `json:"label"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	t, err := s.netMon.Add(ctx, req.Host, req.Label)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) netTargetDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.netMon.Remove(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.netTargets(w, r)
}

var ranges = map[string]time.Duration{"3h": 3 * time.Hour, "12h": 12 * time.Hour, "48h": 48 * time.Hour, "7d": 7 * 24 * time.Hour, "14d": 14 * 24 * time.Hour, "1y": 365 * 24 * time.Hour}

func (s *Server) netTargetSeries(w http.ResponseWriter, r *http.Request) {
	rg := r.URL.Query().Get("range")
	d, ok := ranges[rg]
	if !ok {
		d, rg = 3*time.Hour, "3h"
	}
	now := time.Now()
	t, step, pts, err := s.netMon.Series(r.PathValue("id"), now.Add(-d), now)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": t, "range": rg, "step_s": step, "points": pts})
}

// --- iperf3 ---

func (s *Server) iperfServerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.iperf.Status())
}

// iperfServerStart : un pair (ou l'utilisateur) ouvre le serveur iperf3 de cette appliance pour une
// durée limitée. Refusé pendant un job local : le trafic fausserait la mesure en cours.
func (s *Server) iperfServerStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DurationS int `json:"duration_s"`
	}
	if !decode(w, r, &req) {
		return
	}
	if cur := s.jobs.Current(); cur != nil && cur.Profile != "net.iperf" {
		writeError(w, http.StatusConflict, fmt.Sprintf("un job %s est en cours sur cette appliance", cur.Profile))
		return
	}
	st, err := s.iperf.Start(time.Duration(req.DurationS)*time.Second, principal(r).Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) iperfServerStop(w http.ResponseWriter, r *http.Request) {
	s.iperf.Stop()
	writeJSON(w, http.StatusOK, s.iperf.Status())
}

// --- pairs ---

func (s *Server) peersList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"peers": s.peers.List()})
}

func (s *Server) peerFingerprint(w http.ResponseWriter, r *http.Request) {
	fp, u, err := netdiag.PeerFingerprint(r.Context(), r.URL.Query().Get("url"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": u, "sha256": fp})
}

func (s *Server) peerAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name, URL, Token, Fingerprint string
	}
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	p, err := s.peers.Add(ctx, req.Name, req.URL, req.Token, req.Fingerprint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) peerDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.peers.Remove(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.peersList(w, r)
}

// iperfJob : test de débit vers un pair (qui ouvre son serveur pour l'occasion) ou vers un serveur
// iperf3 quelconque (host).
func (s *Server) iperfJob(req *jobRequest) (jobs.Func, error) {
	o := netdiag.IperfOptions{Host: req.Host, Protocol: req.IperfProtocol, Duration: req.IperfDuration, Streams: req.IperfStreams, Reverse: req.IperfReverse, Bitrate: req.IperfBitrate}
	var peer *netdiag.Peer
	if req.Peer != "" {
		p, ok := s.peers.Get(req.Peer)
		if !ok {
			return nil, errors.New("pair inconnu")
		}
		peer = &p
		o.Host = p.Host()
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
		target := o.Host
		if peer != nil {
			target = peer.Name + " (" + o.Host + ")"
			rep.Progress(2, "ouverture du serveur iperf3 du pair "+peer.Name)
			var st netdiag.ServerStatus
			if err := peer.Call(ctx, http.MethodPost, "/api/net/iperf/server", map[string]int{"duration_s": o.Duration + 30}, &st); err != nil {
				return nil, fmt.Errorf("le pair n'a pas ouvert son serveur iperf3 : %w", err)
			}
			defer func() {
				c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = peer.Call(c, http.MethodDelete, "/api/net/iperf/server", nil, nil)
			}()
			time.Sleep(500 * time.Millisecond)
		}
		sens := "appliance → " + target
		if o.Reverse {
			sens = target + " → appliance"
		}
		rep.Progress(5, fmt.Sprintf("iperf3 %s, %s, %d flux, %d s", o.Protocol, sens, o.Streams, o.Duration))
		res, err := netdiag.RunIperf(ctx, o, func(iv netdiag.Interval) {
			rep.Progress(5+90*iv.End/float64(o.Duration), fmt.Sprintf("%.0f s : %.0f Mbit/s", iv.End, iv.Mbps))
			rep.Data("iperf", iv)
		})
		if res != nil {
			rep.Logf("débit émis %.0f Mbit/s, reçu %.0f Mbit/s, retransmissions %d, gigue %.3f ms, perte %.2f %%", res.SentMbps, res.RecvMbps, res.Retransmits, res.JitterMs, res.LostPct)
		}
		return res, err
	}, nil
}

// sse prépare une réponse Server-Sent Events et renvoie une fonction d'envoi.
func sse(w http.ResponseWriter) (func(event string, v any), bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "flux non supporté")
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	return func(event string, v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		fl.Flush()
		mu.Unlock()
	}, true
}

// netPingStream : ping en temps réel (SSE). Chaque réponse est envoyée dès son arrivée ; fermer la
// connexion arrête le ping. count 0 = continu (jusqu'à MaxPingCount échos).
//
//	GET /api/net/ping/stream?target=&count=&interval_ms=
//	événements : start {ip, on_link, count, interval_ms}, reply {seq, rtt_ms (-1 = perdu)}, done {stats}, error {error}
func (s *Server) netPingStream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	count, _ := strconv.Atoi(q.Get("count"))
	if count == 0 {
		count = netdiag.MaxPingCount
	}
	interval, _ := strconv.Atoi(q.Get("interval_ms"))
	if interval == 0 {
		interval = 1000
	}
	if count < 1 || count > netdiag.MaxPingCount || interval < 100 || interval > 60000 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("count 1..%d (0 = continu), interval_ms 100..60000", netdiag.MaxPingCount))
		return
	}
	ip, err := netdiag.Resolve(r.Context(), q.Get("target"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	send, ok := sse(w)
	if !ok {
		return
	}
	send("start", map[string]any{"target": q.Get("target"), "ip": ip.String(), "on_link": netdiag.OnLink(ip, netdiag.LocalInfo()), "count": count, "interval_ms": interval})
	rtts, err := netdiag.Ping(r.Context(), ip, netdiag.PingOptions{Count: count, Interval: time.Duration(interval) * time.Millisecond,
		OnReply: func(seq int, rtt float64) {
			v := rtt
			if math.IsNaN(v) {
				v = -1
			}
			send("reply", map[string]any{"seq": seq + 1, "rtt_ms": v})
		}})
	if err != nil && r.Context().Err() == nil && len(rtts) == 0 {
		send("error", map[string]string{"error": err.Error()})
		return
	}
	send("done", map[string]any{"stats": netdiag.Compute(rtts), "stopped": r.Context().Err() != nil})
}

// netTracerouteStream : traceroute en temps réel (SSE), un événement par saut.
//
//	GET /api/net/traceroute/stream?target=   événements : start, hop {ttl, addr, name, rtts_ms, reached}, done, error
func (s *Server) netTracerouteStream(w http.ResponseWriter, r *http.Request) {
	ip, err := netdiag.Resolve(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	send, ok := sse(w)
	if !ok {
		return
	}
	send("start", map[string]any{"target": r.URL.Query().Get("target"), "ip": ip.String()})
	hops, err := netdiag.TracerouteLive(r.Context(), ip, 30, 3, 1500*time.Millisecond, func(h netdiag.Hop) {
		h.RTTs = netdiag.Clean(h.RTTs)
		send("hop", h)
	})
	if err != nil && r.Context().Err() == nil && len(hops) == 0 {
		send("error", map[string]string{"error": err.Error()})
		return
	}
	send("done", map[string]any{"reached": len(hops) > 0 && hops[len(hops)-1].Reached, "hops": len(hops)})
}
