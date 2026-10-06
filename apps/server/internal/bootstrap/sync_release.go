package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"tripfolio/server/internal/web"
)

const releaseProfile = "tripfolio-sync-v2-h07"
const releaseDirectory = "tripfolio.sync-release"
const releaseFileLimit int64 = 8 << 20

var errSyncRelease = errors.New("同步发布验收资料不可用")
var requiredReleaseSuites = []string{"admission", "sync-operations", "activation-core", "runtime", "web-consumers"}

// These are observed artifact facts, not an acceptance decision.
type syncReleaseInfo struct {
	Schema           int    `json:"schema"`
	ExecutableSHA256 string `json:"executable_sha256"`
	WebTreeSHA256    string `json:"web_tree_sha256"`
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
}
type releaseReference struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type releaseSuite struct {
	ID               string            `json:"id"`
	Status           string            `json:"status"`
	SourceCommit     string            `json:"source_commit"`
	ExecutableSHA256 string            `json:"executable_sha256"`
	WebTreeSHA256    string            `json:"web_tree_sha256"`
	Report           releaseReference  `json:"report"`
	Reuse            *releaseReference `json:"reuse,omitempty"`
}
type releaseManifest struct {
	Schema       int              `json:"schema"`
	Profile      string           `json:"profile"`
	SourceCommit string           `json:"source_commit"`
	Artifact     syncReleaseInfo  `json:"artifact"`
	Build        releaseReference `json:"build"`
	Suites       []releaseSuite   `json:"suites"`
}
type releaseBuild struct {
	SourceCommit string           `json:"source_commit"`
	Artifact     syncReleaseInfo  `json:"artifact"`
	Log          releaseReference `json:"log"`
}
type releaseReuse struct {
	OriginalSourceCommit     string           `json:"original_source_commit"`
	OriginalExecutableSHA256 string           `json:"original_executable_sha256"`
	OriginalWebTreeSHA256    string           `json:"original_web_tree_sha256"`
	ReportSHA256             string           `json:"report_sha256"`
	SourceCommit             string           `json:"source_commit"`
	Artifact                 syncReleaseInfo  `json:"artifact"`
	Equivalence              releaseReference `json:"equivalence"`
}

type syncReleaseVerifier struct{}

func (syncReleaseVerifier) Verify(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return errSyncRelease
	}
	assets, err := web.Assets()
	if err != nil {
		return errSyncRelease
	}
	if err = verifySyncRelease(ctx, exe, assets); err != nil {
		return errSyncRelease
	}
	return nil
}

func canonicalHash(s string, size int) bool {
	if len(s) != size || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func hashReader(ctx context.Context, r io.Reader, limit int64) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, r}, limit+1))
	if err != nil || n > limit {
		return "", n, errSyncRelease
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func releaseFacts(ctx context.Context, executable string, assets fs.FS) (syncReleaseInfo, error) {
	var out syncReleaseInfo
	st, err := os.Lstat(executable)
	if err != nil || !st.Mode().IsRegular() {
		return out, errSyncRelease
	}
	f, err := os.Open(executable)
	if err != nil {
		return out, errSyncRelease
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return out, errSyncRelease
	}
	exeHash, _, err := hashReader(ctx, f, 512<<20)
	if err != nil {
		return out, err
	}
	index, err := fs.Stat(assets, "index.html")
	if err != nil || !index.Mode().IsRegular() || index.Size() == 0 {
		return out, errSyncRelease
	}
	tree := sha256.New()
	var total int64
	files := 0
	err = fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || !fs.ValidPath(path) {
			return errSyncRelease
		}
		files++
		if files > 20000 {
			return errSyncRelease
		}
		file, err := assets.Open(path)
		if err != nil {
			return err
		}
		digest, size, err := hashReader(ctx, file, (512<<20)-total)
		file.Close()
		if err != nil {
			return err
		}
		total += size
		binary.Write(tree, binary.BigEndian, uint64(len(path)))
		tree.Write([]byte(path))
		binary.Write(tree, binary.BigEndian, uint64(size))
		raw, _ := hex.DecodeString(digest)
		tree.Write(raw)
		return nil
	})
	if err != nil {
		return out, errSyncRelease
	}
	return syncReleaseInfo{1, exeHash, hex.EncodeToString(tree.Sum(nil)), runtime.GOOS, runtime.GOARCH}, nil
}

