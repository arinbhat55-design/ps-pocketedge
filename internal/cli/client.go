// Package cli implements the PS-pocketEdge terminal client.
package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type config struct {
	URL    string `json:"url"`
	Token  string `json:"token,omitempty"`
	CAFile string `json:"caFile,omitempty"`
}

type options struct {
	url, configPath, caFile string
	json                    bool
	timeout                 time.Duration
}

func (o *options) path() (string, error) {
	if o.configPath != "" {
		return o.configPath, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pspocketedge", "cli.json"), nil
}

func (o *options) load() (config, error) {
	p, err := o.path()
	if err != nil {
		return config{}, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return config{}, nil
	}
	if err != nil {
		return config{}, err
	}
	var c config
	err = json.Unmarshal(b, &c)
	return c, err
}

func (o *options) save(c config) error {
	p, err := o.path()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".pse-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	b, err := json.MarshalIndent(c, "", "  ")
	if err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), p)
}

func validateURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("URL must be a control-plane origin, e.g. https://control.example.com:8080")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", fmt.Errorf("remote control planes require HTTPS; HTTP is allowed only on loopback")
	}
	return strings.TrimRight(raw, "/"), nil
}

type client struct {
	config
	http *http.Client
}

func (o *options) client(auth bool) (*client, error) {
	c, err := o.load()
	if err != nil {
		return nil, fmt.Errorf("read CLI configuration: %w", err)
	}
	if o.url != "" {
		if strings.TrimRight(o.url, "/") != c.URL {
			c.Token = ""
			c.CAFile = ""
		}
		c.URL = o.url
	}
	if c.URL == "" {
		c.URL = "http://127.0.0.1:8080"
	}
	c.URL, err = validateURL(c.URL)
	if err != nil {
		return nil, err
	}
	if o.caFile != "" {
		c.CAFile = o.caFile
	}
	if token := os.Getenv("PSE_TOKEN"); token != "" {
		c.Token = token
	}
	if auth && c.Token == "" {
		return nil, fmt.Errorf("login required: run pse login --email <email> --url %s", c.URL)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, err
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	timeout := o.timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	if timeout < 0 {
		return nil, fmt.Errorf("--timeout must be positive")
	}
	return &client{c, &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.URL+path, input)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		r.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		message := strings.TrimSpace(string(b))
		// Login itself sends no token; a 401 there is a wrong password.
		if resp.StatusCode == http.StatusUnauthorized && c.Token != "" {
			message += "; run pse login to renew your session"
		}
		return nil, fmt.Errorf("API %s: %s", resp.Status, message)
	}
	return resp, nil
}

func (c *client) call(ctx context.Context, method, path string, body, result any) error {
	r, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode == http.StatusNoContent {
		return nil
	}
	if result == nil {
		_, err = io.Copy(io.Discard, r.Body)
		return err
	}
	return json.NewDecoder(r.Body).Decode(result)
}
