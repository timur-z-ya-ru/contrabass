#!/usr/bin/env bash
set -euo pipefail

# Contrabass Setup Wizard
# Настраивает GitHub project board + WORKFLOW.md + systemd для нового проекта

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${CYAN}ℹ${NC} $*"; }
ok()    { echo -e "${GREEN}✓${NC} $*"; }
warn()  { echo -e "${YELLOW}⚠${NC} $*"; }
err()   { echo -e "${RED}✗${NC} $*" >&2; }
ask()   { echo -en "${CYAN}?${NC} $* "; }

# --- Проверка зависимостей ---
for cmd in gh contrabass omc tmux; do
  if ! command -v "$cmd" &>/dev/null; then
    err "Не найден: $cmd"
    exit 1
  fi
done

if ! gh auth status &>/dev/null; then
  err "GitHub CLI не авторизован. Выполни: gh auth login"
  exit 1
fi

GITHUB_USER=$(gh api user --jq '.login')

echo ""
echo -e "${CYAN}═══════════════════════════════════════════════════${NC}"
echo -e "${CYAN}  Contrabass Setup Wizard${NC}"
echo -e "${CYAN}  Настройка автономного агента для GitHub-проекта${NC}"
echo -e "${CYAN}═══════════════════════════════════════════════════${NC}"
echo ""

# --- Шаг 1: Выбор проекта ---
info "Доступные репозитории:"
echo ""
repos=$(gh repo list "$GITHUB_USER" --limit 30 --json name,description --jq '.[] | "\(.name)\t\(.description // "-")"')
i=1
declare -a repo_names=()
while IFS=$'\t' read -r name desc; do
  printf "  %2d. %-25s %s\n" "$i" "$name" "$desc"
  repo_names+=("$name")
  ((i++))
done <<< "$repos"
echo ""

ask "Номер репозитория (или введи owner/repo):"
read -r repo_input