// The directory and its parent must be maintained by the trusted deployer and
// read-only to the service identity. Portable mode bits do not prove OS ACLs.
func readReleaseFile(ctx context.Context, root *os.Root, path string, limit int64) ([]byte, error) {
	if !fs.ValidPath(path) || path == "." || strings.ContainsAny(path, "\\:") {
		return nil, errSyncRelease
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		st, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return nil, errSyncRelease
		}
		if i < len(parts)-1 && !st.IsDir() {
			return nil, errSyncRelease
		}
		if i == len(parts)-1 && (!st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > limit) {
			return nil, errSyncRelease
		}
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, errSyncRelease
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errSyncRelease
	}
	return data, nil
}

// encoding/json otherwise accepts duplicate object keys. Reject them as well
// as unknown fields and trailing values in all acceptance metadata.
func strictReleaseJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return errSyncRelease
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				name = strings.ToLower(name)
				if !ok || seen[name] {
					return errSyncRelease
				}
				seen[name] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errSyncRelease
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return errSyncRelease
	}
	if _, err := d.Token(); err != io.EOF {
		return errSyncRelease
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errSyncRelease
	}
	return nil
}

func verifySyncRelease(ctx context.Context, executable string, assets fs.FS) error {
	facts, err := releaseFacts(ctx, executable, assets)
	if err != nil {
		return err
	}
	dir := filepath.Join(filepath.Dir(executable), releaseDirectory)
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errSyncRelease
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errSyncRelease
	}
	defer root.Close()
	raw, err := readReleaseFile(ctx, root, "manifest.json", 128<<10)
	if err != nil {
		return err
	}
	var manifest releaseManifest
	if strictReleaseJSON(raw, &manifest) != nil || manifest.Schema != 1 || manifest.Profile != releaseProfile || !canonicalHash(manifest.SourceCommit, 40) || manifest.Artifact != facts {
		return errSyncRelease
	}
	readRef := func(ref releaseReference) ([]byte, error) {
		if !canonicalHash(ref.SHA256, 64) {
			return nil, errSyncRelease
		}
		data, err := readReleaseFile(ctx, root, ref.Path, releaseFileLimit)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != ref.SHA256 {
			return nil, errSyncRelease
		}
		return data, nil
	}
	buildRaw, err := readRef(manifest.Build)
	if err != nil {
		return err
	}
	var build releaseBuild
	if strictReleaseJSON(buildRaw, &build) != nil || build.SourceCommit != manifest.SourceCommit || build.Artifact != facts {
		return errSyncRelease
	}
	if _, err = readRef(build.Log); err != nil {
		return err
	}
	if len(manifest.Suites) != len(requiredReleaseSuites) {
		return errSyncRelease
	}
	seen := map[string]bool{}
	for _, suite := range manifest.Suites {
		known := false
		for _, id := range requiredReleaseSuites {
			if suite.ID == id {
				known = true
			}
		}
		if !known || seen[suite.ID] || suite.Status != "passed" || !canonicalHash(suite.SourceCommit, 40) || !canonicalHash(suite.ExecutableSHA256, 64) || !canonicalHash(suite.WebTreeSHA256, 64) {
			return errSyncRelease
		}
		seen[suite.ID] = true
		if _, err = readRef(suite.Report); err != nil {
			return err
		}
		matches := suite.SourceCommit == manifest.SourceCommit && suite.ExecutableSHA256 == facts.ExecutableSHA256 && suite.WebTreeSHA256 == facts.WebTreeSHA256
		if !matches && suite.Reuse == nil {
			return errSyncRelease
		}
		if suite.Reuse != nil {
			data, err := readRef(*suite.Reuse)
			if err != nil {
				return err
			}
			var reuse releaseReuse
			if strictReleaseJSON(data, &reuse) != nil || reuse.SourceCommit != manifest.SourceCommit || reuse.Artifact != facts || reuse.OriginalSourceCommit != suite.SourceCommit || reuse.OriginalExecutableSHA256 != suite.ExecutableSHA256 || reuse.OriginalWebTreeSHA256 != suite.WebTreeSHA256 || reuse.ReportSHA256 != suite.Report.SHA256 {
				return errSyncRelease
			}
			if _, err = readRef(reuse.Equivalence); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
