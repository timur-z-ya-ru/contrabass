# PRD: Contrabass — Orchestrator для AI-агентов

**Дата:** March 2026
**Статус:** Production
**Версия:** Co-evolution document (соответствует коду на момент написания)

---

## Обзор

Contrabass — это Go-приложение для оркестрации AI-кодирующих агентов над GitHub Issues или Linear tickets. Пересимплементирует архитектуру OpenAI Symphony с адаптивным управлением нагрузкой, волновой конвейер-системой зависимостей и состояние-персистентной очередью повторов.

**Ключевые особенности:**
- Issue-driven workflow с механизмом claim/release
- Волновой конвейер (phase → wave) с автоматическим разрешением DAG
- Адаптивное масштабирование (scale up/down по CPU и памяти)
- Персистентность состояния (backoff queue сохраняется на graceful shutdown)
- Многопроцессная или горутин-base командная работа (tmux или goroutine)
- TUI (Charm v2: Bubble Tea, Lip Gloss, Bubbles) с snapshot-тестированием
- Web dashboard (React embedded, JSON/SSE API)
- Поддержка Linear, GitHub Issues, Internal Board трекеров
- Агент-адаптеры: Codex, OpenCode, oh-my-opencode, OMX, OMC

---

## Архитектура

### Слои

```
┌─────────────────────────────────────────────────────┐
│                 CLI (Cobra)                         │
│  team | board | wave [--config, --no-tui, etc]     │
└───────────────────┬─────────────────────────────────┘
                    │
┌───────────────────▼─────────────────────────────────┐
│            Orchestrator (Main Loop)                  │
│  - Issue polling + routing                          │
│  - Running map (issue ID → runEntry)                │
│  - Backoff queue (exponential retry + jitter)       │
│  - Event streaming (to TUI/web)                     │
└───────────────┬───────────────────────┬─────────────┘
                │                       │
        ┌───────▼─────────┐      ┌──────▼──────────────┐
        │  Wave Manager   │      │  Load Monitor       │
        │  (Phases/Waves) │      │  (CPU/Mem scale)    │
        │  - DAG builder  │      │  - /proc/loadavg    │
        │  - Promoter     │      │  - /proc/meminfo    │
        │  - Stall detect │      │  - Bidirectional    │
        └────────────────┘       └─────────────────────┘
                │
        ┌───────▼───────────────┐
        │ Tracker Adapter       │
        │ (Linear/GitHub/Board) │
        └───────────────────────┘
                │
        ┌───────▼───────────────┐
        │ Agent Runner          │
        │ (Codex/OpenCode/OMX)  │
        └───────────────────────┘
```

### Компоненты

#### Orchestrator (`internal/orchestrator/`)
- **Config:** max_concurrency, poll_interval_ms, max_retry_backoff_ms, agent_timeout_ms, stall_timeout_ms
- **Running map:** issue ID → { issue, attempt, process, cancel context, workspace, lastEventAt }
- **Backoff queue:** BackoffEntry { ID, RetryAt, Backoff (exponential) }
- **Issue cache:** LRU, maxIssueCacheSize = 1000
- **Stats:** Running, MaxAgents, TotalTokensIn, TotalTokensOut, StartTime, PollCount
- **Events:** OrchestratorEvent channel для TUI/web streaming

#### Wave Manager (`internal/wave/`)
- **Pipeline:** phases[i].waves[j].issues[k] — структурированный DAG
- **Auto-DAG mode:** если wave-config.yaml отсутствует, DAG строится из issues labeled `agent-ready`
- **Promoter:** PromoteWave(ctx, wave, allIssues) → повышает labels на issues в волне
- **Stall Detector:** CheckIssue(RunInfo) → { Continue | Retry | Escalate }
  - Escalate: attempt > MaxRetries ИЛИ Phase == Failed/TimedOut/Stalled
  - Retry: если stall выше WaveMaxAge
- **Merged PR cache:** mergedCacheEntry { merged bool, checked time.Time }, TTL = 60s для negative, immutable для merged=true

#### Load Monitor (`internal/loadmon/`)
- **Snapshot:** CPULoad (1-min load avg / numCPUs), MemUsed (fraction in use), Timestamp
- **Config:**
  - Ceiling (max_concurrency, абсолютный максимум)
  - Floor (default 1)
  - HighCPU = 0.80, LowCPU = 0.50 (CPU thresholds)
  - HighMem = 0.85, LowMem = 0.60 (memory thresholds)
  - PollInterval = 5s (default)
