# swagCore

**swagCore** — мини-PaaS: платформа, которая сама разворачивает ваши проекты
(сайты, боты, скрипты) на ваших же машинах и сама их чинит.

Одна команда на новой машине — и она становится нодой платформы.

```
┌──────────────────────────────────────────────────────────────────────┐
│  Панель:  https://core.swag.best/          (control-plane, Go+SQLite) │
└───────────────┬──────────────────────────────────────────────────────┘
                │  wss://core.swag.best/agent   (исходящее соединение)
                │  ┌────────────────────────┐  ┌────────────────────────┐
                └─►│ агент на Windows       │  │ агент на Linux/Docker  │
                   │ за NAT, без входящих   │  │ в DMZ                  │
                   │ портов                 │  │                        │
                   │ проекты в process-     │  │ проекты в Docker       │
                   │ режиме                │  │                        │
                   └────────────────────────┘  └────────────────────────┘
```

---

## Что это умеет

| Возможность | Как работает |
|---|---|
| **Подключение ноды одной командой** | `install.ps1` / `install.sh` сами скачивают агента, ставят его службой, запускают и проверяют подключение |
| **Машины за NAT** | Агент делает только исходящее соединение. Белый IP и входящие порты не нужны |
| **Два режима запуска проектов** | `docker` — изолированный контейнер с квотами; `process` — нативный бинарник (для машин без Docker) |
| **Самовосстановление** | Платформа каждую минуту спрашивает ноды, что на самом деле запущено, и поднимает упавшее сама |
| **Планировщик** | Сам выбирает ноду: меньше всего загружена по памяти, есть Docker, подходит ОС, влезает в квоты |
| **Квоты ресурсов** | Ограничение памяти/CPU/диска на ноду — планировщик их учитывает по-настоящему |
| **Публичные домены** | Поле `domain` в манифесте → nginx-карта генерируется сама, TLS `*.swag.best` уже настроен |
| **ИИ-агент** | Groq: управляет нодами, деплоит проекты, читает логи — словами, а не командами |
| **Управление нодами** | Команды, скриншоты экрана, перезагрузка, лимиты — прямо из панели |
| **Самообновление** | Агенты сами подтягивают новую сборку (раз в 30 минут) |

---

## Быстрый старт

### 1. Поднять control-plane

```bash
export ADMIN_TOKEN=$(pwgen -s 40 1)     # токен входа в панель
export LISTEN=127.0.0.1:8181           # слушаем только localhost
export DATA_DIR=/opt/swagcore/data

go build -o swagcore-server ./cmd/server
./swagcore-server
```

Дальше — nginx + сертификат. Готовые конфиги: `deploy/nginx-swagcore.conf`,
`deploy/nginx-stream-dreamtunnel.conf`, `deploy/swagcore-server.service`.

### 2. Подключить ноду

В панели: **Добавить ноду** → задать имя и лимиты → нажать
**«Создать токен и показать команду»** → скопировать команду.

**Windows** (чистая Windows 10/11, ничего ставить не нужно):

```powershell
irm https://core.swag.best/download/install.ps1 -OutFile $env:TEMP\swagcore-install.ps1; & $env:TEMP\swagcore-install.ps1 -Token "<ТОКЕН>" -Name "office-pc"
```

**Linux** (Ubuntu/Debian):

```bash
curl -fsSL https://core.swag.best/download/install.sh | sudo bash -s -- <ТОКЕН>
```

Скрипт: проверит систему → скачает агента и сверит контрольную сумму →
поставит службу с автозагрузкой и перезапуском при падении → запустит →
дождётся появления ноды в панели.

С правами администратора ставится **служба Windows** (переживает выход из сессии,
перезапускается при падении). Без прав — автозагрузка в `HKCU\Run`
(работает, но без защиты от сбоев и переживает только сеанс входа).

Отключение: `.\install.ps1 -Uninstall` или `sudo bash install.sh --uninstall`.

---

## Манифест проекта

```yaml
name: mysite                    # имя проекта (латиница, дефис, подчёркивание)
image: nginx:alpine            # Docker-образ

ports:                          # "хост:контейнер" или "авто"
  - "8090:80"

resources:
  memory: 256m
  cpus: "0.5"

placement: all                  # all = все подходящие ноды
                                # selected = только nodes: [1, 2]
# preferred_node: vm4168356     # или закрепить за конкретной нодой
# os: windows                   # фильтр по ОС

domain: mysite.swag.best        # публичный адрес (TLS *.swag.best готов)

# Нативное приложение без Docker:
# mode: process
# os: windows
# command: ./app.exe
# env: {PORT: "3001"}

# Файлы проекта (tar.gz):
# artifact: /artifacts/mysite/site.tar.gz
# artifact_sha: <sha256>
# mount_path: /usr/share/nginx/html
```

### Артефакты

Файлы проекта кладутся в `DATA_DIR/artifacts/<имя>/` и раздаются агентам
**только с валидным токеном ноды** (`X-Node-Token`) или админской сессией.
Снаружи без токена — 401.

```bash
curl -X POST -H "X-Node-Token: <ТОКЕН_АДМИНА>" \
     -F "file=@site.tar.gz" \
     https://core.swag.best/api/artifacts/mysite
# -> {"ok":"saved","url":"/artifacts/mysite/site.tar.gz","sha256":"..."}
```

---

## Публичные домены

