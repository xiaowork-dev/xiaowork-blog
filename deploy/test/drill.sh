#!/usr/bin/env bash
# Deployment drill helper (P8-A). Runs INSIDE an isolated Docker-in-Docker container that plays the role of a
# fresh Linux server; it never touches the developer's own Docker volumes. See deploy/test/README.md.
#
#   drill.sh prepare           tools, base images, Git origin with a snapshot of /src, simulated NPM + mail
#   drill.sh configure         fill deploy/.env for the drill (https://localhost:18443, mail catcher)
#   drill.sh scenario NAME     push a scenario commit to the drill origin:
#                                migration      – adds an additive goose migration (schema 8)
#                                broken-api     – api image whose entrypoint exits (health check fails)
#                                fix-api        – reverts broken-api
#                                bad-migration  – adds a migration with invalid SQL
#                                drop-bad-migration – removes it again
#   drill.sh sync              copy the current /src tree into a new origin commit (the server updates via update.sh)
#   drill.sh counts            row counts used to compare before/after
set -Eeuo pipefail
SERVER=/opt/xiaowork-blog          # the "server" checkout used by the deploy scripts
ORIGIN=/work/origin.git            # bare repository playing GitHub
DEV=/work/dev                      # developer clone used to push scenario commits
DRILL=/drill                       # host folder: base-images.tar, outputs

# snapshot DIR: copy the working tree from /src without dependencies, build output, local data or .git.
snapshot() {
  tar -C /src --exclude=./.git --exclude=./.data --exclude="*/node_modules" --exclude="*/.output" --exclude="*/.nuxt"       --exclude=./deploy/.env -cf - . | tar -C "$1" -xf -
}

prepare() {
  apk add --no-cache -q bash git openssl coreutils findutils tar curl >/dev/null
  git config --global user.email drill@example.test
  git config --global user.name "deploy drill"
  git config --global init.defaultBranch main
  git config --global --add safe.directory '*'
  if [ -f "$DRILL/base-images.tar" ]; then docker load -q -i "$DRILL/base-images.tar"; fi

  # Snapshot of the working tree (no dependencies, build output, local data or .git).
  rm -rf /work && mkdir -p /work/seed
  tar -C /src --exclude=./.git --exclude=./.data --exclude='*/node_modules' --exclude='*/.output' --exclude='*/.nuxt' \
      --exclude=./deploy/.env -cf - . | tar -C /work/seed -xf -
  find /work/seed -name "*.sh" -exec sed -i 's/\r$//' {} + -exec chmod +x {} +
  git -C /work/seed init -q && git -C /work/seed add -A && git -C /work/seed commit -qm "drill: v1 snapshot"
  git clone -q --bare /work/seed "$ORIGIN"
  git clone -q "$ORIGIN" "$DEV"
  rm -rf "$SERVER" && git clone -q "$ORIGIN" "$SERVER"

  # Simulated Nginx Proxy Manager: its own network, self-signed TLS for localhost, the documented routes.
  docker network inspect npm_proxy >/dev/null 2>&1 || docker network create npm_proxy >/dev/null
  mkdir -p /work/certs
  openssl req -x509 -newkey rsa:2048 -nodes -days 7 -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
    -keyout /work/certs/site.key.pem -out /work/certs/site.pem 2>/dev/null
  docker rm -f npm-sim mail-sim >/dev/null 2>&1 || true
  docker run -d --name npm-sim --network npm_proxy -p 443:443 \
    -v "$SERVER/deploy/nginx/npm-equivalent.conf:/etc/nginx/conf.d/default.conf:ro" \
    -v /work/certs:/etc/nginx/certs:ro nginx:1.30.5-alpine >/dev/null
  # Mail catcher standing in for the SMTP provider (reachable from api over the proxy network).
  docker run -d --name mail-sim --network npm_proxy -p 8025:8025 axllent/mailpit:v1.32.0 >/dev/null
  echo "prepared: origin=$ORIGIN server=$SERVER"
}