- **Compute algorithm** (bidirectional):
  - Если highLoad = (CPU > HighCPU ИЛИ Mem > HighMem) && current > floor → scale down на 1
  - Если lowLoad = (CPU < LowCPU И Mem < LowMem) && current < ceiling → scale up на 1
- **Linux-specific:** читает /proc/loadavg и /proc/meminfo

#### Workspace Manager (`internal/workspace/`)
- Управляет изолированной директорией для каждого issue
- Вызывает агент-runners

#### Agent Runners (`internal/runner/`)
- **Codex:** JSONL framing, session protocol (initialize → initialized → thread/start → turn/start → turn/completed)
- **OpenCode, oh-my-opencode, OMX, OMC:** поддерживаемые адаптеры

#### Trackers (`internal/tracker/`)
- **Interface:** GetIssues(ctx) → []types.Issue, ClaimIssue(ctx, id) → error, ReleaseIssue(ctx, id) → error, PostComment(ctx, id, body) → error
- **PRVerifier:** HasMergedPR(ctx, id) → bool (опциональный, для verify blocked deps)
- **Implementations:**
  - **Linear:** GraphQL query over API
  - **GitHub:** REST API v3
  - **Internal Board:** file-based (JSON), no external service

#### Persistence (`internal/orchestrator/persistence.go`)
- **File:** `.contrabass/state.json` (JSON marshaled PersistentState)
- **Structure:** { Backoff: []BackoffEntry, SavedAt, Version: 1 }
- **SaveState():** вызывается при graceful shutdown, сохраняет backoff queue
- **LoadState():** вызывается при startup, восстанавливает entries (с RetryAt clamped к now если expired)
- **Lifecycle:** issues остаются OPEN в очереди, восстанавливаются на следующем запуске

---

## CLI Commands

### Root Command
```bash
contrabass --config <path> [--no-tui] [--log-file <path>] [--log-level <level>] [--dry-run] [--port <num>]
```

**Flags:**
- `--config <path>` (required): путь к YAML-конфигу (или WORKFLOW.md)
- `--no-tui`: запуск в headless режиме (без Charm TUI)
- `--log-file <path>`: логирование в файл
- `--log-level <level>`: debug, info, warn, error
- `--dry-run`: exit после первого события (для тестирования)
- `--port <num>`: номер порта для web dashboard (SSE + React UI); если > 0 → включить, иначе отключить

### Subcommands
1. **team:** запуск командной работы (tmux или goroutine workers)
2. **board:** просмотр доски (issues, waves, stats)
3. **wave:** управление волнами (promote, force)

---

## Trackers (поддерживаемые)

### Linear
- **GraphQL API**
- Поля: ID, title, description, URL, status, labels, cycle
- Конфиг: team, API key

### GitHub Issues
- **REST API v3**
- Поля: number, title, body, state, labels, assignee, milestone
- Конфиг: owner/repo, token (GITHUB_TOKEN)

### Internal Board
- **File-based JSON** (no external service)
- Конфиг: path (например `issues.json`)
- Структура: { "issues": [{ "id", "title", "description", "state", "labels", "blockedBy" }] }

### PRVerifier Interface (опциональный)
Если трекер реализует `HasMergedPR(ctx, id) → bool`:
- FilterDispatchable() проверяет, что закрытые deps имеют merged PR
- Используется merged cache с TTL = 60s для negative results

---

## Agent Runners (поддерживаемые)

### Codex Protocol (основной)
- **Framing:** newline-delimited JSON (JSONL), not Content-Length
- **Session lifecycle:**
  1. initialize(id=1) → initialized notification
  2. thread/start(id=2, threadId) → response
  3. turn/start(id=3, threadId, turnId) → response
  4. turn/completed или turn/failed → финал
- **Terminal events:** turn/completed, turn/failed, turn/cancelled
- **Dynamic tools:** item/tool/call (обработка локально, respond с result)
- **Approval:** auto-approve if policy allows, иначе fail
- **Error handling:** response timeout, port exit, turn timeout, input required, approval required
- **Malformed lines:** tolerate, log, continue

### OpenCode
- Поддерживаемый адаптер (детали в config)

