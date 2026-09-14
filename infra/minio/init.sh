#!/bin/sh
# MinIO 开发桶初始化：建私有桶、配置直传 CORS、给暂存前缀加过期清理。
# 幂等：重复执行不会报错，compose up 每次都会跑一遍。
set -eu

BUCKET="${TRIPFOLIO_BUCKET:-tripfolio}"
ALIAS=local

echo "等待 MinIO 就绪..."
until mc alias set "$ALIAS" http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1; do
	sleep 1
done

# 桶保持私有：所有访问都走服务端签发的短期授权，不开放匿名读。
if mc ls "$ALIAS/$BUCKET" >/dev/null 2>&1; then
	echo "桶 $BUCKET 已存在"
else
	mc mb "$ALIAS/$BUCKET"
	echo "已创建桶 $BUCKET"
fi
mc anonymous set none "$ALIAS/$BUCKET" >/dev/null

# 直传 CORS 不在这里配置：MinIO 不实现按桶 CORS（PutBucketCors 返回 NotImplemented），
# 它按服务端全局配置生效，见 compose.yaml 里 minio 服务的 MINIO_API_CORS_ALLOW_ORIGIN。
# 生产的 OSS 支持按桶 CORS，在控制台配置：允许 PUT/GET/HEAD、暴露 ETag、来源为正式网页域。

# 暂存前缀 1 天过期：未确认的上传不会长期占用空间（数据库设计第 14 节）。
cat >/tmp/lifecycle.json <<'EOF'
{
  "Rules": [
    {
      "ID": "expire-staging",
      "Status": "Enabled",
      "Filter": { "Prefix": "staging/" },
      "Expiration": { "Days": 1 }
    }
  ]
}
EOF
if ilm_err=$(mc ilm import "$ALIAS/$BUCKET" </tmp/lifecycle.json 2>&1); then
	echo "已配置 staging/ 前缀 1 天过期清理"
else
	echo "错误：配置 staging/ 过期规则失败：$ilm_err" >&2
	exit 1
fi

echo "MinIO 初始化完成：桶 $BUCKET（私有）"
