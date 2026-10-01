package bootstrap

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	adminpg "tripfolio/server/internal/adapters/postgres/admin"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/config"
	"tripfolio/server/internal/modules/account"
)

// RunAdmin 只在运维命令中授予或撤销资格，不提供公开注册或自动提权入口。
func RunAdmin(ctx context.Context, cfg config.Config, args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "grant" && args[0] != "revoke") {
		return fmt.Errorf("用法：tripfolio admin grant|revoke --email 邮箱 --reason 原因")
	}
	flags := flag.NewFlagSet("admin "+args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	email := flags.String("email", "", "已验证的现有账号邮箱")
	reason := flags.String("reason", "", "操作原因（必填）")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不支持多余的位置参数")
	}
	_, key, err := account.NormalizeEmail(*email)
	if err != nil {
		return fmt.Errorf("邮箱无效：%w", err)
	}
	why := strings.TrimSpace(*reason)
	if why == "" || utf8.RuneCountInString(why) > 500 {
		return fmt.Errorf("操作原因须为 1–500 个字符")
	}
	pool, err := pgcore.NewMigrationPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	id, err := adminpg.NewStore(pool).ChangePrincipal(ctx, key, why, args[0] == "grant")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "超级管理员资格 %s 成功，账号编号：%s\n", args[0], id)
	return err
}