### oh-my-opencode
- Поддерживаемый адаптер (детали в config)

### OMX (oh-my-codex)
- Поддерживаемый адаптер для Codex integration

### OMC (oh-my-claudecode)
- Поддерживаемый адаптер для Claude Code integration

---

## Wave Pipeline

### Концепция
- **Phase:** логическая группа (e.g. "planning", "implementation", "testing")
- **Wave:** последовательный набор issues внутри phase
- **Issues:** конкретные GitHub/Linear tickets
- **DAG:** граф зависимостей (issue A blocks issue B)

### Auto-DAG Mode
Если wave-config.yaml отсутствует:
- DAG строится автоматически из issues с label `agent-ready`
- Затем issues группируются в вол­ны по БДП
- Ограничение: может быть неполным (только agent-ready issues видны)

### Wave Promotion
1. **Auto:** когда текущая волна завершена полностью
   - Все issues в wave → State = Complete/Closed
   - openSet[issue.ID] удаляются
   - OnIssueCompleted() → проверяет allDone в текущей wave
   - Если да, запускает m.promoter.PromoteWave(ctx, nextWave, allIssues)
2. **Manual:** `wave promote` (via CLI)
3. **Force:** `wave promote --force` (обходит completion checks)

### Dispatch Logic (FilterDispatchable)
```go
for _, issue := range issues {
  if issue.State != Unclaimed { continue }

  dispatchable := true
  for _, depID := range issue.BlockedBy {
    if openSet[depID] {
      // Dep всё ещё open → blocked
      dispatchable = false; break
    }
    if hasPV && shouldVerifyMerge(depID) {
      if !checkMergedCached(ctx, pv, depID) {
        dispatchable = false; break
      }
    }
  }

  if dispatchable { result = append(result, issue) }
}
```

**Сортировка:** wave index ascending, затем Blocks count descending

---

## Adaptive Load Management

### Алгоритм масштабирования (bidirectional)

**Вход:** текущее concurrency, CPU load, memory usage
**Выход:** новое concurrency (adjusted на ±1 в зависимости от load)

```
if (CPULoad > HighCPU OR MemUsed > HighMem) AND current > floor:
  current--  // scale down
else if (CPULoad < LowCPU AND MemUsed < LowMem) AND current < ceiling:
  current++  // scale up
```

### Параметры

| Параметр | Default | Описание |
|----------|---------|----------|
| HighCPU | 0.80 | Масштабировать вниз если выше |
| LowCPU | 0.50 | Масштабировать вверх если ниже |
| HighMem | 0.85 | Масштабировать вниз если выше |
| LowMem | 0.60 | Масштабировать вверх если ниже |
| PollInterval | 5s | Частота проверки /proc/loadavg и /proc/meminfo |
| Ceiling | max_concurrency | Абсолютный максимум (из конфига) |
| Floor | 1 (default) | Минимум concurrency |

### Метрики

- **CPULoad:** 1-minute load average / numCPUs (normalized 0-1+ range)
- **MemUsed:** fraction of total memory in use (0-1)
- **Timestamp:** moment of snapshot

### Источники данных
- Linux: `/proc/loadavg` (поле 0 → 1-min load), `/proc/meminfo` (MemTotal, MemAvailable)
- Fallback: return 0 if файл не читается (graceful degradation)

---

## State Persistence

### Сохранение состояния

**Trigger:** graceful shutdown (SIGINT, SIGTERM)
**File:** `.contrabass/state.json`
**Format:** JSON

```json
{
  "backoff": [
    {
      "id": "issue-123",
      "retryAt": "2026-03-28T14:30:00Z",
      "backoff": 2000
    }
  ],
  "savedAt": "2026-03-28T14:25:00Z",
  "version": 1
}
```

### Восстановление состояния

**Trigger:** startup
**Logic:**
1. Прочитать `.contrabass/state.json`
2. Unmarshaling JSON → PersistentState
3. Для каждого entry:
   - Если RetryAt < now → установить RetryAt = now (избежать immediate flood)
   - Добавить в backoff queue
4. Удалить state файл после загрузки
5. Следующий poll переберет issues, восстановленные из backoff

### Гарантии
- **Issues остаются OPEN:** не закрываются при shutdown, восстанавливаются в очереди
- **Exponential backoff:** сохраняет backoff duration для каждого retry attempt
- **Graceful degradation:** если state файл corrupt → log warning, ignore, start fresh

