#!/usr/bin/env bash
# Deployment drill helper (P8-A, release drills since P8-B2). Runs INSIDE an isolated Docker-in-Docker container
# that plays the role of a fresh Linux server; it never touches the developer's own Docker volumes and never
# talks to the real GitHub. See deploy/test/README.md.
#
#   drill.sh prepare           tools, base images, Git origin with a snapshot of /src, simulated proxy + mail
#                              DRILL_MODE=docker-network (default): nginx as Nginx Proxy Manager on network npm_proxy
#                              DRILL_MODE=host-loopback: nginx on the host network as 1Panel's OpenResty
#                              SNAPSHOT_SRC=/path: snapshot another tree (e.g. an older release) instead of /src
#   drill.sh configure         fill deploy/.env for the drill (https://localhost:18443, mail catcher)
#   drill.sh scenario NAME     push a scenario commit to the drill origin:
#                                migration      – adds an additive goose migration (schema 8)
#                                broken-api     – api image whose entrypoint exits (health check fails)
#                                fix-api        – reverts broken-api
#                                bad-migration  – adds a migration with invalid SQL
#                                drop-bad-migration – removes it again
#                                unreleased     – a commit on main that is not part of any release
#   drill.sh sync              copy the current /src tree into a new origin commit (the server updates via update.sh)
#   drill.sh counts            row counts used to compare before/after
#   drill.sh mock start|stop|mode ok|403|500|badjson   mock GitHub Releases API on http://127.0.0.1:9999
#   drill.sh release TAG [stable|prerelease|draft]      tag the origin main head and publish it in the mock
#   drill.sh move-tag TAG      force-move a published tag to another commit (tampering test)
set -Eeuo pipefail
SERVER=/opt/xiaowork-blog          # the "server" checkout used by the deploy scripts
ORIGIN=/work/origin.git            # bare repository playing GitHub
DEV=/work/dev                      # developer clone used to push scenario commits and tags
DRILL=/drill                       # host folder: base-images.tar, outputs
DRILL_MODE="${DRILL_MODE:-docker-network}"
SNAPSHOT_SRC="${SNAPSHOT_SRC:-/src}"
MOCK=/work/mock                    # mock API state: mode, releases.lines

# snapshot DIR: copy the working tree from /src without dependencies, build output, local data or .git.
snapshot() {
  tar -C "$SNAPSHOT_SRC" --exclude=./.git --exclude=./.data --exclude="*/node_modules" --exclude="*/.output" --exclude="*/.nuxt" \
      --exclude=./deploy/.env -cf - . | tar -C "$1" -xf -
}

