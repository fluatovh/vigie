package netdiag

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ErrNoIPv4 : seules les cibles IPv4 sont prises en charge pour l'instant.
var ErrNoIPv4 = errors.New("cible IPv4 attendue")

// Resolve valide une cible (adresse IPv4 ou nom) et renvoie son adresse IPv4. Aucune entrée n'est
// jamais passée à un shell.
func Resolve(ctx context.Context, target string) (net.IP, error) {
	if len(target) == 0 || len(target) > 253 {
		return nil, errors.New("cible vide ou trop longue")
	}
	for _, c := range target {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return nil, fmt.Errorf("caractère interdit %q dans la cible", c)
		}
	}
	if ip := net.ParseIP(target); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return nil, ErrNoIPv4
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", target)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("résolution de %s impossible : %v", target, err)
	}
	return ips[0].To4(), nil
}

// conn est un socket ICMP brut partagé : un lecteur unique distribue les réponses par identifiant.
type conn struct {
	pc      *icmp.PacketConn
	p4      *ipv4.PacketConn
	mu      sync.Mutex
	waiters map[uint32]chan reply // clé : id<<16 | seq
	writeMu sync.Mutex
}

type reply struct {
	from net.IP
	at   time.Time
	typ  ipv4.ICMPType
}

var (
	shared     *conn
	sharedErr  error
	sharedOnce sync.Once
)

// ICMP renvoie le socket partagé (ouvert une fois ; nécessite root ou CAP_NET_RAW).
func getConn() (*conn, error) {
	sharedOnce.Do(func() {
		pc, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if err != nil {
			sharedErr = fmt.Errorf("socket ICMP : %w (droits root requis)", err)
			return
		}
		c := &conn{pc: pc, p4: pc.IPv4PacketConn(), waiters: map[uint32]chan reply{}}
		go c.readLoop()
		shared = c
	})
	return shared, sharedErr
}

func (c *conn) readLoop() {
	buf := make([]byte, 1500)
	for {
		n, peer, err := c.pc.ReadFrom(buf)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		at := time.Now()
		msg, err := icmp.ParseMessage(1, buf[:n])
		if err != nil {
			continue
		}
		var id, seq int
		switch b := msg.Body.(type) {
		case *icmp.Echo:
			if msg.Type != ipv4.ICMPTypeEchoReply {
				continue
			}
			id, seq = b.ID, b.Seq
		case *icmp.TimeExceeded:
			id, seq = innerEcho(b.Data)
		case *icmp.DstUnreach:
			id, seq = innerEcho(b.Data)
		default:
			continue
		}
		if id < 0 {
			continue
		}
		key := uint32(id)<<16 | uint32(seq)
		c.mu.Lock()
		ch, ok := c.waiters[key]
		c.mu.Unlock()
		if ok {
			ip := net.ParseIP(peer.String())
			if ia, ok := peer.(*net.IPAddr); ok {
				ip = ia.IP
			}
			select {
			case ch <- reply{from: ip, at: at, typ: msg.Type.(ipv4.ICMPType)}:
			default:
			}
		}
	}
}

// innerEcho extrait id/seq de l'en-tête ICMP d'écho cité dans un message d'erreur (en-tête IP + 8 octets).
func innerEcho(data []byte) (int, int) {
	if len(data) < 20 {
		return -1, -1
	}
	ihl := int(data[0]&0x0f) * 4
	if len(data) < ihl+8 || data[ihl] != byte(ipv4.ICMPTypeEcho) {
		return -1, -1
	}
	return int(binary.BigEndian.Uint16(data[ihl+4:])), int(binary.BigEndian.Uint16(data[ihl+6:]))
}

func newID() int {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return int(binary.BigEndian.Uint16(b)) ^ (os.Getpid() & 0xffff)
}

// probe envoie un écho ICMP (TTL optionnel) et attend la réponse ou une erreur ICMP.
func (c *conn) probe(ctx context.Context, dst net.IP, id, seq, ttl int, timeout time.Duration) (reply, time.Duration, error) {
	key := uint32(id)<<16 | uint32(seq)
	ch := make(chan reply, 1)
	c.mu.Lock()
	c.waiters[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.waiters, key); c.mu.Unlock() }()
	payload := make([]byte, 56)
	binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
	b, err := (&icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: id, Seq: seq, Data: payload}}).Marshal(nil)
	if err != nil {
		return reply{}, 0, err
	}
	c.writeMu.Lock()
	if ttl > 0 {
		_ = c.p4.SetTTL(ttl)
	} else {
		_ = c.p4.SetTTL(64)
	}
	start := time.Now()
	_, err = c.pc.WriteTo(b, &net.IPAddr{IP: dst})
	c.writeMu.Unlock()
	if err != nil {
		return reply{}, 0, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r, r.at.Sub(start), nil
	case <-t.C:
		return reply{}, 0, context.DeadlineExceeded
	case <-ctx.Done():
		return reply{}, 0, ctx.Err()
	}
}

