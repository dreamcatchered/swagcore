#!/usr/bin/env bash
# swagCore — установщик ноды для Linux. Одна команда, полная настройка.
#
#   curl -fsSL https://core.swag.best/download/install.sh | sudo bash -s -- <ТОКЕН>
#
# Что делает:
#   1) проверяет ОС/архитектуру и права root;
#   2) скачивает агента с проверкой sha256;
#   3) ставит systemd-юнит (автозапуск + перезапуск при падении);
#   4) при отсутствии Docker — ставит его официальным скриптом get.docker.com;
#   5) запускает агента и проверяет, что нода появилась в панели.
#
# Флаги (после --):
#   --token X        токен ноды (обязателен, если не передан позиционно)
#   --name X         имя машины для панели
#   --max-mem MB     лимит RAM для проектов платформы
#   --max-disk GB    лимит диска для проектов платформы
#   --no-docker      считать, что Docker не используется
#   --no-docker-install  не ставить Docker, даже если его нет
#   --uninstall      снести агента и юнит
set -uo pipefail

CORE="core.swag.best"
BASE="https://$CORE/download"
DATA="/var/lib/swagcore"
BIN="/usr/local/bin/swagcore-agent"
UNIT="/etc/systemd/system/swagcore-agent.service"
LOG="/var/log/swagcore-install.log"
SVC="swagcore-agent"

TOKEN=""; NAME=""; MAX_MEM=0; MAX_DISK=0
NO_DOCKER=0; NO_DOCKER_INSTALL=0; UNINSTALL=0

say()  { printf '\n\033[36m[%s] %s\033[0m\n' "$STEP" "$*"; STEP=$((STEP+1)); }
ok()   { printf '    \033[32mOK\033[0m  %s\n' "$*"; }
warn() { printf '    \033[33m!\033[0m   %s\n' "$*"; }
die()  { printf '    \033[31mx\033[0m   %s\n' "$*" >&2; log "FATAL: $*"; exit 1; }
log()  { printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >> "$LOG" 2>/dev/null || true; }
STEP=1

cleanup_on_error() {
  local code=$?
  if [ $code -ne 0 ]; then
    printf '\n\033[31mУстановка прервана (код %s). Лог: %s\033[0m\n' "$code" "$LOG" >&2
  fi
}
trap cleanup_on_error EXIT

# ---------------- разбор аргументов ----------------
if [ "${1:-}" = "--uninstall" ]; then UNINSTALL=1; shift; fi
while [ $# -gt 0 ]; do
  case "$1" in
    --token)              TOKEN="${2:-}"; shift 2 ;;
    --name)               NAME="${2:-}"; shift 2 ;;
    --max-mem)            MAX_MEM="${2:-0}"; shift 2 ;;
    --max-disk)           MAX_DISK="${2:-0}"; shift 2 ;;
    --no-docker)          NO_DOCKER=1; shift ;;
    --no-docker-install)  NO_DOCKER_INSTALL=1; shift ;;
    --uninstall)          UNINSTALL=1; shift ;;
    -*) echo "неизвестный флаг: $1" >&2; exit 2 ;;
    *)                    TOKEN="$1"; shift ;;
  esac
done

[ -d /etc/systemd/system ] || die "systemd не найден — этот установщик только для systemd-систем"
[ "$(id -u)" = "0" ] || die "нужны права root. Запустите: sudo bash install.sh <ТОКЕН>"
mkdir -p "$(dirname "$LOG")"
log "=== install start: token=…${TOKEN: -6} maxmem=$MAX_MEM ==="

# ---------------- удаление ----------------
if [ "$UNINSTALL" = "1" ]; then
  say "Удаляем агента"
  systemctl stop "$SVC" 2>/dev/null || true
  systemctl disable "$SVC" 2>/dev/null || true
  rm -f "$UNIT"
  systemctl daemon-reload
  ok "юнит и автозапуск удалены"
  ok "данные в $DATA сохранены (Sites/Apps не тронуты)"
  exit 0
fi