prepare() {
  apk add --no-cache -q bash git openssl coreutils findutils tar curl iproute2 util-linux-misc >/dev/null
  git config --global user.email drill@example.test
  git config --global user.name "deploy drill"
  git config --global init.defaultBranch main
  git config --global --add safe.directory '*'
  if [ -f "$DRILL/base-images.tar" ]; then docker load -q -i "$DRILL/base-images.tar"; fi

  rm -rf /work && mkdir -p /work/seed "$MOCK"
  snapshot /work/seed
  find /work/seed -name "*.sh" -exec sed -i 's/\r$//' {} + -exec chmod +x {} +
  git -C /work/seed init -q && git -C /work/seed add -A && git -C /work/seed commit -qm "drill: v1 snapshot"
  git clone -q --bare /work/seed "$ORIGIN"
  git clone -q "$ORIGIN" "$DEV"
  rm -rf "$SERVER" && git clone -q "$ORIGIN" "$SERVER"

  mkdir -p /work/certs
  openssl req -x509 -newkey rsa:2048 -nodes -days 7 -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
    -keyout /work/certs/site.key.pem -out /work/certs/site.pem 2>/dev/null
  docker rm -f npm-sim openresty-sim mail-sim >/dev/null 2>&1 || true
  if [ "$DRILL_MODE" = host-loopback ]; then
    # Simulated 1Panel OpenResty: a container on the HOST network that owns 80/443 and forwards to 127.0.0.1.
    docker run -d --name openresty-sim --network host \
      -v "/src/deploy/nginx/openresty-host-loopback.conf:/etc/nginx/conf.d/default.conf:ro" \
      -v /work/certs:/etc/nginx/certs:ro nginx:1.30.5-alpine >/dev/null
    # Mail catcher on the host network (1025/8025); api reaches it at its own network gateway (first address of APP_SUBNET). A port
    # published from the default bridge would not do: Docker drops traffic forwarded between bridge networks.
    # Reverse DNS of the client address would delay the SMTP greeting past the API timeout on the host network.
    docker run -d --name mail-sim --network host -e MP_SMTP_DISABLE_RDNS=true axllent/mailpit:v1.32.0 >/dev/null
  else
    # Simulated Nginx Proxy Manager: its own network, self-signed TLS for localhost, the documented routes.
    docker network inspect npm_proxy >/dev/null 2>&1 || docker network create npm_proxy >/dev/null
    docker run -d --name npm-sim --network npm_proxy -p 443:443 \
      -v "/src/deploy/nginx/npm-equivalent.conf:/etc/nginx/conf.d/default.conf:ro" \
      -v /work/certs:/etc/nginx/certs:ro nginx:1.30.5-alpine >/dev/null
    # Mail catcher standing in for the SMTP provider (reachable from api over the proxy network).
    docker run -d --name mail-sim --network npm_proxy -p 8025:8025 axllent/mailpit:v1.32.0 >/dev/null
  fi
  echo "prepared ($DRILL_MODE): origin=$ORIGIN server=$SERVER snapshot=$SNAPSHOT_SRC"
}

# app_gateway ENV_FILE: first address of APP_SUBNET (the host side of the blog's app network).
app_gateway() {
  local subnet
  subnet="$(sed -n "s/^APP_SUBNET=['\"]\{0,1\}\([0-9.]*\)\/.*/\1/p" "$1" | head -n 1)"
  echo "${subnet%.*}.$(( ${subnet##*.} + 1 ))"
}

configure() {
  local env="$SERVER/deploy/.env"
  [ -f "$env" ] || { echo "run deploy/install.sh once first (it creates deploy/.env)"; exit 1; }
  sed -i -e 's|^PUBLIC_BASE_URL=.*|PUBLIC_BASE_URL=https://localhost:18443|' \
         -e 's|^DATA_DIR=.*|DATA_DIR=/srv/xiaowork-blog|' \
         -e 's|^SMTP_PORT=.*|SMTP_PORT=1025|' -e 's|^SMTP_TLS=.*|SMTP_TLS=none|' \
         -e 's|^# *GOPROXY=.*|GOPROXY=https://goproxy.cn,direct|' "$env"
  if [ "$DRILL_MODE" = host-loopback ]; then
    sed -i -e 's|^PROXY_MODE=.*|PROXY_MODE=host-loopback|' -e "s|^SMTP_HOST=.*|SMTP_HOST=$(app_gateway "$env")|" "$env"
    grep -q '^PROXY_MODE=' "$env" || echo 'PROXY_MODE=host-loopback' >> "$env"
  else
    # An older deploy/.env (before P8-B6) has no PROXY_MODE line and is left that way on purpose.
    sed -i -e 's|^SMTP_HOST=.*|SMTP_HOST=mail-sim|' -e 's|^PROXY_NETWORK=.*|PROXY_NETWORK=npm_proxy|' "$env"
  fi
  grep -q '^GOPROXY=' "$env" || echo 'GOPROXY=https://goproxy.cn,direct' >> "$env"
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
INSERT INTO drill_marker VALUES (1, 'added by the update drill');
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
    unreleased)
      date -u +%FT%TZ >> "$DEV/docs/drill-unreleased.txt"
      push "drill: unreleased work on main";;
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
UNION ALL SELECT 'static_pages', count(*) FROM static_pages;
SELECT 'site', site_title || ' | ' || profile_bio FROM site_settings;
SQL
  echo "media_files $(find /srv/xiaowork-blog/media -type f | wc -l)"
  echo "media_sha $(find /srv/xiaowork-blog/media -type f -exec sha256sum {} + | awk '{print $1}' | sort | sha256sum | cut -c1-16)"
}