if [[ "$repo_input" =~ ^[0-9]+$ ]]; then
  idx=$((repo_input - 1))
  if [[ $idx -lt 0 || $idx -ge ${#repo_names[@]} ]]; then
    err "Неверный номер"
    exit 1
  fi
  REPO_NAME="${repo_names[$idx]}"
  REPO_FULL="$GITHUB_USER/$REPO_NAME"
elif [[ "$repo_input" =~ / ]]; then
  REPO_FULL="$repo_input"
  REPO_NAME="${repo_input##*/}"
else
  REPO_NAME="$repo_input"
  REPO_FULL="$GITHUB_USER/$REPO_NAME"
fi

# Проверка что репо существует
if ! gh repo view "$REPO_FULL" &>/dev/null; then
  err "Репозиторий $REPO_FULL не найден"
  exit 1
fi
ok "Репозиторий: $REPO_FULL"

# --- Шаг 2: Определение локального пути ---
OWNER="${REPO_FULL%%/*}"
DEFAULT_PROJECT_DIR="$HOME/projects/$REPO_NAME"

ask "Локальный путь к проекту [$DEFAULT_PROJECT_DIR]:"
read -r project_dir
PROJECT_DIR="${project_dir:-$DEFAULT_PROJECT_DIR}"

if [[ ! -d "$PROJECT_DIR" ]]; then
  warn "Директория $PROJECT_DIR не существует"
  ask "Склонировать репозиторий? [Y/n]:"
  read -r clone_answer
  if [[ "${clone_answer,,}" != "n" ]]; then
    git clone "https://github.com/$REPO_FULL.git" "$PROJECT_DIR"
    ok "Склонировано в $PROJECT_DIR"
  else
    err "Директория не существует, прерывание"
    exit 1
  fi
fi
ok "Проект: $PROJECT_DIR"

# --- Шаг 3: Настройка label ---
info "Проверяю label 'agent' в $REPO_FULL..."
if gh label list --repo "$REPO_FULL" --json name --jq '.[].name' | grep -q '^agent$'; then
  ok "Label 'agent' уже существует"
else
  gh label create agent --repo "$REPO_FULL" --color "0075ca" --description "Задача для автономного агента Contrabass" 2>/dev/null
  ok "Label 'agent' создан"
fi

# --- Шаг 4: Настройка GitHub Project Board ---
info "Проверяю GitHub Project Board..."

EXISTING_PROJECT=$(gh project list --owner "$OWNER" --format json 2>/dev/null | \
  python3 -c "import sys,json; projects=json.load(sys.stdin).get('projects',[]); matches=[p for p in projects if p.get('title','').lower()=='${REPO_NAME}'.lower()]; print(matches[0]['number'] if matches else '')" 2>/dev/null || echo "")

if [[ -n "$EXISTING_PROJECT" ]]; then
  ok "Project Board #$EXISTING_PROJECT уже существует"
  PROJECT_NUMBER="$EXISTING_PROJECT"
else
  ask "Создать GitHub Project Board '$REPO_NAME'? [Y/n]:"
  read -r create_board
  if [[ "${create_board,,}" != "n" ]]; then
    PROJECT_NUMBER=$(gh project create --owner "$OWNER" --title "$REPO_NAME" --format json 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('number',''))")
    if [[ -n "$PROJECT_NUMBER" ]]; then
      ok "Project Board #$PROJECT_NUMBER создан"

      # Привязать репо к проекту
      gh project link "$PROJECT_NUMBER" --owner "$OWNER" --repo "$REPO_FULL" 2>/dev/null && \
        ok "Репозиторий привязан к project board" || \
        warn "Не удалось привязать репозиторий (сделай вручную)"
    else
      warn "Не удалось создать project board"
    fi
  fi
fi

# --- Шаг 5: Параметры WORKFLOW.md ---
echo ""
info "Настройка параметров агента:"
echo ""

ask "Максимальная конкурентность агентов [2]:"
read -r max_conc
MAX_CONCURRENCY="${max_conc:-2}"

ask "Модель (anthropic/claude-sonnet-4-6 или anthropic/claude-opus-4-6) [anthropic/claude-sonnet-4-6]:"
read -r model_input
MODEL="${model_input:-anthropic/claude-sonnet-4-6}"

ask "Таймаут агента в минутах [15]:"
read -r timeout_min
AGENT_TIMEOUT_MS=$(( ${timeout_min:-15} * 60000 ))

ask "Краткое описание проекта:"
read -r project_desc

ask "Технологии (через запятую, напр: FastAPI, React, PostgreSQL):"
read -r tech_stack

ask "Команда запуска тестов (или пусто если нет):"
read -r test_command

# --- Шаг 6: Генерация WORKFLOW.md ---
info "Генерирую WORKFLOW.md..."

TEST_SECTION=""
if [[ -n "$test_command" ]]; then
  TEST_SECTION="### Тестирование
\`\`\`bash
$test_command
\`\`\`

Тесты ОБЯЗАНЫ проходить перед созданием PR."
fi

cat > "$PROJECT_DIR/WORKFLOW.md" << WORKFLOW_EOF
---
max_concurrency: $MAX_CONCURRENCY
poll_interval_ms: 15000
model: $MODEL
agent_timeout_ms: $AGENT_TIMEOUT_MS
stall_timeout_ms: 180000
tracker:
  type: github
  owner: $OWNER
  repo: $REPO_NAME
  labels:
    - agent
  assignee: $OWNER
  token: \$GITHUB_TOKEN
agent:
  type: omc
team:
  worker_mode: goroutine
omc:
  binary_path: omc
  team_spec: 1:claude
  poll_interval_ms: 1200
  startup_timeout_ms: 21000
workspace:
  branch_prefix: agent/
---
# Задача: {{ issue.title }}

**Issue:** {{ issue.url }}

## Описание задачи
{{ issue.description }}

## Шаг 0 — Ориентация

Ты — автономный агент-разработчик. Работаешь над проектом **$REPO_NAME** — $project_desc.

{% if attempt > 1 %}
**⚠ Это повторная попытка (attempt {{ attempt }}).** Предыдущая попытка не завершилась успешно.
- Проверь текущее состояние workspace: \`git status\`, \`git log --oneline -5\`
- Если ветка и PR уже созданы — продолжи с того места, где остановился
- Если workspace чистый — начни заново
- НЕ создавай дублирующий PR
{% endif %}

Перед началом работы:
1. Прочитай \`CLAUDE.md\` проекта — там правила, стек, команды
2. Изучи структуру проекта: \`ls\`, ключевые директории
3. Пойми контекст задачи из описания issue

## Шаг 1 — Workpad (прогресс-комментарий)

Создай комментарий в GitHub Issue для отслеживания прогресса. Используй \`gh issue comment\`:

\`\`\`
## 🤖 Agent Workpad

**Status:** 🔄 In Progress
**Branch:** \`agent/<slug>\`
**Attempt:** {{ attempt }}

### План
- [ ] <пункт 1>
- [ ] <пункт 2>
- [ ] <пункт 3>

### Решения
_<ключевые решения по ходу работы>_

### Блокеры
_<если есть>_
\`\`\`

Обновляй этот комментарий по мере продвижения (используй \`gh issue comment --edit-last\`).

## Шаг 2 — Реализация

### Создание ветки
\`\`\`bash
git checkout main && git pull
git checkout -b agent/issue-<номер>-<slug>
\`\`\`

### Разработка
1. Напиши код, удовлетворяющий требованиям issue
2. Следуй существующим паттернам проекта
3. Добавь тесты, если уместно
4. Проверь линтинг, если настроен

$TEST_SECTION

## Шаг 3 — Создание PR

Создай Pull Request:
\`\`\`bash
gh pr create --title "<краткое описание>" --body "\$(cat <<'EOF'
## Что сделано
<описание изменений>

## Тесты
<какие тесты добавлены/проверены>

## Связь
Closes #<номер-issue>

🤖 Автоматически создано агентом Contrabass
EOF
)"
\`\`\`

Обнови workpad-комментарий: \`Status: ✅ PR Created\`

## Технологии проекта
$tech_stack

## Guardrails (запреты)

**НИКОГДА не делай:**
- ❌ НЕ мержи PR — жди ручной review
- ❌ НЕ закрывай issue — это делает PR при мерже
- ❌ НЕ коммить в main — только feature-ветка
- ❌ НЕ меняй файлы вне scope задачи (CLAUDE.md, CI конфиги, docker-compose без явной просьбы)
- ❌ НЕ удаляй и не переименовывай существующие API-эндпоинты без явного указания в issue
- ❌ НЕ добавляй новые зависимости без крайней необходимости
- ❌ НЕ хардкодь секреты — только переменные окружения

**Если заблокирован (нет доступа, непонятно требование):**
- Обнови workpad: \`Status: ⚠️ Blocked\`, опиши проблему
- НЕ угадывай — лучше остановись и опиши что непонятно
WORKFLOW_EOF

ok "WORKFLOW.md создан: $PROJECT_DIR/WORKFLOW.md"

# --- Шаг 7: Systemd ---
echo ""
ask "Включить systemd-сервис (автозапуск при ребуте)? [Y/n]:"
read -r enable_systemd

if [[ "${enable_systemd,,}" != "n" ]]; then
  # Убедиться что template unit существует
  UNIT_DIR="$HOME/.config/systemd/user"
  if [[ ! -f "$UNIT_DIR/contrabass@.service" ]]; then
    warn "Template unit не найден, создаю..."
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
ExecStart=/bin/bash -c 'exec /home/booster/bin/contrabass --config WORKFLOW.md --no-tui --log-file /home/booster/projects/%i/contrabass.log --log-level info'
Restart=on-failure
RestartSec=30

UnsetEnvironment=CLAUDECODE
UnsetEnvironment=CLAUDE_CODE_ENTRYPOINT
UnsetEnvironment=CLAUDE_AGENT_SDK_VERSION
UnsetEnvironment=CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING

[Install]
WantedBy=default.target
UNIT_EOF
    ok "Template unit создан"
  fi

  # Убедиться что env-файл существует
  ENV_FILE="$HOME/.config/contrabass/env"
  if [[ ! -f "$ENV_FILE" ]]; then
    mkdir -p "$(dirname "$ENV_FILE")"
    echo "GITHUB_TOKEN=$(gh auth token)" > "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    ok "Env-файл создан: $ENV_FILE"
  fi

  systemctl --user daemon-reload
  systemctl --user enable "contrabass@$REPO_NAME"
  ok "Сервис contrabass@$REPO_NAME включён"

  ask "Запустить сервис сейчас? [Y/n]:"
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
fi

# --- Итог ---
echo ""
echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Настройка завершена!${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
echo ""
echo "  Проект:     $REPO_FULL"
echo "  Директория: $PROJECT_DIR"
echo "  WORKFLOW:   $PROJECT_DIR/WORKFLOW.md"
echo ""
echo "  Управление:"
echo "    systemctl --user status contrabass@$REPO_NAME"
echo "    systemctl --user stop contrabass@$REPO_NAME"
echo "    systemctl --user restart contrabass@$REPO_NAME"
echo "    tail -f $PROJECT_DIR/contrabass.log"
echo ""
echo "  Создание задачи для агента:"
echo "    gh issue create --repo $REPO_FULL --label agent --title \"...\" --body \"...\""
echo ""
