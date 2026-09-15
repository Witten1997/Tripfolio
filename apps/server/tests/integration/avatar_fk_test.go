package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"tripfolio/server/internal/adapters/postgres/pgcore"
)

// avatarFKConstraint 是 00009 迁移建的复合外键 (id, avatar_asset_id) → assets(account_id, id)。
const avatarFKConstraint = "accounts_avatar_asset_fk"

// avatarFKFixture 固化头像外键语义只需要数据库：账号走 HTTP 注册，头像资产直接写库。
// 资产创建接口（POST /assets）在对象存储未装配时报依赖错误（assets.Service.Create → storageReady），
// 而这两条用例验证的是 schema 契约，只设 TRIPFOLIO_TEST_DATABASE_URL 时也必须真跑，
// 所以资产行按 assets 表的约束直接插入（与 ledgerFixture.insertAsset 同一手法）。
type avatarFKFixture struct {
	*apiFixture
	token     string
	accountID string
}

func newAvatarFKFixture(t *testing.T) *avatarFKFixture {
	t.Helper()
	f := newAPIFixture(t)
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	token, _ := reg.data()["access_token"].(string)
	accountID, _ := reg.data()["account"].(map[string]any)["id"].(string)
	return &avatarFKFixture{apiFixture: f, token: token, accountID: accountID}
}

// insertAvatarAsset 插入一条 uploading 的 avatar 资产：scope=avatar 必须不带 trip_id，
// uploading 状态又要求暂存键、期望大小、声明类型与截止时间齐全（assets_uploading_requires_staging）。
func (f *avatarFKFixture) insertAvatarAsset(accountID string) string {
	f.t.Helper()
	id := uuid.NewString()
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO assets (id, account_id, scope, trip_id, original_name, status, declared_media_type, staging_object_key, expected_size, upload_expires_at)
		 VALUES ($1, $2, 'avatar', NULL, 'me.jpg', 'uploading', 'image/jpeg', $3, 1024, now() + interval '1 hour')`,
		id, accountID, "staging/avatar-"+id,
	); err != nil {
		f.t.Fatalf("插入头像资产：%v", err)
	}
	return id
}

// bindAvatar 走 PATCH /account 绑定头像；引用不要求资产 ready（接口设计 3.7）。
func (f *avatarFKFixture) bindAvatar(assetID string) apiResponse {
	f.t.Helper()
	acc := f.do(request{method: http.MethodGet, path: "/account", token: f.token})
	expectStatus(f.t, acc, http.StatusOK, "")
	version := acc.data()["version"].(string)
	return f.do(request{method: http.MethodPatch, path: "/account", token: f.token, body: map[string]any{"avatar_asset_id": assetID},
		headers: f.authHeaders(map[string]string{"If-Match": `"` + version + `"`})})
}

// accountAvatar 读 accounts 行的主键与头像指针；返回的指针为空串表示 NULL。
func (f *avatarFKFixture) accountAvatar(accountID string) (accountRowID, avatarID string) {
	f.t.Helper()
	if err := f.pool.QueryRow(context.Background(),
		`SELECT id::text, coalesce(avatar_asset_id::text, '') FROM accounts WHERE id = $1`, accountID,
	).Scan(&accountRowID, &avatarID); err != nil {
		f.t.Fatalf("读取 accounts 行：%v", err)
	}
	return accountRowID, avatarID
}

// 删除头像资产要清空指针，但 accounts.id 必须原样保留。
// 为什么是触发器而不是列子集 SET NULL：列子集 ON DELETE SET NULL (avatar_asset_id) 需要 PostgreSQL 15+，
// 而自托管下限是 13/14；普通的 ON DELETE SET NULL 更不能用——外键是 (id, avatar_asset_id) 复合键，
// 置空会落到 id 列上。因此 00009 让外键保持默认 NO ACTION，改由 assets 上的 BEFORE DELETE 触发器
// 先把 accounts.avatar_asset_id 清空：触发器在就删得掉；触发器一旦缺失，删除会被外键拒绝，
// 也不会留下指向已删资产的悬挂引用。
func TestAvatarFKDeleteAssetClearsAccountPointer(t *testing.T) {
	f := newAvatarFKFixture(t)
	ctx := context.Background()

	avatarID := f.insertAvatarAsset(f.accountID)
	res := f.bindAvatar(avatarID)
	expectStatus(t, res, http.StatusOK, "")
	if got := res.data()["data"].(map[string]any)["avatar_asset_id"]; got != avatarID {
		t.Fatalf("头像应已绑定到账号：%v", got)
	}
	accountRowID, pointer := f.accountAvatar(f.accountID)
	if pointer != avatarID {
		t.Fatalf("库中头像指针应为 %s，得到 %q", avatarID, pointer)
	}

	// 触发器先清空指针，删除才不会被 accounts_avatar_asset_fk 挡住：这里报错即说明触发器没生效。
	if _, err := f.pool.Exec(ctx, `DELETE FROM assets WHERE id = $1`, avatarID); err != nil {
		t.Fatalf("删除头像资产应被触发器放行：%v", err)
	}

	afterRowID, afterPointer := f.accountAvatar(f.accountID)
	if afterPointer != "" {
		t.Errorf("删除资产后 accounts.avatar_asset_id 应为 NULL，得到 %q", afterPointer)
	}
	if afterRowID != accountRowID {
		t.Errorf("删除资产不应改动 accounts.id：删除前 %s，删除后 %s", accountRowID, afterRowID)
	}
}

// 复合外键保证头像与账号同属：拿别的账号的 avatar 资产 id 直接改库必须被 23503 拒绝。
// 单列外键只能保证「资产存在」，那样第二个账号的头像就能挂到第一个账号上。
func TestAvatarFKCrossAccountReferenceRejected(t *testing.T) {
	f := newAvatarFKFixture(t)
	ctx := context.Background()

	other := f.registerWeb(uniqueEmail(), "correct horse battery")
	otherAccountID, _ := other.data()["account"].(map[string]any)["id"].(string)
	foreignAvatarID := f.insertAvatarAsset(otherAccountID)

	// 放在事务里执行再回滚：失败语句只废掉这个事务，不会拖垮连接，也不会让测试进程崩掉。
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `UPDATE accounts SET avatar_asset_id = $1 WHERE id = $2`, foreignAvatarID, f.accountID)
	if err == nil {
		t.Fatalf("跨账号头像引用应被 %s 拒绝", avatarFKConstraint)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("应收到 PostgreSQL 错误，得到 %v", err)
	}
	if pgErr.Code != "23503" {
		t.Fatalf("SQLSTATE 应为 23503（foreign_key_violation），得到 %s：%v", pgErr.Code, err)
	}
	if name := pgcore.ConstraintViolation(err); name != avatarFKConstraint {
		t.Errorf("应违反 %s，得到 %q", avatarFKConstraint, name)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("回滚：%v", err)
	}

	// 被拒的更新没有留下痕迹。
	if _, pointer := f.accountAvatar(f.accountID); pointer != "" {
		t.Errorf("被拒的引用不应写入 accounts.avatar_asset_id，得到 %q", pointer)
	}
	// 本账号自己的头像资产仍可绑定：拒绝来自「跨账号」，不是「不能用头像」。
	expectStatus(t, f.bindAvatar(f.insertAvatarAsset(f.accountID)), http.StatusOK, "")
}