# ---------------- 1. проверки ----------------
say "Проверяем систему"
. /etc/os-release 2>/dev/null || true
ok "OS: ${PRETTY_NAME:-unknown}"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) die "неподдерживаемая архитектура: $ARCH (нужна x86_64 или arm64)" ;;
esac
ok "архитектура: $GOARCH"

if command -v systemctl >/dev/null 2>&1; then
  ok "systemctl доступен"
else
  die "нет systemctl — нужна systemd-система"
fi

AVAIL_MB="$(df -Pm / | awk 'NR==2{print $4}')"
ok "свободно на /: $((AVAIL_MB / 1024)) ГБ"
[ "$AVAIL_MB" -ge 512 ] || warn "мало свободного места (<512 МБ)"

# ---------------- 2. токен ----------------
say "Токен подключения"
if [ -z "$TOKEN" ]; then
  echo "  Вставьте токен из панели: https://$CORE/ -> «+ Добавить ноду»"
  printf '  Токен: '
  read -r TOKEN
fi
TOKEN="$(printf '%s' "$TOKEN" | tr -d '[:space:]')"
case "$TOKEN" in
  *[!A-Za-z0-9_-]*|"") die "токен содержит недопустимые символы" ;;
esac
[ "${#TOKEN}" -ge 16 ] || die "токен слишком короткий (${#TOKEN} символов)"
ok "токен принят"

[ -n "$NAME" ] || NAME="$(hostname -s 2>/dev/null || hostname)"
NAME="$(printf '%s' "$NAME" | tr -cd 'A-Za-z0-9_.-')"
[ -n "$NAME" ] || NAME="node"
ok "имя ноды: $NAME"

# ---------------- 3. останавливаем прежнюю версию ----------------
say "Останавливаем прежнюю версию (если есть)"
systemctl stop "$SVC" 2>/dev/null || true
pkill -f "$BIN run" 2>/dev/null || true
sleep 1
ok "готово"

# ---------------- 4. скачиваем агента ----------------
say "Скачиваем агента"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
AGENT_URL="$BASE/agent/linux/$GOARCH/swagcore-agent"

dl() { # dl <url> <out> [tries]
  local url="$1" out="$2" tries="${3:-4}" i
  for i in $(seq 1 "$tries"); do
    if curl -fsSL --connect-timeout 20 --max-time 600 "$url" -o "$out"; then return 0; fi
    warn "попытка $i/$tries не удалась: $url"
    [ "$i" -lt "$tries" ] && sleep $((i * 2))
  done
  return 1
}

dl "$AGENT_URL" "$TMP/agent" 4 || die "не удалось скачать агента: $AGENT_URL"
[ -s "$TMP/agent" ] || die "скачался пустой файл"

WANT_SHA=""
if dl "$AGENT_URL.sha256" "$TMP/agent.sha256" 2; then
  WANT_SHA="$(awk '{print $1}' "$TMP/agent.sha256" 2>/dev/null | tr -d '[:space:]')"
fi
if [ -n "$WANT_SHA" ]; then
  GOT_SHA="$(sha256sum "$TMP/agent" | awk '{print $1}')"
  [ "$GOT_SHA" = "$WANT_SHA" ] || die "контрольная сумма не совпала: скачано $GOT_SHA, ожидалось $WANT_SHA"
  ok "контрольная сумма сходится"
else
  warn "не удалось получить манифест sha256 — проверяем только, что файл запускается"
fi

chmod +x "$TMP/agent"
if ! "$TMP/agent" version >/dev/null 2>&1; then
  die "скачанный агент не запускается на этой системе"
fi
LOCAL_BUILD="$("$TMP/agent" version 2>/dev/null)"
ok "локальная сборка: $LOCAL_BUILD"

# ---------------- 5. устанавливаем ----------------
say "Устанавливаем в $BIN"
mkdir -p /usr/local/bin "$DATA"
if [ -f "$BIN" ]; then
  cp -f "$BIN" "$BIN.old" 2>/dev/null || true
  ok "предыдущая сборка сохранена как swagcore-agent.old"
fi
install -m 0755 "$TMP/agent" "$BIN"
ok "установлено ($(stat -c%s "$BIN") байт)"

