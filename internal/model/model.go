// Package model описывает общие типы платформы swagCore:
// ноды, проекты, манифесты и протокол сообщений сервер <-> агент.
package model

import (
	"encoding/json"
	"time"
)

// Version — версия протокола/сборки. Используется selfupdate агента.
//
// ВАЖНО (v0.6.0): номер версии недостаточен для selfupdate. Реальный случай:
// binaries пересобрали в 06:17, а version.txt писали в 05:57 — номер остался
// прежним, и ни одна нода не обновилась. Поэтому Build — короткий хеш сборки,
// который меняется при КАЖДОЙ пересборке. Агент сравнивает строку
// "<Version>+<Build>" целиком, поэтому любая пересборка всегда доезжает до нод.
// Формат version.txt на сервере: "0.6.0+ab12cd34".
const (
	Version = "0.7.0"
	// Build — вручную bumps при пересборке бинарников агента.
	// Деплой-скрипт deploy/release.sh генерирует version.txt из Version+Build.
	Build = "v0712"
)

// VersionTag — полная строка версии для сравнения в selfupdate.
func VersionTag() string { return Version + "+" + Build }

// ---------- Ноды ----------

// Metrics — метрики ноды, присылаемые агентом в heartbeat.
type Metrics struct {
	CPUPct      float64 `json:"cpu_pct"`
	MemUsedMB   uint64  `json:"mem_used_mb"`
	MemTotalMB  uint64  `json:"mem_total_mb"`
	DiskUsedGB  float64 `json:"disk_used_gb"`
	DiskTotalGB float64 `json:"disk_total_gb"`
	Containers  int     `json:"containers"`
	UptimeSec   uint64  `json:"uptime_sec,omitempty"`
}

// Node — зарегистрированная нода в реестре сервера.
type Node struct {
	ID         int64  `json:"id"`
	TokenHash  string `json:"-"`
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	AgentVer   string `json:"agent_version"`
	IP         string `json:"ip"`
	Status     string `json:"status"` // online / offline
	LastSeen   time.Time `json:"last_seen"`
	CreatedAt  time.Time `json:"created_at"`
	Metrics    *Metrics          `json:"metrics,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Enabled    bool              `json:"enabled"`     // участвует в планировании (воркер)
	MaxMemMB   int               `json:"max_mem_mb"`  // лимит RAM для проектов платформы (0 = без лимита)
	MaxCPUs    float64           `json:"max_cpus"`    // лимит ядер CPU (0 = без лимита)
	MaxDiskGB  int               `json:"max_disk_gb"` // лимит диска GB (0 = без лимита)
	HasDocker  bool              `json:"has_docker"`
	// InstanceID — постоянный идентификатор МАШИНЫ (не токена и не node_id).
	// Агент генерирует его один раз в своём data-каталоге и шлёт в каждом hello.
	// Нужен, чтобы хаб мог отличать «новая нода» от «та же самая машина»:
	// раньше два автозапуска с разными токенами создавали две ноды с одинаковым
	// hostname, и обе работали (гонка за порты). Теперь дубль определяется на
	// сервере и схлопывается автоматически.
	InstanceID string `json:"instance_id,omitempty"`
}

// NodeToken — именованный токен подключения ноды.
type NodeToken struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Token     string    `json:"token,omitempty"` // заполняется только при создании
	TokenHash string    `json:"-"`
	UsedBy    string    `json:"used_by,omitempty"` // hostname ноды, взявшей токен
	Used      bool      `json:"used"`
	CreatedAt time.Time `json:"created_at"`
	// Лимиты, применяемые к ноде при первом подключении с этим токеном (0 = без лимита).
	MaxMemMB  int     `json:"max_mem_mb,omitempty"`
	MaxCPUs   float64 `json:"max_cpus,omitempty"`
	MaxDiskGB int     `json:"max_disk_gb,omitempty"`
}

// ---------- Проекты ----------

// Manifest — декларативное описание проекта (core.yml).
type Manifest struct {
	Name          string            `yaml:"name" json:"name"`
	Mode          string            `yaml:"mode,omitempty" json:"mode,omitempty"`   // docker (по умолчанию) | process (нативное приложение)
	Image         string            `yaml:"image,omitempty" json:"image,omitempty"` // для mode=process не нужен
	OS            string            `yaml:"os,omitempty" json:"os,omitempty"`       // фильтр ОС нод: windows | linux (пусто = любые)
	Command       string            `yaml:"command,omitempty" json:"command,omitempty"`
	Ports         []string          `yaml:"ports,omitempty" json:"ports,omitempty"`
	Env           map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Volumes       []string          `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	Resources     Resources         `yaml:"resources,omitempty" json:"resources,omitempty"`
	Replicas      int               `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	PreferredNode string            `yaml:"preferred_node,omitempty" json:"preferred_node,omitempty"` // hostname или empty
	Placement     string            `yaml:"placement,omitempty" json:"placement,omitempty"`           // all (по умолчанию) | selected
	Nodes         []int64           `yaml:"nodes,omitempty" json:"nodes,omitempty"`                   // для placement=selected
	Artifact      string            `yaml:"artifact,omitempty" json:"artifact,omitempty"`             // URL tar.gz с файлами проекта
	ArtifactSHA   string            `yaml:"artifact_sha,omitempty" json:"artifact_sha,omitempty"`     // sha256 tar.gz (проверка на агенте)
	MountPath     string            `yaml:"mount_path,omitempty" json:"mount_path,omitempty"`         // точка монтирования (напр. /www)
	Domain        string            `yaml:"domain,omitempty" json:"domain,omitempty"`                 // публичный домен (напр. status.swag.best); TLS *.swag.best на edge
}

// Размещение проекта на нодах.
//
//	placement "all"      — на всех docker-нодах (включая добавленные позже) — по умолчанию;
//	placement "selected" — только на выбранных node_ids.
const (
	PlacementAll      = "all"
	PlacementSelected = "selected"
)

// Статусы экземпляра проекта на ноде.
const (
	InstDesired  = "desired"  // ждёт деплоя (нода оффлайн или очередь)
	InstStarting = "starting" // задача отправлена
	InstRunning  = "running"
	InstStopped  = "stopped"
	InstFailed   = "failed"
	InstOrphan   = "orphan" // нода оффлайн, экземпляр числится — статус неизвестен
)

// Resources — квоты ресурсов контейнера.
type Resources struct {
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty"` // напр. 256m
	CPUs   string `yaml:"cpus,omitempty" json:"cpus,omitempty"`     // напр. "0.5"
}

