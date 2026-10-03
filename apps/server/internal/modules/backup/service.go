package backup

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
)

type Options struct {
	Key, DatabaseURL, Directory string
	MaxBytes                    int64
	Timeout                     time.Duration
	AllowedHosts                []string
}
type Service struct {
	store   Store
	key     []byte
	options Options
	allowed map[string]bool
}

func New(store Store, o Options) *Service {
	s := &Service{store: store, options: o, allowed: map[string]bool{}}
	if o.Key != "" {
		s.key, _ = ParseKey(o.Key)
	}
	for _, host := range o.AllowedHosts {
		s.allowed[strings.ToLower(strings.TrimSpace(host))] = true
	}
	if s.options.Directory == "" {
		s.options.Directory = filepath.Join(os.TempDir(), "tripfolio-backups")
	}
	if s.options.MaxBytes == 0 {
		s.options.MaxBytes = 5 << 30
	}
	if s.options.Timeout == 0 {
		s.options.Timeout = time.Hour
	}
	return s
}
func (s *Service) Readiness() (bool, string) {
	if len(s.key) != 32 {
		return false, "服务器尚未配置 TRIPFOLIO_BACKUP_KEY"
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		return false, "服务器未安装 PostgreSQL pg_dump 客户端"
	}
	return true, "已配置备份密钥和数据库导出工具"
}
func (s *Service) Validate(in Update) error {
	if _, err := time.Parse("15:04", in.Time); err != nil || len(in.Time) != 5 {
		return apperr.BadRequest("BACKUP_SETTINGS_INVALID", "备份时间须为 HH:mm")
	}
	if in.Retain < 1 || in.Retain > 90 || in.Version < 1 || len(in.Username) > 256 || len(in.Password) > 2048 || strings.ContainsAny(in.Username, "\r\n") {
		return apperr.BadRequest("BACKUP_SETTINGS_INVALID", "保留份数应为 1–90，凭证或版本无效")
	}
	if _, err := s.validateURL(in.URL); err != nil {
		return apperr.BadRequest("BACKUP_SETTINGS_INVALID", Summary(err.Error()))
	}
	if in.Enabled || in.Password != "" {
		if ok, why := s.Readiness(); !ok {
			return apperr.New(503, "BACKUP_UNAVAILABLE", why)
		}
	}
	return nil
}
func (s *Service) Schedule(ctx context.Context) error { return s.store.Schedule(ctx) }

func Summary(code string) string {
	switch code {
	case "WEBDAV_HTTPS_REQUIRED":
		return "WebDAV 需使用 HTTPS；内网 HTTP 须在服务器配置允许的主机"
	case "WEBDAV_URL_INVALID":
		return "请填写不含账号、查询参数或片段的 WebDAV 目录地址"
	case "WEBDAV_ADDRESS_BLOCKED":
		return "目标地址受限；内网 WebDAV 需配置服务器主机白名单"
	case "WEBDAV_DIRECTORY_FAILED":
		return "无法创建备份目录，请检查地址及写入权限"
	case "WEBDAV_UPLOAD_FAILED":
		return "上传失败，请检查 WebDAV 凭证、空间和写入权限"
	case "WEBDAV_VERIFY_FAILED":
		return "远端文件读取或 SHA-256 完整性校验失败"
	case "WEBDAV_DELETE_FAILED":
		return "远端文件删除失败，请检查删除权限"
	case "BACKUP_KEY_INVALID":
		return "备份密钥无法解密已保存凭证，请核对服务器密钥"
	case "BACKUP_KEY_MISSING":
		return "服务器未配置有效备份密钥"
	case "BACKUP_SPACE_LIMIT":
		return "备份暂存空间达到上限，请清理失败文件或提高容量配置"
	case "BACKUP_TOO_LARGE":
		return "备份超过单文件大小上限"
	case "BACKUP_VERSION_MISMATCH":
		return "pg_dump 主版本低于数据库版本，请升级客户端"
	case "BACKUP_DATABASE_MISMATCH":
		return "备份连接与当前应用不是同一个数据库，请核对配置"
	case "BACKUP_DUMP_FAILED":
		return "数据库导出失败，请检查连接、读取权限、客户端版本和锁等待"
	case "BACKUP_TIMEOUT":
		return "备份超时或服务已停止，可重试"
	case "BACKUP_INTERRUPTED":
		return "任务中断且未完成，可重试"
	case "BACKUP_FILE_MISSING":
		return "本地暂存文件不可用"
	case "BACKUP_AUDIT_FAILED":
		return "备份状态或审计记录保存失败"
	case "BACKUP_RESTORE_REQUIRED":
		return "备份文件校验失败，需要重新导出"
	case "":
		return ""
	default:
		return "备份或 WebDAV 请求失败，请检查服务器配置和网络"
	}
}