# Mock of GET /repos/xwb/blog/releases (the only GitHub endpoint manage.sh uses). The response is built from
# $MOCK/releases.lines (one JSON object per line, newest last); $MOCK/mode simulates failures.
mock() {
  case "$1" in
    start)
      mkdir -p "$MOCK"; [ -f "$MOCK/mode" ] || echo ok > "$MOCK/mode"; touch "$MOCK/releases.lines"
      cat > "$MOCK/server.mjs" <<'JS'
import http from 'node:http'
import fs from 'node:fs'
http.createServer((req, res) => {
  const mode = fs.readFileSync('/mock/mode', 'utf8').trim()
  const json = (code, body) => { res.writeHead(code, { 'content-type': 'application/json' }); res.end(body) }
  if (mode === '403') return json(403, '{"message":"API rate limit exceeded"}')
  if (mode === '500') return json(500, '{"message":"Server Error"}')
  if (mode === 'badjson') return json(200, '<html>maintenance</html>')
  if (!/^\/repos\/xwb\/blog\/releases(\?|$)/.test(req.url)) return json(404, '{"message":"Not Found"}')
  const lines = fs.readFileSync('/mock/releases.lines', 'utf8').split('\n').filter(Boolean).reverse()
  json(200, '[' + lines.join(',') + ']')
}).listen(9999)
JS
      docker rm -f mock-github >/dev/null 2>&1 || true
      docker run -d --name mock-github -p 127.0.0.1:9999:9999 -v "$MOCK:/mock:ro" node:24.22.1-alpine node /mock/server.mjs >/dev/null
      echo "mock GitHub API on http://127.0.0.1:9999 (GITHUB_REPO=xwb/blog)";;
    stop) docker stop mock-github >/dev/null && echo "mock stopped";;
    mode) echo "${2:?mode}" > "$MOCK/mode"; echo "mock mode ${2}";;
    *) echo "mock start|stop|mode MODE"; exit 2;;
  esac
}

release() {
  local tag="${1:?tag}" kind="${2:-stable}" draft=false pre=false
  case "$kind" in draft) draft=true;; prerelease) pre=true;; stable) ;; *) echo "kind?"; exit 2;; esac
  git -C "$DEV" pull -q origin main
  git -C "$DEV" tag -a "$tag" -m "release $tag"
  git -C "$DEV" push -q origin "$tag"
  printf '{"tag_name":"%s","name":"XiaoWork Blog %s","draft":%s,"prerelease":%s,"published_at":"%s","body":"## %s\\n\\n- drill release notes for %s\\n- commit %s"}\n' \
    "$tag" "$tag" "$draft" "$pre" "$(date -u +%FT%TZ)" "$tag" "$tag" "$(git -C "$DEV" rev-parse --short=12 HEAD)" >> "$MOCK/releases.lines"
  echo "released $tag ($kind) at $(git -C "$DEV" rev-parse --short=12 "$tag^{commit}")"
}

move_tag() {
  local tag="${1:?tag}" other
  other="$(git -C "$DEV" rev-parse "$tag^{commit}~1")"
  git -C "$DEV" tag -f -a "$tag" -m "moved $tag" "$other" >/dev/null
  git -C "$DEV" push -q -f origin "$tag"
  echo "moved $tag to $(git -C "$DEV" rev-parse --short=12 "$tag^{commit}") in origin"
}

case "${1:-}" in
  prepare) prepare;;
  configure) configure;;
  scenario) scenario "${2:?scenario name}";;
  sync) sync;;
  counts) counts;;
  mock) shift; mock "$@";;
  release) shift; release "$@";;
  move-tag) move_tag "${2:-}";;
  *) sed -n '2,19p' "$0"; exit 2;;
esac