// Project — проект в БД сервера.
type Project struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Manifest  Manifest  `json:"manifest"`
	NodeID    int64     `json:"node_id"` // legacy (v0.1), не используется при placement
	Status    string    `json:"status"`
	Container string    `json:"container,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProjectInstance — экземпляр проекта на конкретной ноде.
type ProjectInstance struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	NodeID    int64     `json:"node_id"`
	Status    string    `json:"status"`
	Container string    `json:"container,omitempty"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Статусы проектов.
const (
	StatusDesiredRunning = "desired_running"
	StatusDesiredStopped = "desired_stopped"
	StatusStarting       = "starting"
	StatusRunning        = "running"
	StatusStopped        = "stopped"
	StatusFailed         = "failed"
)

// ---------- Протокол WS сервер <-> агент ----------

// Типы сообщений.
const (
	MsgHello    = "hello"         // агент -> сервер: регистрация
	MsgWelcome  = "welcome"       // сервер -> агент: подтверждение + node_id
	MsgPing     = "ping"          // агент -> сервер: heartbeat + метрики
	MsgDeploy   = "deploy"        // сервер -> агент: запустить проект
	MsgStop     = "stop"          // сервер -> агент: остановить проект
	MsgDelete   = "delete"        // сервер -> агент: удалить контейнер
	MsgStatus   = "status"        // агент -> сервер: статус проекта
	MsgLogs     = "logs"          // агент -> сервер: поток логов (ответ на logs_req)
	MsgLogsReq  = "logs_req"      // сервер -> агент: запрос логов
	MsgUpdate   = "update"        // сервер -> агент: самообновление агента
	MsgRecon    = "reconcile"     // сервер -> агент: запрос списка контейнеров
	MsgReconRes = "reconcile_res" // агент -> сервер: список контейнеров
	MsgExec     = "exec"          // сервер -> агент: выполнить команду
	MsgPower    = "power"         // сервер -> агент: reboot/shutdown
	MsgShot     = "screenshot"    // сервер -> агент: сделать скриншот
	MsgResult   = "result"        // агент -> сервер: результат exec/power/screenshot
)

// Envelope — конверт сообщений поверх WS (JSON).
type Envelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Hello — регистрация агента.
type Hello struct {
	Hostname   string  `json:"hostname"`
	OS         string  `json:"os"`
	Arch       string  `json:"arch"`
	Version    string  `json:"version"`
	Token      string  `json:"token"`
	MaxMemMB   int     `json:"max_mem_mb,omitempty"`  // лимит RAM ноды (0 = без лимита)
	MaxCPUs    float64 `json:"max_cpus,omitempty"`    // лимит ядер CPU (0 = без лимита)
	MaxDiskGB  int     `json:"max_disk_gb,omitempty"` // лимит диска GB (0 = без лимита)
	HasDocker  bool    `json:"has_docker,omitempty"`
	InstanceID string  `json:"instance_id,omitempty"` // постоянный ID машины (анти-дубль)
}