configure() {
  local env="$SERVER/deploy/.env"
  [ -f "$env" ] || { echo "run deploy/install.sh once first (it creates deploy/.env)"; exit 1; }
  sed -i -e 's|^PUBLIC_BASE_URL=.*|PUBLIC_BASE_URL=https://localhost:18443|' \
         -e 's|^DATA_DIR=.*|DATA_DIR=/srv/xiaowork-blog|' \
         -e 's|^SMTP_HOST=.*|SMTP_HOST=mail-sim|' -e 's|^SMTP_PORT=.*|SMTP_PORT=1025|' -e 's|^SMTP_TLS=.*|SMTP_TLS=none|' \
         -e 's|^PROXY_NETWORK=.*|PROXY_NETWORK=npm_proxy|' \
         -e 's|^# *GOPROXY=.*|GOPROXY=https://goproxy.cn,direct|' "$env"
  echo "configured $env (drill values; SMTP_TLS=none is accepted only because the site is https://localhost)"
}

push() { git -C "$DEV" add -A && git -C "$DEV" commit -qm "$1" && git -C "$DEV" push -q origin main && git -C "$DEV" rev-parse --short=12 HEAD; }

scenario() {
  git -C "$DEV" pull -q origin main
  case "$1" in
    migration)
      cat > "$DEV/apps/api/db/migrations/00008_drill_marker.sql" <<'SQL'
-- Drill-only additive migration: proves update.sh runs migrations and that rollback keeps working.
-- +goose Up
CREATE TABLE drill_marker (id integer PRIMARY KEY, note text NOT NULL);
INSERT INTO drill_marker VALUES (1, 'added by the P8-A update drill');
-- +goose Down
DROP TABLE drill_marker;
SQL
      push "drill: additive migration 00008";;
    broken-api)
      sed -i 's|^ENTRYPOINT \["/usr/local/bin/blog-api"\]|ENTRYPOINT ["/bin/false"]|' "$DEV/apps/api/Dockerfile"
      push "drill: api that cannot start";;
    fix-api)
      sed -i 's|^ENTRYPOINT \["/bin/false"\]|ENTRYPOINT ["/usr/local/bin/blog-api"]|' "$DEV/apps/api/Dockerfile"
      push "drill: api fixed";;
    bad-migration)
      printf -- '-- +goose Up\nCREATE TABLE broken (;\n-- +goose Down\nSELECT 1;\n' > "$DEV/apps/api/db/migrations/00009_drill_broken.sql"
      push "drill: invalid migration";;
    drop-bad-migration)
      git -C "$DEV" rm -q apps/api/db/migrations/00009_drill_broken.sql
      push "drill: remove invalid migration";;
    *) echo "unknown scenario $1"; exit 2;;
  esac
}

sync() {
  git -C "$DEV" pull -q origin main
  snapshot "$DEV"
  find "$DEV" -name "*.sh" -not -path "*/.git/*" -exec sed -i "s/\r$//" {} + -exec chmod +x {} +
  if [ -n "$(git -C "$DEV" status --porcelain)" ]; then push "drill: sync working tree"; else echo "nothing to sync"; fi
}

counts() {
  docker compose -p xiaowork-blog exec -T db sh -c 'psql -tA -F" " -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT 'articles', count(*) FROM articles
UNION ALL SELECT 'published', count(*) FROM articles WHERE status = 'PUBLISHED'
UNION ALL SELECT 'users', count(*) FROM users
UNION ALL SELECT 'comments', count(*) FROM comments
UNION ALL SELECT 'media_ready', count(*) FROM media_assets WHERE state = 'READY'
UNION ALL SELECT 'social_links', count(*) FROM site_social_links
UNION ALL SELECT 'static_pages', count(*) FROM static_pages
UNION ALL SELECT 'schema', max(version_id) FROM goose_db_version WHERE is_applied;
SELECT 'site', site_title || ' | ' || profile_bio FROM site_settings;
SQL
  echo "media_files $(find /srv/xiaowork-blog/media -type f | wc -l)"
  echo "media_sha $(find /srv/xiaowork-blog/media -type f -exec sha256sum {} + | awk '{print $1}' | sort | sha256sum | cut -c1-16)"
}

case "${1:-}" in
  prepare) prepare;;
  configure) configure;;
  scenario) scenario "${2:?scenario name}";;
  sync) sync;;
  counts) counts;;
  *) sed -n '2,13p' "$0"; exit 2;;
esac
