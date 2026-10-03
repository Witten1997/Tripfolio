package backup

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
)

type RemoteBackup struct {
	ID           uuid.UUID `json:"id"`
	Destination  string    `json:"destination"`
	SnapshotAt   time.Time `json:"snapshot_at"`
	Size         int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
	GooseVersion int64     `json:"goose_version"`
}

type RemotePage struct {
	Destinations    []string       `json:"destinations"`
	Data            []RemoteBackup `json:"data"`
	Total           int            `json:"total"`
	Page            int            `json:"page"`
	PageSize        int            `json:"page_size"`
	Skipped         int            `json:"skipped"`
	SettingsVersion int64          `json:"settings_version"`
}

type RemoteRestoreInput struct {
	RestoreInput
	ID              uuid.UUID `json:"id"`
	Destination     string    `json:"destination"`
	SHA256          string    `json:"sha256"`
	SettingsVersion int64     `json:"settings_version"`
}

// RemoteRestore is constructed from the saved configuration and a fetched manifest.
// The client cannot provide database identity, credentials, URLs or archive metadata.
type RemoteRestore struct {
	Config          Config
	Manifest        Manifest
	SettingsVersion int64
}

type davEntry struct {
	name       string
	collection bool
	modified   time.Time
}

func (d *dav) entries(ctx context.Context) ([]davEntry, error) {
	body := `<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getlastmodified/></d:prop></d:propfind>`
	r, err := d.request(ctx, "PROPFIND", "", strings.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusMultiStatus {
		return nil, errors.New("WEBDAV_LIST_FAILED")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return nil, errors.New("WEBDAV_LIST_TOO_LARGE")
	}
	var listing struct {
		XMLName   xml.Name `xml:"DAV: multistatus"`
		Responses []struct {
			Href  string `xml:"DAV: href"`
			Props []struct {
				Status string `xml:"DAV: status"`
				Prop   struct {
					Type struct {
						Collection *struct{} `xml:"DAV: collection"`
					} `xml:"DAV: resourcetype"`
					Modified string `xml:"DAV: getlastmodified"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: propstat"`
		} `xml:"DAV: response"`
	}
	if xml.Unmarshal(b, &listing) != nil {
		return nil, errors.New("WEBDAV_LIST_FAILED")
	}
	entries := make([]davEntry, 0)
	seen := make(map[string]bool)
	for _, item := range listing.Responses {
		href, err := url.Parse(item.Href)
		if err != nil {
			continue
		}
		u := d.base.ResolveReference(href)
		if u.Scheme != d.base.Scheme || u.Host != d.base.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, d.base.Path) {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(u.Path, d.base.Path), "/")
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || seen[name] {
			continue
		}
		for _, prop := range item.Props {
			fields := strings.Fields(prop.Status)
			if len(fields) < 2 || fields[1] != "200" {
				continue
			}
			modified, _ := http.ParseTime(prop.Prop.Modified)
			entries = append(entries, davEntry{name, prop.Prop.Type.Collection != nil, modified})
			seen[name] = true
			break
		}
	}
	return entries, nil
}

func validDestination(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (s *Service) remoteManifest(ctx context.Context, d *dav, id uuid.UUID) (Manifest, error) {
	var m Manifest
	r, err := d.request(ctx, "GET", id.String()+".complete.json", nil, 0)
	if err != nil {
		return m, err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return m, errors.New("RESTORE_ARCHIVE_INVALID")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(b) > 65536 || json.Unmarshal(b, &m) != nil {
		return m, errors.New("RESTORE_ARCHIVE_INVALID")
	}
	hash, err := hex.DecodeString(m.SHA256)
	if err != nil || len(hash) != 32 || m.SHA256 != strings.ToLower(m.SHA256) || m.ID != id || m.Format != 1 || m.Size <= 0 || m.Size > s.options.MaxBytes || m.SnapshotAt.IsZero() || m.GooseVersion < 22 || m.RiverVersion < 1 || len(m.Schemas) != 2 || m.Schemas[0] != "public" || m.Schemas[1] != "tripfolio_private" {
		return m, errors.New("RESTORE_ARCHIVE_INVALID")
	}
	return m, nil
}

func remoteError(err error) error {
	if err == nil {
		return nil
	}
	message := "无法读取 WebDAV 备份，请检查目录地址、凭证及列出目录和读取权限"
	if err.Error() == "WEBDAV_LIST_TOO_LARGE" {
		message = "WebDAV 目录过大，请将所需备份目录放入单独的父目录后重试"
	}
	if err.Error() == "RESTORE_ARCHIVE_INVALID" {
		message = RestoreSummary(err.Error())
	}
	return apperr.New(502, "WEBDAV_REMOTE_FAILED", message)
}

func (s *Service) RemoteBackups(ctx context.Context, cfg Config, destination string, page int) (out RemotePage, err error) {
	out = RemotePage{Destinations: []string{}, Data: []RemoteBackup{}, Page: page, PageSize: 20}
	if page < 1 || page > 10000 || (destination != "" && !validDestination(destination)) {
		return out, apperr.BadRequest("MALFORMED_REQUEST", "备份目录或分页参数无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	defer func() { err = remoteError(err) }()
	d, err := s.davRoot(cfg)
	if err != nil {
		return out, err
	}
	defer d.close()
	if destination == "" {
		entries, e := d.entries(ctx)
		if e != nil {
			return out, e
		}
		for _, entry := range entries {
			id := strings.TrimPrefix(entry.name, "tripfolio-")
			if entry.collection && strings.HasPrefix(entry.name, "tripfolio-") && validDestination(id) {
				out.Destinations = append(out.Destinations, id)
			}
		}
		sort.Strings(out.Destinations)
		return out, nil
	}
	d.base.Path += "tripfolio-" + destination + "/"
	entries, err := d.entries(ctx)
	if err != nil {
		return out, err
	}
	files := make(map[string]bool)
	for _, entry := range entries {
		if !entry.collection {
			files[entry.name] = true
		}
	}
	var candidates []davEntry
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.name, ".complete.json")
		if !entry.collection && strings.HasSuffix(entry.name, ".complete.json") && validDestination(id) && files[id+".dump.age"] {
			candidates = append(candidates, entry)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modified.Equal(candidates[j].modified) {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].modified.After(candidates[j].modified)
	})
	out.Total = len(candidates)
	start := min((page-1)*out.PageSize, len(candidates))
	for _, entry := range candidates[start:min(start+out.PageSize, len(candidates))] {
		id, _ := uuid.Parse(strings.TrimSuffix(entry.name, ".complete.json"))
		m, e := s.remoteManifest(ctx, d, id)
		if e != nil {
			if e.Error() != "RESTORE_ARCHIVE_INVALID" {
				return out, e
			}
			out.Skipped++
			continue
		}
		out.Data = append(out.Data, RemoteBackup{m.ID, destination, m.SnapshotAt, m.Size, m.SHA256, m.GooseVersion})
	}
	return out, nil
}

func (s *Service) PrepareRemoteRestore(ctx context.Context, cfg Config, in RemoteRestoreInput) (RemoteRestore, error) {
	var out RemoteRestore
	if !validDestination(in.Destination) || in.ID == uuid.Nil || len(in.SHA256) != 64 || in.SettingsVersion < 1 {
		return out, apperr.BadRequest("MALFORMED_REQUEST", "备份选择无效，请重新查找")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cfg.Destination = in.Destination
	d, err := s.dav(cfg)
	if err != nil {
		return out, remoteError(err)
	}
	defer d.close()
	m, err := s.remoteManifest(ctx, d, in.ID)
	if err != nil {
		return out, remoteError(err)
	}
	if m.SHA256 != in.SHA256 {
		return out, apperr.Conflicted("RESTORE_ARCHIVE_CHANGED", "所选备份已变化，请重新查找并确认")
	}
	return RemoteRestore{cfg, m, in.SettingsVersion}, nil
}