// ExecTask — выполнить команду на ноде.
type ExecTask struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// PowerTask — управление питанием ноды.
type PowerTask struct {
	Action string `json:"action"` // reboot | shutdown
}

// Result — универсальный результат exec/power/screenshot от агента.
type Result struct {
	RequestID string    `json:"request_id,omitempty"`
	Kind      string    `json:"kind"` // exec | power | screenshot
	Action    string    `json:"action,omitempty"`
	Command   string    `json:"command,omitempty"`
	OK        bool      `json:"ok"`
	Output    string    `json:"output,omitempty"`
	Error     string    `json:"error,omitempty"`
	File      string    `json:"file,omitempty"`       // имя файла на сервере (скриншоты)
	CreatedAt time.Time `json:"created_at,omitempty"` // время выполнения (заполняет сервер)
}

// KindLabel — человекочитаемое название типа результата для журнала.
func (r Result) KindLabel() string {
	switch r.Kind {
	case "exec":
		if r.Action != "" && r.Action != "exec" {
			return "команда " + r.Action
		}
		return "команда"
	case "power":
		return "питание"
	case "screenshot":
		return "снимок экрана"
	case "":
		return "действие"
	}
	return r.Kind
}

// StatusLabel — человекочитаемый статус проекта для журнала.
func (p ProjectStatus) StatusLabel() string {
	switch p.Status {
	case "running":
		return "работает"
	case "stopped":
		return "остановлен"
	case "failed":
		return "ошибка"
	case "starting":
		return "запускается"
	case "":
		return "нет статуса"
	}
	return p.Status
}

// Welcome — ответ сервера.
type Welcome struct {
	NodeID        int64  `json:"node_id"`
	ServerVersion string `json:"server_version"`
}

// Ping — heartbeat агента.
type Ping struct {
	Metrics Metrics `json:"metrics"`
}

// DeployTask — задача запуска проекта на ноде.
type DeployTask struct {
	ProjectID   int64             `json:"project_id"`
	Name        string            `json:"name"`
	Mode        string            `json:"mode,omitempty"` // docker (по умолчанию) | process
	Image       string            `json:"image,omitempty"`
	Command     string            `json:"command,omitempty"`
	Ports       []string          `json:"ports,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Volumes     []string          `json:"volumes,omitempty"`
	Memory      string            `json:"memory,omitempty"`
	CPUs        string            `json:"cpus,omitempty"`
	ArtifactURL string            `json:"artifact_url,omitempty"` // tar.gz с файлами проекта (опц.)
	ArtifactSHA string            `json:"artifact_sha,omitempty"`
	MountPath   string            `json:"mount_path,omitempty"` // куда смонтировать артефакт в контейнере
}

// StopTask — задача остановки/удаления проекта.
type StopTask struct {
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	Remove    bool   `json:"remove"` // docker rm после stop
}

// ProjectStatus — статус проекта от агента.
type ProjectStatus struct {
	ProjectID int64  `json:"project_id"`
	Status    string `json:"status"` // running / stopped / failed
	Container string `json:"container,omitempty"`
	Error     string `json:"error,omitempty"`
}

// LogsRequest — запрос логов контейнера.
type LogsRequest struct {
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	Tail      string `json:"tail,omitempty"` // напр. "200" или "all"
	Follow    bool   `json:"follow"`
}

// LogsChunk — порция логов.
type LogsChunk struct {
	ProjectID int64  `json:"project_id"`
	Data      string `json:"data"`
	Done      bool   `json:"done"`
}

// ReconResult — фактическое состояние ноды: docker-контейнеры + process-проекты.
//
// v0.6.0: раньше агент отдавал только docker-контейнеры, поэтому для нод без
// Docker (process-режим, типичная Windows-машина за NAT) drift-детекция была
// слепой: упавший процесс числился running навсегда. Теперь агент сообщает и
// про процессы (pid + живость), и сервер сверяет оба вида.
type ReconResult struct {
	Containers []ContainerInfo `json:"containers"`
	Processes  []ProcessInfo   `json:"processes,omitempty"`
}

// ContainerInfo — информация о контейнере.
type ContainerInfo struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Status  string `json:"status"`
	Project string `json:"project,omitempty"`
}

// ProcessInfo — информация о process-проекте (mode: process, без Docker).
type ProcessInfo struct {
	Name    string `json:"name"`    // имя проекта (без префикса swag_)
	Unit    string `json:"unit"`    // каталог/имя юнита (совпадает с containerName)
	PID     int    `json:"pid"`     // pid процесса (0 = неизвестно)
	Alive   bool   `json:"alive"`   // процесс жив
	Started string `json:"started"` // RFC3339 старта (пусто = не запущен)
}
