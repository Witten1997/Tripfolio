package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
)

type dav struct {
	client             *http.Client
	base               *url.URL
	username, password string
}

func (s *Service) validateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\\") || len(raw) > 2048 {
		return nil, errors.New("WEBDAV_URL_INVALID")
	}
	allowed := s.allowed[strings.ToLower(u.Hostname())]
	if u.Scheme != "https" && !(u.Scheme == "http" && allowed) {
		return nil, errors.New("WEBDAV_HTTPS_REQUIRED")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return nil, errors.New("WEBDAV_URL_INVALID")
		}
	}
	u.RawPath = ""
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	return u, nil
}

func (s *Service) dav(cfg Config) (*dav, error) {
	u, err := s.validateURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if _, err = uuid.Parse(cfg.Destination); err != nil {
		return nil, errors.New("WEBDAV_NOT_CONFIGURED")
	}
	password, err := s.unseal(cfg.Secret)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, errors.New("WEBDAV_ADDRESS_BLOCKED")
		}
		addresses, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(addresses) == 0 {
			return nil, errors.New("WEBDAV_DNS_FAILED")
		}
		for _, ip := range addresses {
			ip = ip.Unmap()
			if !s.allowed[strings.ToLower(host)] && !publicAddress(ip) {
				return nil, errors.New("WEBDAV_ADDRESS_BLOCKED")
			}
		}
		dialer := net.Dialer{Timeout: 10 * time.Second}
		for _, ip := range addresses {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, errors.New("WEBDAV_CONNECT_FAILED")
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("WEBDAV_REDIRECT_BLOCKED") }}
	u.Path = path.Join(u.Path, "tripfolio-"+cfg.Destination) + "/"
	return &dav{client, u, cfg.Username, password}, nil
}

func publicAddress(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}
func (d *dav) close() { d.client.CloseIdleConnections() }
func (d *dav) request(ctx context.Context, method, name string, body io.Reader, size int64) (*http.Response, error) {
	u := *d.base
	u.Path += name
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, errors.New("WEBDAV_REQUEST_FAILED")
	}
	req.SetBasicAuth(d.username, d.password)
	req.ContentLength = size
	if method == "PUT" {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, errors.New("WEBDAV_REQUEST_FAILED")
	}
	return resp, nil
}
func closeResponse(r *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
	_ = r.Body.Close()
}
func (d *dav) mkdir(ctx context.Context) error {
	r, e := d.request(ctx, "MKCOL", "", nil, 0)
	if e != nil {
		return e
	}
	defer closeResponse(r)
	if r.StatusCode != 201 && r.StatusCode != 405 {
		return errors.New("WEBDAV_DIRECTORY_FAILED")
	}
	return nil
}
func (d *dav) put(ctx context.Context, name string, reader io.Reader, size int64) error {
	r, e := d.request(ctx, "PUT", name, reader, size)
	if e != nil {
		return e
	}
	defer closeResponse(r)
	if r.StatusCode != 200 && r.StatusCode != 201 && r.StatusCode != 204 {
		return errors.New("WEBDAV_UPLOAD_FAILED")
	}
	return nil
}
func (d *dav) remove(ctx context.Context, name string) error {
	r, e := d.request(ctx, "DELETE", name, nil, 0)
	if e != nil {
		return e
	}
	defer closeResponse(r)
	if r.StatusCode != 200 && r.StatusCode != 204 && r.StatusCode != 404 {
		return errors.New("WEBDAV_DELETE_FAILED")
	}
	return nil
}
func (d *dav) verify(ctx context.Context, name string, size int64, digest string) error {
	r, e := d.request(ctx, "GET", name, nil, 0)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return errors.New("WEBDAV_VERIFY_FAILED")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(r.Body, size+1))
	if e != nil || n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("WEBDAV_VERIFY_FAILED")
	}
	return nil
}
func (d *dav) putBytes(ctx context.Context, name string, b []byte) error {
	if err := d.put(ctx, name, bytes.NewReader(b), int64(len(b))); err != nil {
		return err
	}
	h := sha256.Sum256(b)
	return d.verify(ctx, name, int64(len(b)), hex.EncodeToString(h[:]))
}
func (d *dav) upload(ctx context.Context, name, filename string, size int64) error {
	f, e := os.Open(filename)
	if e != nil {
		return errors.New("BACKUP_FILE_MISSING")
	}
	defer f.Close()
	return d.put(ctx, name, f, size)
}

func (s *Service) Probe(ctx context.Context, cfg Config) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	d, err := s.dav(cfg)
	if err != nil {
		return apperr.New(422, "WEBDAV_TEST_FAILED", Summary(err.Error()))
	}
	defer d.close()
	name := "probe-" + uuid.NewString()
	payload := make([]byte, 64)
	if _, err = rand.Read(payload); err != nil {
		return err
	}
	if err = d.mkdir(ctx); err == nil {
		err = d.putBytes(ctx, name, payload)
	}
	cleanCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	cleanup := d.remove(cleanCtx, name)
	if err == nil {
		err = cleanup
	}
	if err != nil {
		return apperr.New(502, "WEBDAV_TEST_FAILED", Summary(err.Error()))
	}
	return nil
}
