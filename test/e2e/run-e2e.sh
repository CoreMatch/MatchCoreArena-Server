#!/usr/bin/env bash
# 一键启动 MariaDB 容器 → 等待就绪 → 跑 e2e 测试 → 清理
# 用法: cd MatchCoreArena-Server && bash test/e2e/run-e2e.sh
# 不引入 make 或其他构建系统，仅依赖 docker 与 go toolchain。

set -euo pipefail

CONTAINER_NAME="mca-e2e-mariadb"

cleanup() {
  local exit_code=$?
  echo "→ 清理容器 ${CONTAINER_NAME}..."
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  exit "${exit_code}"
}
trap cleanup EXIT

# 避免上一次异常退出留下脏容器
docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

echo "→ 启动 MariaDB 容器（mariadb:11，端口 13306）..."
docker run -d --name "${CONTAINER_NAME}" -p 13306:3306 \
  -e MARIADB_ROOT_PASSWORD=root \
  -e MARIADB_USER=mca \
  -e MARIADB_PASSWORD='mca-e2e-pass' \
  -e MARIADB_DATABASE=mca_e2e \
  mariadb:11

echo "→ 等待 MariaDB 就绪（最多 60s）..."
for i in $(seq 1 60); do
  if docker exec "${CONTAINER_NAME}" mariadb \
       -umca -pmca-e2e-pass -e "SELECT 1" mca_e2e >/dev/null 2>&1; then
    echo "✓ MariaDB 就绪（用时 ${i}s）"
    break
  fi
  if [ "${i}" -eq 60 ]; then
    echo "✗ MariaDB 60s 内未就绪，放弃" >&2
    exit 1
  fi
  sleep 1
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "→ 运行 e2e 测试..."
cd "${PROJECT_ROOT}"
go test -tags e2e -race -count=1 -v ./test/e2e/...
