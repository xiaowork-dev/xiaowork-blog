#!/usr/bin/env bash
# Read-only production host precheck (P8-B1). Changes nothing: it creates no file, directory, container,
# network or volume, pulls no image and does not enter any container.
#
#   sudo bash deploy/precheck.sh [--npm-network NAME] [--data-dir DIR] [--domain blog.example.com]
#                                [--smtp smtp.example.com:587] [--app-subnet 172.30.240.0/24] > precheck.txt
#
# It prints no secrets: container environments, `docker inspect` dumps and .env files are never shown.
# Review precheck.txt before sharing it (it lists container names, networks, ports and directories).
set -uo pipefail

NPM_NETWORK=""; DATA_DIR=/srv/xiaowork-blog; DOMAIN=""; SMTP=""; APP_SUBNET=172.30.240.0/24
while [ $# -gt 0 ]; do
  case "$1" in
    --npm-network) NPM_NETWORK="${2:-}"; shift 2;;
    --data-dir) DATA_DIR="${2:-}"; shift 2;;
    --domain) DOMAIN="${2:-}"; shift 2;;
    --smtp) SMTP="${2:-}"; shift 2;;
    --app-subnet) APP_SUBNET="${2:-}"; shift 2;;
    -h|--help) sed -n '2,9p' "$0"; exit 0;;
    *) echo "unknown option $1" >&2; exit 2;;
  esac
done

section() { printf '\n==== %s ====\n' "$*"; }
note() { printf '  -> %s\n' "$*"; }
have() { command -v "$1" >/dev/null 2>&1; }
run() { printf '$ %s\n' "$*"; "$@" 2>&1 | sed 's/^/  /'; }

# ip2int / overlap: IPv4 CIDR arithmetic for the subnet-collision check.
ip2int() { local IFS=.; set -- $1; echo $(( ($1 << 24) + ($2 << 16) + ($3 << 8) + $4 )); }
overlap() {  # overlap A/len B/len -> exit 0 when the ranges intersect
  local a="${1%/*}" al="${1#*/}" b="${2%/*}" bl="${2#*/}" as ae bs be
  case "$a$b" in *:*) return 1;; esac
  as=$(ip2int "$a"); bs=$(ip2int "$b")
  as=$(( as & ~((1 << (32 - al)) - 1) & 0xFFFFFFFF )); ae=$(( as + (1 << (32 - al)) - 1 ))
  bs=$(( bs & ~((1 << (32 - bl)) - 1) & 0xFFFFFFFF )); be=$(( bs + (1 << (32 - bl)) - 1 ))
  [ "$as" -le "$be" ] && [ "$bs" -le "$ae" ]
}

echo "XiaoWork Blog production precheck — $(date -u +%FT%TZ) — read-only"
[ "$(id -u)" = 0 ] || note "不是 root：部分信息（端口对应进程、防火墙）可能看不到"

section "1. 系统"
[ -r /etc/os-release ] && grep -E '^(PRETTY_NAME|VERSION_ID)=' /etc/os-release | sed 's/^/  /'
run uname -srm
have timedatectl && timedatectl show -p NTPSynchronized -p Timezone 2>/dev/null | sed 's/^/  /'
run uptime

section "2. 资源"
note "CPU 核数：$(nproc 2>/dev/null || echo ?)"
have free && run free -m
run df -hP / /var/lib/docker "$(dirname "$DATA_DIR")"
run df -iP /

section "3. Docker"
if have docker && docker info >/dev/null 2>&1; then
  note "Engine $(docker version -f '{{.Server.Version}}' 2>/dev/null)，Compose $(docker compose version --short 2>/dev/null || echo 缺少插件)"
  docker info -f '  Root dir: {{.DockerRootDir}}
  Storage: {{.Driver}}  Cgroup: {{.CgroupDriver}} v{{.CgroupVersion}}
  Security: {{.SecurityOptions}}
  Containers: {{.Containers}} (running {{.ContainersRunning}})  Images: {{.Images}}' 2>/dev/null
  if [ -r /etc/docker/daemon.json ]; then
    note "/etc/docker/daemon.json 中的网络相关设置："
    grep -E '"(userland-proxy|iptables|ip6tables|ipv6|default-address-pools|bip|live-restore|log-driver)"' /etc/docker/daemon.json | sed 's/^/    /'
  fi
  docker info -f '{{.SecurityOptions}}' 2>/dev/null | grep -q rootless && note "rootless Docker：NPM 可能拿不到真实客户端 IP（风险 R2）"
else
  note "docker 不可用或当前用户无权访问 Docker 守护进程"
fi

section "4. 现有容器（名称 / 镜像 / 状态 / 端口；不显示环境变量）"
docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}' 2>/dev/null | sed 's/^/  /'

section "5. 主机监听端口"
if have ss; then
  ss -Htlnp 2>/dev/null | awk '{print "  " $4 "\t" $6}' | sort -u
else
  note "没有 ss 命令"
fi
for p in 80 443 81; do
  ss -Htln "sport = :$p" 2>/dev/null | grep -q . && note "端口 $p 已被占用（期望由 NPM 占用 80/443，81 为 NPM 管理界面）"
done

