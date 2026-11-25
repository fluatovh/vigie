package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client appelle l'API du serveur vigie par le socket de la console (ou une URL, pour les tests).
type Client struct {
	Base string // http://console (socket) ou URL de test
	HTTP *http.Client
}

// NewSocketClient crée un client sur le socket Unix de la console.
func NewSocketClient(path string) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
	return &Client{Base: "http://console", HTTP: &http.Client{Transport: tr}}
}

// APIError est une erreur renvoyée par l'API.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// Do exécute une requête JSON ; out peut être nil.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+"/api"+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("service vigie injoignable : %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return &APIError{resp.StatusCode, e.Error}
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Get raccourci.
func (c *Client) Get(path string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

// Post raccourci (délai long : rescan, adoption, ajout de pair).
func (c *Client) Post(path string, in, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return c.Do(ctx, http.MethodPost, path, in, out)
}

// Delete raccourci.
func (c *Client) Delete(path string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.Do(ctx, http.MethodDelete, path, nil, out)
}

// Stream lit un flux SSE et appelle fn(événement, données) jusqu'à la fin ou l'annulation.
func (c *Client) Stream(ctx context.Context, path string, fn func(event string, data []byte)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/api"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return &APIError{resp.StatusCode, e.Error}
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	event := "message"
	var data []byte
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data != nil {
				fn(event, data)
			}
			event, data = "message", nil
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimSpace(line[5:])...)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return sc.Err()
}

// Do2 : requête modifiante (PUT, POST) avec un délai long, sans réponse attendue.
func (c *Client) Do2(method, path string, in any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return c.Do(ctx, method, path, in, nil)
}