`domain` из манифеста попадает в БД → cron-скрипт `sync-domains.sh` раз в
2 минуты генерирует `/etc/nginx/swag-sites.map` → nginx перезагружается.

```bash
# /usr/local/bin/sync-domains.sh  (запускать от root)
map $host $sw_site_backend {
    default "";
    <domain>  <ip>:<port>;
}
```

Поддерживается любой edge: если нода за NAT, проброс порта делается
reverse-туннелем (например, `ssh -R 18300:3001 …`), а скрипт подставляет
`127.0.0.1:<порт-туннеля>` для хостов с `dream`-префиксом.

---

## Релиз

```bash
./deploy/release.sh              # собрать всё и выложить на сервер
./deploy/release.sh --build-only # только собрать
```

Порядок выкладки **критичен**, и скрипт соблюдает его:

1. бинарники агентов (все платформы) + `.sha256`
2. установщики `install.sh` / `install.ps1`
3. **и только потом** `version.txt`
4. control-plane и перезапуск

> Почему так: в v0.5.x бинарники пересобрали в 06:17, а `version.txt` писали
> в 05:57. Номер версии не изменился → ни одна нода не обновилась и осталась
> на старой сборке навсегда. Теперь `version.txt` содержит `версия+сборка`
> (`0.6.0+v0600`), где `Build` меняется при каждой пересборке.

`install.ps1` публикуется в **UTF-8 с BOM** (PowerShell 5.1 иначе ломает
кириллицу), `install.sh` — в **LF** (bash не понимает `\r`).

---

## API

Все защищённые ручки принимают `Authorization: Bearer <ADMIN_TOKEN>` или
cookie `admin_token`.

| Метод | Путь | Что делает |
|---|---|---|
| GET | `/api/health` | публичный: версия, ноды, проекты, размер БД |
| GET | `/api/summary` | сводка для дашборда |
| GET | `/api/nodes` | список нод с метриками |
| GET | `/api/projects` | список проектов |
| GET | `/api/events?limit=N` | журнал (без технического шума опроса нод) |
| POST | `/api/bootstrap` | **создать токен + вернуть готовые команды установки** |
| POST | `/api/action` | единая точка действий UI (`__do=/nodes/delete`, `/projects/deploy`, …) |
| POST | `/upload/` | загрузка файла агентом (скриншоты) |
| POST | `/api/artifacts/<проект>` | загрузка артефакта проекта |

Пример — подключить ноду одной командой через API:

```bash
curl -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
     -d "name=office-pc&max_mem=2048" \
     https://core.swag.best/api/bootstrap
```

```json
{
  "ok": true, "name": "office-pc", "token": "…",
  "cmd_windows": "irm https://core.swag.best/download/install.ps1 -OutFile $env:TEMP\\swagcore-install.ps1; & $env:TEMP\\swagcore-install.ps1 -Token \"…\" -Name \"office-pc\"",
  "cmd_linux":   "curl -fsSL https://core.swag.best/download/install.sh | sudo bash -s -- …"
}
```

---

## Безопасность

* Панель — только по `ADMIN_TOKEN`; попытки входа с одного IP блокируются
  (8 ошибок → пауза, далее экспоненциально до 15 минут).
* Сравнение токенов — без раннего выхода (защита от тайминга).
* `/screenshots/` и `/artifacts/` закрыты: скриншоты видит только админ,
  артефакты — админ или агент с валидным `X-Node-Token`. Листинг каталогов
  артефактов отключён.
* В БД хранится **только sha256** токена ноды, сырой токен не сохраняется.
* `LISTEN=127.0.0.1:8181` — наружу отдаёт только nginx на 443.
* Cookie: `HttpOnly`, `SameSite=Lax`, `Secure` при HTTPS, 30 дней.
* Агент перед подменой собственного бинарника проверяет его запуском
  (`version`) и сверяет sha256; предыдущая сборка остаётся как `.old`.
* Заголовки ответов: `X-Content-Type-Options`, `X-Frame-Options`,
  `Referrer-Policy`.
* Резервные копии БД и сжатие WAL — по cron
  (`/etc/cron.d/swagcore-maintenance`), старые копии удаляются через 14 дней.

---

## Структура репозитория

```
cmd/server/       control-plane: флаги, env, запуск сервера и реконсилера
cmd/agent/        агент: подкоманды install/uninstall/run/doctor/version
internal/model/   общие типы, константы протокола и статусов, версия+сборка
internal/store/   SQLite: схема, миграции, CRUD, drift-детекция, ротация журнала
internal/hub/     WebSocket-хаб: handshake, heartbeat, ExecSync, анти-дубль нод
internal/sched/   выбор ноды по загрузке
internal/reconciler/  цикл приведения к желаемому + квоты + housekeeping
internal/api/     HTTP: страницы, действия, JSON API, авторизация
internal/ai/      Groq-агент: чат, 13 тулзов, workspace
internal/agent/   клиент, docker/process-исполнители, метрики, скриншоты, selfupdate
deploy/           install.ps1, install.sh, release.sh, nginx, systemd
examples/         пример манифеста и статики
reports/          отчёты по сессиям и состоянию платформы
```

---

## Требования

* control-plane: Go 1.26+, Linux, systemd (опционально), nginx
* нода: Go не нужен — только ОС. Docker нужен для `mode: docker`
* зависимости Go: gorilla/websocket, kardianos/service, yaml.v3, modernc.org/sqlite