func (s *Service) Execute(ctx context.Context, id uuid.UUID, lastAttempt bool) error {
	ctx, stop := context.WithTimeout(ctx, s.options.Timeout)
	defer stop()
	lease, release, err := s.store.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx = lease
	run, err := s.store.LoadRun(ctx, id)
	if err != nil {
		return errors.New("BACKUP_STATE_FAILED")
	}
	if run.State == "succeeded" {
		return nil
	}
	if err = s.store.Progress(ctx, id, "running", nil); err != nil {
		return errors.New("BACKUP_AUDIT_FAILED")
	}
	filename := filepath.Join(s.options.Directory, id.String()+".dump.age")
	err = s.execute(ctx, &run, filename)
	if err != nil {
		code := err.Error()
		if ctx.Err() != nil {
			code = "BACKUP_TIMEOUT"
		}
		state := "retrying"
		if lastAttempt {
			state = "failed"
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := s.store.Finish(finishCtx, id, state, code); e != nil {
			return errors.New("BACKUP_AUDIT_FAILED")
		}
		if lastAttempt && run.Manifest.Size > 0 {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if e := s.discardRemote(cleanCtx, run); e != nil {
				_ = s.store.CleanupError(finishCtx, id, "WEBDAV_DELETE_FAILED")
			}
			cancel()
		}
		return errors.New(code)
	}
	if err = s.store.Finish(ctx, id, "succeeded", ""); err != nil {
		state := "retrying"
		if lastAttempt {
			state = "failed"
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.store.Finish(finishCtx, id, state, "BACKUP_AUDIT_FAILED")
		cancel()
		return errors.New("BACKUP_AUDIT_FAILED")
	}
	_ = os.Remove(filename)
	// Only verified, completed files from this destination participate in retention.
	if err = s.prune(ctx, run.Config); err != nil {
		_ = s.store.CleanupError(ctx, id, "WEBDAV_DELETE_FAILED")
	}
	return nil
}

func (s *Service) execute(ctx context.Context, r *Run, filename string) error {
	if len(s.key) != 32 {
		return errors.New("BACKUP_KEY_MISSING")
	}
	if _, err := s.unseal(r.Config.Secret); err != nil {
		return err
	}
	if !validArchive(filename, r.Manifest) {
		if err := s.prepareDirectory(); err != nil {
			return err
		}
		m, err := s.dump(ctx, r.ID, filename, r.Config.DatabaseID)
		if err != nil {
			return err
		}
		r.Manifest = m
	}
	if err := s.store.Progress(ctx, r.ID, "uploading", &r.Manifest); err != nil {
		return errors.New("BACKUP_AUDIT_FAILED")
	}
	d, err := s.dav(r.Config)
	if err != nil {
		return err
	}
	defer d.close()
	if err = d.mkdir(ctx); err != nil {
		return err
	}
	name := r.ID.String() + ".dump.age"
	if err = d.upload(ctx, name, filename, r.Manifest.Size); err != nil {
		return err
	}
	if err = s.store.Progress(ctx, r.ID, "verifying", nil); err != nil {
		return errors.New("BACKUP_AUDIT_FAILED")
	}
	if err = d.verify(ctx, name, r.Manifest.Size, r.Manifest.SHA256); err != nil {
		return err
	}
	manifest, _ := json.Marshal(r.Manifest)
	return d.putBytes(ctx, r.ID.String()+".complete.json", manifest)
}
func validArchive(filename string, m Manifest) bool {
	if m.Size <= 0 || m.SHA256 == "" {
		return false
	}
	stat, err := os.Lstat(filename)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != m.Size {
		return false
	}
	f, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return err == nil && hex.EncodeToString(h.Sum(nil)) == m.SHA256
}
func (s *Service) prepareDirectory() error {
	if err := os.MkdirAll(s.options.Directory, 0700); err != nil {
		return errors.New("BACKUP_SPACE_LIMIT")
	}
	st, err := os.Lstat(s.options.Directory)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("BACKUP_SPACE_LIMIT")
	}
	entries, err := os.ReadDir(s.options.Directory)
	if err != nil {
		return errors.New("BACKUP_SPACE_LIMIT")
	}
	var total int64
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil {
			return errors.New("BACKUP_SPACE_LIMIT")
		}
		name := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".partial"), ".dump.age")
		_, owned := uuid.Parse(name)
		if owned == nil && (strings.HasSuffix(entry.Name(), ".dump.age") || strings.HasSuffix(entry.Name(), ".dump.age.partial")) && info.Mode().IsRegular() && time.Since(info.ModTime()) > 7*24*time.Hour {
			if os.Remove(filepath.Join(s.options.Directory, entry.Name())) == nil {
				continue
			}
		}
		total += info.Size()
	}
	if total > s.options.MaxBytes*2 {
		return errors.New("BACKUP_SPACE_LIMIT")
	}
	return nil
}
func (s *Service) prune(ctx context.Context, cfg Config) error {
	expired, err := s.store.Expired(ctx, cfg)
	if err != nil {
		return err
	}
	if len(expired) == 0 {
		return nil
	}
	d, err := s.dav(cfg)
	if err != nil {
		return err
	}
	defer d.close()
	for _, r := range expired {
		if err = s.store.BeforeDelete(ctx, r.ID); err != nil {
			return err
		}
		if err = d.remove(ctx, r.ID.String()+".complete.json"); err != nil {
			return err
		}
		if err = d.remove(ctx, r.ID.String()+".dump.age"); err != nil {
			return err
		}
		if err = s.store.Deleted(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) passphrase() string { return base64.StdEncoding.EncodeToString(s.key) }

func (s *Service) discardRemote(ctx context.Context, r Run) error {
	d, err := s.dav(r.Config)
	if err != nil {
		return err
	}
	defer d.close()
	if err = d.remove(ctx, r.ID.String()+".complete.json"); err != nil {
		return err
	}
	return d.remove(ctx, r.ID.String()+".dump.age")
}
