package backup

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const RestoreConfirmation = "确认覆盖当前数据库"

type RestoreInput struct {
	Confirmation string `json:"confirmation"`
	Password     string `json:"password"`
	Reason       string `json:"reason"`
}

type RestoreJob struct {
	ID         uuid.UUID  `json:"id"`
	BackupID   uuid.UUID  `json:"backup_id"`
	State      string     `json:"state"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	ErrorCode  string     `json:"error_code"`
	Message    string     `json:"message"`
	Token      string     `json:"token,omitempty"`
	Secret     string     `json:"-"`
	Config     Config     `json:"-"`
	Manifest   Manifest   `json:"-"`
}

type RestoreStore interface {
	PendingRestore(context.Context) (*RestoreJob, error)
	RestoreProgress(context.Context, uuid.UUID, string, string) error
	RestoreStatus(context.Context, uuid.UUID, []byte) (RestoreJob, error)
	ResetConnections()
}

func (s *Service) WithRestores(store RestoreStore, databaseURL string) *Service {
	s.restores, s.restoreURL = store, databaseURL
	return s
}

func RestoreSummary(code string) string {
	switch code {
	case "RESTORE_INTERRUPTED":
		return "恢复任务因服务中断而停止，未提交的数据库变更已回滚"
	case "RESTORE_ARCHIVE_INVALID":
		return "备份文件缺失、损坏或完整性校验失败，当前数据库未被替换"
	case "RESTORE_PASSWORD_INVALID":
		return "备份解密失败，请输入备份当时的加密密码"
	case "RESTORE_VERSION_MISMATCH":
		return "备份结构版本与当前程序不兼容，请使用匹配版本的备份"
	case "RESTORE_DATABASE_MISMATCH":
		return "恢复连接未通过当前数据库身份校验"
	case "RESTORE_FAILED":
		return "数据库恢复未提交，现有数据保持不变；请检查数据库权限、空间和备份兼容性"
	case "RESTORE_TIMEOUT":
		return "恢复超时，未提交的数据库变更已回滚"
	case "RESTORE_SPACE_LIMIT":
		return "恢复暂存空间不足或文件超过容量上限"
	case "":
		return ""
	default:
		return "恢复准备失败，当前数据库未被替换，请检查备份文件及 WebDAV 配置"
	}
}
