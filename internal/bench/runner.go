package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner lance `vigie worker` en sous-processus et relaie ses événements.
type Runner struct {
	Binary string // chemin du binaire vigie (par défaut : le binaire courant)
}

// Report est le résultat complet d'un run.
type Report struct {
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended"`
	Request  Request   `json:"request"`
	CPUs     []int     `json:"worker_cpus"`
	Prefill  *Result   `json:"prefill,omitempty"`
	Results  []*Result `json:"results"`
	Error    string    `json:"error,omitempty"`
	Finished bool      `json:"finished"`
}

// Run exécute la requête et appelle onEvent pour chaque événement (progression). Annulable par ctx
// (le worker est tué, le disque reste intact : seules des données de bench y sont).
func (r *Runner) Run(ctx context.Context, req Request, onEvent func(Event)) (*Report, error) {
	bin := r.Binary
	if bin == "" {
		var err error
		if bin, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	rep := &Report{Started: time.Now(), Request: req}
	rep.Request.Record.BackingUUID = req.Record.BackingUUID
	cmd := exec.CommandContext(ctx, bin, "worker")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lancement du worker : %w", err)
	}
	log.Printf("bench: worker pid %d lancé (%d palier(s), prefill=%v)", cmd.Process.Pid, len(req.Steps), req.Prefill)
	if err := json.NewEncoder(stdin).Encode(req); err != nil {
		cmd.Process.Kill()
		return nil, err
	}
	stdin.Close()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var werr string
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			log.Printf("bench: ligne worker illisible : %s", truncate(sc.Text(), 200))
			continue
		}
		switch e.Type {
		case "start":
			rep.CPUs = e.CPUs
		case "prefill":
			if e.Result != nil {
				rep.Prefill = e.Result
			}
		case "step":
			if e.Result != nil {
				rep.Results = append(rep.Results, e.Result)
			}
		case "done":
			rep.Finished = true
		case "error":
			werr = e.Msg
		}
		if onEvent != nil {
			onEvent(e)
		}
	}
	waitErr := cmd.Wait()
	rep.Ended = time.Now()
	switch {
	case werr != "":
		rep.Error = werr
	case ctx.Err() != nil:
		rep.Error = "annulé"
	case waitErr != nil && !rep.Finished:
		rep.Error = fmt.Sprintf("worker terminé anormalement : %v", waitErr)
	}
	if rep.Error != "" {
		return rep, errors.New(rep.Error)
	}
	return rep, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var _ io.Reader = strings.NewReader("")