---

## WORKFLOW.md Format

### Структура

```yaml
---
title: "My Workflow"
phases:
  - name: planning
    description: "Issue analysis and planning"
    waves:
      - issues: [issue-1, issue-2]
      - issues: [issue-3]
---

You are an expert AI coding assistant.

## Task
{{ issue.title }}

## Description
{{ issue.description }}

## Links
- Issue: {{ issue.url }}
```

### YAML Frontmatter (передний материал)
```yaml
---
title: string
phases: array (optional)
  - name: string
    description: string (optional)
    waves: array
      - issues: [id1, id2, ...]
---
```

### Liquid Template Bindings
- `{{ issue.title }}` — название issue
- `{{ issue.description }}` — описание (body)
- `{{ issue.url }}` — URL issue в трекере
- `{{ issue.id }}` — ID issue
- `{{ issue.labels }}` — массив labels
- `{{ env.VAR_NAME }}` — переменные окружения (interpolation)

### Rendering
- YAML frontmatter парсится → phases/waves
- Markdown часть renderings с Liquid (issue/env binding)
- Результат отправляется агент-runners

---

## wave-config.yaml Format

### Структура (опциональный файл рядом с WORKFLOW.md)

```yaml
# Phases and waves (опционально, если нет в WORKFLOW.md frontmatter)
phases:
  - name: planning
    description: "Issue analysis"
    waves:
      - issues: [issue-1, issue-2]
      - issues: [issue-3]
  - name: implementation
    waves:
      - issues: [issue-4, issue-5, issue-6]

# Model routing
modelRouting:
  defaultLabel: "agent-ready"
  heavyLabel: "agent-heavy"
  modelMap:
    agent-light: "gpt-4-mini"
    agent-heavy: "gpt-4"

# Stall detection thresholds
stallDetection:
  maxRetries: 3
  waveMaxAge: 48h
```

### Поля

| Поле | Тип | Default | Описание |
|------|-----|---------|----------|
| phases[].name | string | - | Имя phase |
| phases[].waves[].issues | []string | - | Issue IDs в wave |
| modelRouting.defaultLabel | string | "agent-ready" | Label для стандартных issues |
| modelRouting.heavyLabel | string | "agent-heavy" | Label для heavy issues |
| modelRouting.modelMap | map | - | Маппинг label → model override |
| stallDetection.maxRetries | int | 3 | Максимум retry attempts |
| stallDetection.waveMaxAge | duration | 48h | Макс время на wave до escalate |

### Автоматическая загрузка
- Contrabass ищет `wave-config.yaml` рядом с WORKFLOW.md (или в CWD)
- Если found → используется для phases/waves/routing
- Если missing → auto-DAG mode (строится из agent-ready issues)

---

## Dashboard

### Web API (если --port > 0)

**Endpoint:** `http://localhost:<port>`

- **GET /api/stats** → JSON { running, maxAgents, totalTokensIn, totalTokensOut, startTime, pollCount }
- **GET /api/issues** → JSON { issues: [{ id, title, state, labels, blockedBy, ... }] }
- **GET /api/events** → Server-Sent Events (SSE) stream OrchestratorEvent
  - Event types: IssueStarted, IssueClaimed, IssueCompleted, IssueEscalated, WavePromoted, LoadChanged
  - Каждое event: { timestamp, type, issueID, data: {...} }

### TUI (Charm v2 + Bubble Tea)

**Modes:**
- **Issues list view:** скроллируемый список с состояниями (Running, Completed, Backoff, Blocked)
- **Issue detail view:** full issue description, last event, retry history
- **Wave view:** phases → waves → issues в wave
- **Stats view:** concurrency, tokens, load metrics, retry backoff

**Snapshot testing:** TUI может быть tested с snapshot assertions (текстовое представление экрана)

### React UI (embedded)
- Live stats (concurrency, tokens)
- Issues table (sortable, filterable)
- Events log (real-time SSE)
- Wave progression (visual pipeline)

---

## Известные ограничения

1. **Auto-DAG mode неполнота:**
   - Если wave-config.yaml отсутствует, DAG строится только из issues с label `agent-ready`
   - Может пропустить зависимости от unlabeled issues
   - **Рекомендация:** используйте wave-config.yaml для полного контроля