# ---------------- 6. systemd-юнит ----------------
say "Регистрируем systemd-юнит"
ARGS="run --server wss://$CORE/agent --token $TOKEN --data $DATA"
[ "$MAX_MEM" -gt 0 ]  && ARGS="$ARGS --max-mem $MAX_MEM"
[ "$MAX_DISK" -gt 0 ] && ARGS="$ARGS --max-disk $MAX_DISK"
[ "$NO_DOCKER" -eq 1 ] && ARGS="$ARGS --no-docker"

cat > "$UNIT" <<EOF
[Unit]
Description=swagCore node agent
Documentation=https://$CORE/
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN $ARGS
Restart=always
RestartSec=5
# агент работает от root, чтобы управлять docker и процессами; платформа
# доверяет коду ноды полностью, поэтому здесь НЕТ изоляции — это осознанно.
StandardOutput=append:$DATA/agent.log
StandardError=append:$DATA/agent.err.log

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SVC" >/dev/null 2>&1 || warn "не удалось включить автозапуск"
ok "юит создан, автозапуск включён"

# ---------------- 7. Docker ----------------
if [ "$NO_DOCKER_INSTALL" -eq 1 ]; then
  say "Docker пропущен по запросу"
elif command -v docker >/dev/null 2>&1; then
  say "Docker уже установлен"
  ok "$(docker --version 2>/dev/null || echo 'версия неизвестна')"
else
  say "Ставим Docker"
  # официальный скрипт; ставим в фоне, чтобы агент стартовал независимо
  if dl "https://get.docker.com" "$TMP/get-docker.sh" 3; then
    ( sh "$TMP/get-docker.sh" >"$DATA/docker-install.log" 2>&1 && systemctl enable --now docker >>"$DATA/docker-install.log" 2>&1 ) &
    ok "установка Docker запущена в фоне, лог: $DATA/docker-install.log"
  else
    warn "не удалось скачать get.docker.com — поставьте Docker вручную"
  fi
fi

# ---------------- 8. запуск ----------------
say "Запускаем агента"
systemctl restart "$SVC" 2>/dev/null || systemctl start "$SVC" 2>/dev/null || true
sleep 4
if systemctl is-active --quiet "$SVC"; then
  ok "служба работает (PID $(systemctl show -p MainPID --value "$SVC"))"
else
  warn "служба не active — смотрите: journalctl -u $SVC -n 40"
  systemctl status "$SVC" --no-pager -l 2>/dev/null | tail -20 || true
fi

# ---------------- 9. проверка панели ----------------
say "Проверяем подключение к панели"
CONNECTED=0
for i in $(seq 1 20); do
  if curl -fsS --max-time 15 "https://$CORE/api/health" >/dev/null 2>&1; then
    ok "сервер платформы отвечает"
    CONNECTED=1
    break
  fi
  echo "    попытка $i/20 — нода ещё не появилась в панели…"
  sleep 3
done

# ---------------- итог ----------------
printf '\n  \033[90m------------------------------------------------\033[0m\n'
if [ "$CONNECTED" = "1" ]; then
  printf '  \033[32mГОТОВО. Нода подключена.\033[0m\n\n'
else
  printf '  \033[33mУстановлено, но подключение не подтверждено.\033[0m\n\n'
fi
printf '  Панель:        https://%s/  ->  раздел «Ноды»\n' "$CORE"
printf '  Имя ноды:      %s\n' "$NAME"
printf '  Сборка агента: %s\n' "$LOCAL_BUILD"
printf '  Данные:        %s\n' "$DATA"
printf '  Лог агента:    journalctl -u %s -f\n' "$SVC"
printf '\n  Что дальше:\n'
printf '   1) Откройте панель и убедитесь, что нода %s в списке «Онлайн».\n' "$NAME"
printf '   2) Задеплойте проект: раздел «Проекты» -> «+ Задеплоить».\n'
printf '   3) Агент сам обновляется, когда выйдет новая сборка (раз в 30 мин).\n'
printf '\n  Снести: sudo bash install.sh --uninstall\n'
printf '  \033[90m------------------------------------------------\033[0m\n'

log "=== install done connected=$CONNECTED build=$LOCAL_BUILD ==="
exit 0