// PingOptions paramètrent un ping.
type PingOptions struct {
	Count    int           // 1..MaxPingCount
	Interval time.Duration // ≥ 100 ms
	Timeout  time.Duration // par sonde
	// OnReply est appelé dès qu'une sonde aboutit (rtt en ms, NaN si perdue), pour l'affichage en
	// temps réel. Les appels peuvent arriver dans le désordre.
	OnReply func(seq int, rttMs float64)
}

// MaxPingCount : 3600 échos (une heure à 1 s, ou « continu » arrêté par l'utilisateur).
const MaxPingCount = 3600

// Ping envoie Count échos et renvoie les RTT en ms (NaN = perdu), dans l'ordre d'émission.
func Ping(ctx context.Context, dst net.IP, o PingOptions) ([]float64, error) {
	if o.Count <= 0 || o.Count > MaxPingCount {
		o.Count = 10
	}
	if o.Interval < 100*time.Millisecond {
		o.Interval = time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Second
	}
	c, err := getConn()
	if err != nil {
		return nil, err
	}
	id := newID()
	rtts := make([]float64, o.Count)
	var wg sync.WaitGroup
	for i := 0; i < o.Count; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				wg.Wait()
				return rtts[:i], ctx.Err()
			case <-time.After(o.Interval):
			}
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, d, err := c.probe(ctx, dst, id, i%65536, 0, o.Timeout)
			if err != nil || r.typ != ipv4.ICMPTypeEchoReply {
				rtts[i] = math.NaN()
			} else {
				rtts[i] = float64(d.Microseconds()) / 1000
			}
			if o.OnReply != nil && ctx.Err() == nil {
				o.OnReply(i, rtts[i])
			}
		}(i)
	}
	wg.Wait()
	return rtts, nil
}

// Hop est un saut de traceroute.
type Hop struct {
	TTL     int       `json:"ttl"`
	Addr    string    `json:"addr,omitempty"`
	Name    string    `json:"name,omitempty"`
	RTTs    []float64 `json:"rtts_ms"` // NaN sérialisé en null côté API
	Reached bool      `json:"reached"`
}

// Traceroute envoie 3 sondes ICMP par TTL jusqu'à la cible (30 sauts au maximum).
func Traceroute(ctx context.Context, dst net.IP, maxHops, probes int, timeout time.Duration) ([]Hop, error) {
	return TracerouteLive(ctx, dst, maxHops, probes, timeout, nil)
}

// TracerouteLive appelle onHop à chaque saut terminé (affichage en temps réel).
func TracerouteLive(ctx context.Context, dst net.IP, maxHops, probes int, timeout time.Duration, onHop func(Hop)) ([]Hop, error) {
	if maxHops <= 0 || maxHops > 64 {
		maxHops = 30
	}
	if probes <= 0 || probes > 5 {
		probes = 3
	}
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	c, err := getConn()
	if err != nil {
		return nil, err
	}
	id := newID()
	var hops []Hop
	for ttl := 1; ttl <= maxHops; ttl++ {
		h := Hop{TTL: ttl}
		for p := 0; p < probes; p++ {
			r, d, err := c.probe(ctx, dst, id, ttl*8+p, ttl, timeout)
			if ctx.Err() != nil {
				return hops, ctx.Err()
			}
			if err != nil {
				h.RTTs = append(h.RTTs, math.NaN())
				continue
			}
			h.RTTs = append(h.RTTs, float64(d.Microseconds())/1000)
			if h.Addr == "" {
				h.Addr = r.from.String()
			}
			if r.typ == ipv4.ICMPTypeEchoReply || r.from.Equal(dst) {
				h.Reached = true
			}
		}
		if h.Addr != "" {
			rctx, cancel := context.WithTimeout(ctx, time.Second)
			if names, err := net.DefaultResolver.LookupAddr(rctx, h.Addr); err == nil && len(names) > 0 {
				h.Name = names[0]
			}
			cancel()
		}
		hops = append(hops, h)
		if onHop != nil {
			onHop(h)
		}
		if h.Reached {
			break
		}
	}
	return hops, nil
}