section "6. Docker 网络与网段冲突（APP_SUBNET=$APP_SUBNET）"
for n in $(docker network ls -q 2>/dev/null); do
  line="$(docker network inspect -f '{{.Name}} {{.Driver}} {{range .IPAM.Config}}{{.Subnet}} {{end}}' "$n" 2>/dev/null)"
  echo "  $line"
  for s in ${line#* * }; do
    case "$s" in */*) overlap "$s" "$APP_SUBNET" && note "冲突：网络 ${line%% *} 的 $s 与 APP_SUBNET 重叠，需要换一个 APP_SUBNET";; esac
  done
done
have ip && ip -4 route 2>/dev/null | awk '{print $1}' | grep / | while read -r r; do
  overlap "$r" "$APP_SUBNET" && note "冲突：主机路由 $r 与 APP_SUBNET 重叠"
done

section "7. Nginx Proxy Manager"
npm_ids="$(docker ps -a --format '{{.ID}} {{.Image}}' 2>/dev/null | awk 'tolower($2) ~ /nginx-proxy-manager/ {print $1}')"
if [ -z "$npm_ids" ]; then
  note "没有找到镜像名包含 nginx-proxy-manager 的容器；请人工确认 NPM 的容器名"
fi
for id in $npm_ids; do
  docker inspect -f '  容器 {{.Name}}  镜像 {{.Config.Image}}  状态 {{.State.Status}}  重启策略 {{.HostConfig.RestartPolicy.Name}}
  Compose 项目 {{index .Config.Labels "com.docker.compose.project"}}  工作目录 {{index .Config.Labels "com.docker.compose.project.working_dir"}}
  网络：{{range $k, $v := .NetworkSettings.Networks}}{{$k}}={{$v.IPAddress}} {{end}}
  端口：{{range $p, $b := .NetworkSettings.Ports}}{{$p}}->{{range $b}}{{.HostIp}}:{{.HostPort}} {{end}} {{end}}
  挂载：{{range .Mounts}}{{.Source}}:{{.Destination}} {{end}}' "$id" 2>/dev/null
  nets="$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' "$id" 2>/dev/null)"
  case " $nets " in *" bridge "*) note "NPM 连接在默认 bridge 网络上：该网络没有容器名解析，需要按 P8-B1 文档 §4 方案 B 处理";; esac
done
if [ -n "$NPM_NETWORK" ]; then
  if docker network inspect "$NPM_NETWORK" >/dev/null 2>&1; then
    docker network inspect -f '  网络 {{.Name}}  驱动 {{.Driver}}  internal={{.Internal}}  网段 {{range .IPAM.Config}}{{.Subnet}}(网关 {{.Gateway}}) {{end}}
  成员：{{range .Containers}}{{.Name}}={{.IPv4Address}} {{end}}' "$NPM_NETWORK"
    case "$NPM_NETWORK" in bridge|host|none) note "不能使用预置网络 $NPM_NETWORK 作为 PROXY_NETWORK";; esac
  else
    note "网络 $NPM_NETWORK 不存在"
  fi
fi

section "8. 1Panel / 其他面板"
have systemctl && systemctl list-units --type=service --no-legend 2>/dev/null | grep -iE '1panel|openresty|nginx|apache|caddy|bt' | sed 's/^/  /'
docker ps --format '{{.Names}} {{.Image}}' 2>/dev/null | grep -iE '1panel|openresty' | sed 's/^/  容器 /'
[ -d /opt/1panel ] && note "存在 /opt/1panel"

section "9. 博客已有资源（应为空）"
docker ps -a --format '{{.Names}}' 2>/dev/null | grep '^xiaowork-blog' | sed 's/^/  容器 /'
docker volume ls -q 2>/dev/null | grep '^xiaowork-blog' | sed 's/^/  数据卷 /'
docker image ls --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep '^xiaowork-blog' | sed 's/^/  镜像 /'
docker network ls --format '{{.Name}}' 2>/dev/null | grep '^xiaowork-blog' | sed 's/^/  网络 /'
for d in /opt/xiaowork-blog "$DATA_DIR"; do
  if [ -e "$d" ]; then note "$d 已存在：$(stat -c '%U:%G %a' "$d" 2>/dev/null)，内容 $(find "$d" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l) 项"
  else note "$d 不存在（安装时创建）"; fi
done
note "DATA_DIR 所在文件系统：$(df -PT "$(dirname "$DATA_DIR")" 2>/dev/null | awk 'NR==2 {print $1, $2, "挂载于", $7}')"

section "10. 防火墙"
have ufw && run ufw status
have firewall-cmd && run firewall-cmd --state
have nft && note "nftables 规则数：$(nft list ruleset 2>/dev/null | grep -c 'accept\|drop' || true)"

section "11. 外部连通性（只发起只读请求）"
probe() {  # any HTTP status means the host is reachable; 000 = DNS, TLS or connection failure
  local code; code="$(curl -s -o /dev/null -m 10 -w '%{http_code}' "$1" 2>/dev/null)"
  if [ "${code:-000}" = 000 ]; then note "$1 -> 无法访问（DNS / TLS / 连接失败）"; else note "$1 -> HTTP $code"; fi
}
if have curl; then
  for u in https://github.com https://proxy.golang.org https://goproxy.cn https://registry.npmjs.org https://registry.npmmirror.com https://registry-1.docker.io/v2/; do probe "$u"; done
else
  note "没有 curl"
fi
tcp() { timeout 6 bash -c "exec 3<>/dev/tcp/$1/$2" 2>/dev/null && note "TCP $1:$2 可连接" || note "TCP $1:$2 无法连接"; }
if [ -n "$SMTP" ]; then tcp "${SMTP%:*}" "${SMTP##*:}"; fi
if [ -n "$DOMAIN" ]; then
  note "$DOMAIN 解析：$(getent ahosts "$DOMAIN" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ')"
  have ip && note "本机全局地址：$(ip -o addr show scope global 2>/dev/null | awk '{print $4}' | tr '\n' ' ')"
fi

section "12. 工具"
for c in git openssl sha256sum bash curl crontab rsync; do have "$c" && note "$c ✓" || note "$c 缺少"; done

echo
echo "预检结束：本脚本没有做任何修改。"
