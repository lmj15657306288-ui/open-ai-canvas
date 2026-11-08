#!/bin/sh
# 回滚到 v1.8.2（schema 91），撤销「商品视频」这次上线。
#
# 用法：sh /root/yc-20261108src/rollback.sh
#
# 为什么不能只切镜像：新版本把 schema 推到 93（92 drama_reference_controls、
# 93 ecommerce_video_replication），v1.8.2 期望 91，只切镜像会 ready:false。
# 所以必须同时还原数据库。
set -eu

BACKUP_DIR=${BACKUP_DIR:-/root/canvas-pre-ecommerce-20261108-124830}
COMPOSE_DIR=${COMPOSE_DIR:-/opt/open-ai-canvas-src}
COMPOSE_FILE="$COMPOSE_DIR/docker-compose.server.yml"
OLD_BACKEND=${OLD_BACKEND:-open-ai-canvas-backend:v1.8.2-cd2512eb}
OLD_WEB=${OLD_WEB:-open-ai-canvas-web:v1.8.2-cd2512eb}
STAMP=$(date +%Y%m%d-%H%M%S)

echo "== 1. 校验前置 =="
test -s "$BACKUP_DIR/db.sql.gz" || { echo "备份缺失：$BACKUP_DIR/db.sql.gz"; exit 1; }
docker image inspect "$OLD_BACKEND" >/dev/null 2>&1 || { echo "旧后端镜像不存在：$OLD_BACKEND"; exit 1; }
docker image inspect "$OLD_WEB" >/dev/null 2>&1 || { echo "旧前端镜像不存在：$OLD_WEB"; exit 1; }
echo "备份：$BACKUP_DIR"
echo "旧镜像：$OLD_BACKEND / $OLD_WEB"

echo "== 2. 记录当前状态（便于再前进） =="
docker inspect -f '{{.Name}} {{.Config.Image}}' open-ai-canvas-backend open-ai-canvas-web || true

echo "== 3. 停服务 =="
docker compose -f "$COMPOSE_FILE" stop backend web

echo "== 4. 保留新库并重建空库 =="
docker exec open-ai-canvas-postgres psql -U open_ai_canvas -d postgres -c \
  "alter database open_ai_canvas rename to open_ai_canvas_bak_$STAMP"
docker exec open-ai-canvas-postgres psql -U open_ai_canvas -d postgres -c \
  "create database open_ai_canvas owner open_ai_canvas"

echo "== 5. 还原 dump =="
zcat "$BACKUP_DIR/db.sql.gz" | docker exec -i open-ai-canvas-postgres psql -q -U open_ai_canvas -d open_ai_canvas

echo "== 6. 切回旧镜像 =="
docker tag "$OLD_BACKEND" open-ai-canvas-backend:server
docker tag "$OLD_WEB" open-ai-canvas-web:server
docker compose -f "$COMPOSE_FILE" up -d --no-build backend web

echo "== 7. 等待健康 =="
i=0
while [ "$i" -lt 15 ]; do
  i=$((i+1))
  B=$(docker inspect -f '{{.State.Health.Status}}' open-ai-canvas-backend 2>/dev/null || echo unknown)
  W=$(docker inspect -f '{{.State.Health.Status}}' open-ai-canvas-web 2>/dev/null || echo unknown)
  echo "try$i backend=$B web=$W"
  if [ "$B" = "healthy" ] && [ "$W" = "healthy" ]; then break; fi
  sleep 6
done

echo "== 8. 校验 =="
curl -s -m 10 http://127.0.0.1:5173/api/health/ready || true
echo
echo "回滚完成。旧库保留为 open_ai_canvas_bak_$STAMP（确认无误后可手工 drop）。"