2. **Linux-only load monitoring:**
   - Load Monitor читает `/proc/loadavg` и `/proc/meminfo`
   - На macOS/Windows вернёт 0 (graceful fallback)
   - **Рекомендация:** используйте Linux для production

3. **Merged PR verification timeout:**
   - PRVerifier.HasMergedPR() может timeout если трекер slow
   - **Параметр:** agent_timeout_ms (default 5 минут)

4. **Issue cache LRU eviction:**
   - maxIssueCacheSize = 1000 issues
   - Если > 1000 → oldest evicted
   - **Рекомендация:** мониторьте cache hit rate в stats

5. **Backoff exponential cap:**
   - max_retry_backoff_ms (default 5 minutes)
   - Backoff не растёт бесконечно

6. **Concurrent worker limit (team mode):**
   - team.max_workers (default 4)
   - Ограничивает процессы на OS level (tmux) или горутины (goroutine)

7. **Process isolation (tmux mode only):**
   - каждый worker в отдельном tmux session
   - stderr/stdout → JSONL events, heartbeat via file
   - На exit worker → process cleanup

---

## Запуск

### Требования
- Go 1.18+
- Linux для load monitoring
- Docker (опционально) или native binary

### Инсталляция

```bash
cd ~/tools/contrabass
go build -o bin/contrabass ./cmd/contrabass
```

### Конфигурация

**config.yaml** (required):
```yaml
maxConcurrency: 4
pollIntervalMs: 5000
maxRetryBackoffMs: 300000
agentTimeoutMs: 300000
stallTimeoutMs: 86400000

tracker:
  type: github  # or linear, internal
  # github specific
  owner: my-org
  repo: my-repo
  token: ${GITHUB_TOKEN}

agentRunner:
  type: codex
  # codex specific
  port: 8080
  endpoint: http://localhost:8080

team:
  maxWorkers: 4
  claimLeaseSeconds: 30
  workerMode: tmux  # or goroutine
  executionMode: team  # or single, auto

webDashboard:
  enabled: true
  port: 3000
```

или используйте **WORKFLOW.md** с frontmatter (парсится как config):
```yaml
---
title: My Workflow
maxConcurrency: 4
tracker:
  type: github
  owner: my-org
  repo: my-repo
agentRunner:
  type: codex
  port: 8080
---
```

### Запуск

```bash
# TUI mode (interactive)
./bin/contrabass --config config.yaml

# Headless mode (no TUI)
./bin/contrabass --config config.yaml --no-tui

# With web dashboard
./bin/contrabass --config config.yaml --port 3000

# Dry-run (exit after first event)
./bin/contrabass --config config.yaml --dry-run

# With logging
./bin/contrabass --config config.yaml --log-file contrabass.log --log-level debug
```

### Subcommands

```bash
# Team mode (claim/dispatch workers)
./bin/contrabass --config config.yaml team

# Board view
./bin/contrabass --config config.yaml board

# Wave promotion (auto)
./bin/contrabass --config config.yaml wave promote

# Force wave promotion (bypass checks)
./bin/contrabass --config config.yaml wave promote --force
```

### Environment Variables
```bash
export GITHUB_TOKEN=ghp_...
export LINEAR_API_KEY=lin_api_...
export CONTRABASS_LOG_LEVEL=debug
./bin/contrabass --config config.yaml
```

### Graceful Shutdown
```bash
# Ctrl+C (SIGINT) или kill -TERM <pid>
# → SaveState() → backoff queue to .contrabass/state.json
# → Clean stop
```

On next startup:
```bash
./bin/contrabass --config config.yaml
# → LoadState() → restore backoff entries → resume
```

---

## Дополнительные ресурсы

- **README.md:** обширный overview с примерами
- **docs/codex-protocol.md:** полное описание JSONL protocol и session lifecycle
- **cmd/contrabass/main.go:** CLI entry point, flag parsing, initialization chain
- **internal/orchestrator/:** main loop, config, running map, backoff queue
- **internal/wave/:** pipeline, DAG builder, promoter, stall detector
- **internal/loadmon/:** adaptive concurrency algorithm, metrics
- **internal/tracker/:** adapter interfaces (Linear, GitHub, Internal)
- **internal/runner/:** agent runners (Codex, OpenCode, OMX, OMC)

---

**Документ актуален на:** March 2026
**Соответствие коду:** ✓ Verified (matching source at commit time)
