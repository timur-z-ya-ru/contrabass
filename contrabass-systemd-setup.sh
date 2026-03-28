#!/usr/bin/env bash
set -euo pipefail

# Contrabass Systemd Setup
# Настраивает systemd user service для автозапуска Contrabass при ребуте.
# Использование: ./contrabass-systemd-setup.sh <repo-name> [project-dir]
#
# Пример:
#   ./contrabass-systemd-setup.sh statworm-edc ~/projects/statworm.ru/services/edc

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${CYAN}i${NC} $*"; }
ok()    { echo -e "${GREEN}+${NC} $*"; }
warn()  { echo -e "${YELLOW}!${NC} $*"; }
err()   { echo -e "${RED}x${NC} $*" >&2; }

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <repo-name> [project-dir]"
  echo ""
  echo "  repo-name    — имя для systemd unit (например: statworm-edc)"
  echo "  project-dir  — путь к проекту (default: ~/projects/<repo-name>)"
  exit 1
fi

REPO_NAME="$1"
PROJECT_DIR="${2:-$HOME/projects/$REPO_NAME}"

if [[ ! -d "$PROJECT_DIR" ]]; then
  err "Директория $PROJECT_DIR не существует"
  exit 1
fi

if [[ ! -f "$PROJECT_DIR/WORKFLOW.md" ]]; then
  err "WORKFLOW.md не найден в $PROJECT_DIR"
  err "Сначала создай WORKFLOW.md (Issue Planner skill, Step 8.5)"
  exit 1
fi

# --- Template unit ---
UNIT_DIR="$HOME/.config/systemd/user"
if [[ ! -f "$UNIT_DIR/contrabass@.service" ]]; then
  info "Создаю template unit..."
  mkdir -p "$UNIT_DIR"
  cat > "$UNIT_DIR/contrabass@.service" << 'UNIT_EOF'
[Unit]
Description=Contrabass orchestrator for %i
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/home/booster/projects/%i
Environment=PATH=/home/booster/bin:/home/booster/.local/bin:/usr/local/bin:/usr/bin:/bin
Environment=HOME=/home/booster
EnvironmentFile=/home/booster/.config/contrabass/env
ExecStart=/bin/bash -c 'exec /home/booster/tools/contrabass/contrabass --config WORKFLOW.md --no-tui --log-file /home/booster/projects/%i/contrabass.log --log-level info'
Restart=on-failure
RestartSec=30

UnsetEnvironment=CLAUDECODE
UnsetEnvironment=CLAUDE_CODE_ENTRYPOINT
UnsetEnvironment=CLAUDE_AGENT_SDK_VERSION
UnsetEnvironment=CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING

[Install]
WantedBy=default.target
UNIT_EOF
  ok "Template unit создан: $UNIT_DIR/contrabass@.service"
else
  ok "Template unit уже существует"
fi

# --- Env file ---
ENV_FILE="$HOME/.config/contrabass/env"
if [[ ! -f "$ENV_FILE" ]]; then
  mkdir -p "$(dirname "$ENV_FILE")"
  if command -v gh &>/dev/null && gh auth status &>/dev/null; then
    echo "GITHUB_TOKEN=$(gh auth token)" > "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    ok "Env-файл создан: $ENV_FILE"
  else
    echo "GITHUB_TOKEN=" > "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    warn "Env-файл создан, но GITHUB_TOKEN пустой — заполни вручную: $ENV_FILE"
  fi
else
  ok "Env-файл уже существует: $ENV_FILE"
fi

# --- Enable + start ---
systemctl --user daemon-reload
systemctl --user enable "contrabass@$REPO_NAME"
ok "Сервис contrabass@$REPO_NAME включён"

echo ""
info "Запустить сервис сейчас? [Y/n]:"
read -r start_now
if [[ "${start_now,,}" != "n" ]]; then
  systemctl --user start "contrabass@$REPO_NAME"
  sleep 2
  if systemctl --user is-active "contrabass@$REPO_NAME" &>/dev/null; then
    ok "Сервис запущен"
  else
    err "Сервис не запустился. Проверь: journalctl --user -u contrabass@$REPO_NAME"
  fi
fi

# --- Summary ---
echo ""
echo -e "${GREEN}Готово!${NC}"
echo ""
echo "  Управление:"
echo "    systemctl --user status contrabass@$REPO_NAME"
echo "    systemctl --user stop contrabass@$REPO_NAME"
echo "    systemctl --user restart contrabass@$REPO_NAME"
echo "    tail -f $PROJECT_DIR/contrabass.log"
