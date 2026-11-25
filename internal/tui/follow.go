package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"vigie/internal/jobs"
)

// line est une ligne de texte stylée (vues défilantes).
type line struct {
	T     string
	St    Style
	Label string // colonne de gauche (retrait suspendu de labelW)
}

const labelW = 16

// Scroller affiche des lignes avec défilement.
type Scroller struct {
	Lines []line
	Top   int
}

// Add ajoute une ligne.
func (sc *Scroller) Add(t string, st Style) { sc.Lines = append(sc.Lines, line{T: t, St: st}) }

// AddKV ajoute une ligne « libellé : texte » avec retrait suspendu.
func (sc *Scroller) AddKV(label, t string, st Style) {
	sc.Lines = append(sc.Lines, line{T: t, St: st, Label: label})
}

// Key fait défiler.
func (sc *Scroller) Key(k Key, h int) bool {
	switch k.Name {
	case "Up":
		sc.Top--
	case "Down":
		sc.Top++
	case "PgUp":
		sc.Top -= h
	case "PgDn":
		sc.Top += h
	case "Home":
		sc.Top = 0
	default:
		return false
	}
	return true
}

// Draw affiche la fenêtre visible (les lignes longues sont coupées).
func (sc *Scroller) Draw(s *Screen, x, y, w, h int) {
	type out struct {
		line
		indent int
	}
	var all []out
	for _, l := range sc.Lines {
		l.T, l.Label = T(l.T), T(l.Label)
		if l.Label != "" {
			for i, p := range wrap(l.T, w-labelW) {
				lb := ""
				if i == 0 {
					lb = l.Label
				}
				all = append(all, out{line{T: p, St: l.St, Label: lb}, labelW})
			}
			continue
		}
		for _, p := range wrap(l.T, w) {
			all = append(all, out{line{T: p, St: l.St}, 0})
		}
	}
	sc.Top = max(0, min(sc.Top, len(all)-h))
	for i := 0; i < h && sc.Top+i < len(all); i++ {
		l := all[sc.Top+i]
		if l.Label != "" {
			s.Raw(x, y+i, l.Label, stBright, labelW-1)
		}
		s.Raw(x+l.indent, y+i, l.T, l.St, w-l.indent)
	}
	if len(all) > h {
		s.Text(x+w-9, y+h-1, fmt.Sprintf(" %d/%d ", min(len(all), sc.Top+h), len(all)), stAccent, 9)
	}
}

// follower suit un job par son flux d'événements.
type follower struct {
	ID       string
	Profile  string
	State    string
	Progress float64
	Step     string
	Log      []string
	Result   json.RawMessage
	Err      string
	cancel   context.CancelFunc
}

func (f *follower) Running() bool {
	return f != nil && f.ID != "" && f.State != "succeeded" && f.State != "failed" && f.State != "cancelled"
}

// follow s'abonne aux événements du job id ; onData reçoit les événements de données (second, iperf).
func (a *App) follow(id string, f *follower, onData func(typ string, data json.RawMessage)) {
	if f.cancel != nil {
		f.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.ID, f.cancel, f.State = id, cancel, "running"
	go func() {
		err := a.C.Stream(ctx, "/jobs/"+url.PathEscape(id)+"/events", func(ev string, data []byte) {
			var e jobs.Event
			if json.Unmarshal(data, &e) != nil {
				return
			}
			a.Post(func() {
				if f.ID != id {
					return
				}
				switch e.Type {
				case "progress":
					f.Progress, f.Step = e.Progress, e.Step
				case "log":
					f.Log = append(f.Log, e.Msg)
					if len(f.Log) > 200 {
						f.Log = f.Log[len(f.Log)-200:]
					}
				case "result":
					f.Result = e.Data
				case "state":
					f.State = string(e.State)
					if e.Msg != "" {
						f.Err = e.Msg
					}
				default:
					if onData != nil {
						onData(e.Type, e.Data)
					}
				}
			})
		})
		// fin du flux : on relit l'état final (le job a pu se terminer pendant une coupure)
		var j jobs.Job
		if gerr := a.C.Get("/jobs/"+url.PathEscape(id), &j); gerr == nil {
			a.Post(func() {
				if f.ID != id {
					return
				}
				f.State, f.Progress = string(j.State), j.Progress
				if j.Result != nil {
					f.Result = j.Result
				}
				if j.Error != "" {
					f.Err = j.Error
				}
			})
		} else if err != nil {
			a.Post(func() { a.Error(err) })
		}
	}()
}

func (f *follower) stop() {
	if f != nil && f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}

// submitJob soumet un job puis le suit.
func (a *App) submitJob(req map[string]any, f *follower, onStart func(), onData func(string, json.RawMessage)) {
	a.Go(func() (any, error) {
		var j jobs.Job
		err := a.C.Post("/jobs", req, &j)
		return j, err
	}, func(v any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		j := v.(jobs.Job)
		*f = follower{Profile: j.Profile}
		if onStart != nil {
			onStart()
		}
		a.follow(j.ID, f, onData)
		a.Status("job "+j.Profile+" lancé ("+j.ID+")", stOK)
	})
}

// cancelJob demande l'annulation (avec confirmation).
func (a *App) cancelJob(f *follower) {
	if !f.Running() {
		return
	}
	id := f.ID
	a.Confirm("Annuler le test", []string{"Arrêter le job " + f.Profile + " en cours ?", "Le disque de test est conservé."}, func() {
		a.Go(func() (any, error) { return nil, a.C.Post("/jobs/"+url.PathEscape(id)+"/cancel", nil, nil) }, func(_ any, err error) { a.Error(err) })
	})
}

func stateStyle(state string) Style {
	switch state {
	case "succeeded":
		return stOK
	case "failed":
		return stAlert
	case "cancelled":
		return stWarn
	}
	return stAccent
}

func stateFR(state string) string {
	switch state {
	case "succeeded":
		return "terminé"
	case "failed":
		return "en échec"
	case "cancelled":
		return "annulé"
	case "queued":
		return "en attente"
	}
	return "en cours"
}

func levelStyle(l string) Style {
	switch l {
	case "OK":
		return stOK
	case "ATTENTION":
		return stWarn
	case "ALERTE":
		return stAlert
	}
	return stNormal
}

// progressBar dessine une barre de progression.
func progressBar(s *Screen, x, y, w int, pctv float64, label string) {
	n := int(pctv / 100 * float64(w))
	for i := 0; i < w; i++ {
		ch, st := '░', Style{Fg: White, Bg: Black}
		if i < n {
			ch, st = '█', stAccent
		}
		s.Set(x+i, y, ch, st)
	}
	txt := fmt.Sprintf(" %3.0f %% ", pctv)
	s.Raw(x+(w-len(txt))/2, y, txt, Style{Fg: Black, Bg: White}, 0)
	if label != "" {
		s.Text(x, y+1, label, stNormal, w)
	}
}
