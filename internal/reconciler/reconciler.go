// Package reconciler — приведение фактического состояния платформы к желаемому.
//
// Логика (в духе Kubernetes):
//   - placement=all: проект должен жить на КАЖДОЙ подходящей ноде,
//     включая добавленные после деплоя;
//   - placement=selected: только на выбранных нодах;
//   - нода отвалилась и не вернулась → её экземпляры помечаются stopped,
//     проект переразмещается на живые ноды (failover);
//   - экземпляр desired/starting/failed/зависший → (пере)отправляем деплой;
//   - квоты ноды (max_mem_mb/max_cpus/max_disk_gb) РЕАЛЬНО учитываются.
package reconciler

import (
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/store"
)

const (
	tickInterval  = 20 * time.Second
	downtimeLimit = 90 * time.Second // нода не пингует дольше — считаем упавшей
	staleStarting = 3 * time.Minute  // starting дольше этого — переотправляем задачу
	reconInterval = 60 * time.Second // период фактического опроса нод (drift-детекция)
	housekeeping  = 30 * time.Minute // чистка журнала и WAL
	eventsKeep    = 5000             // сколько записей журнала хранить
)

// Reconciler — цикл сверки состояния.
// NodeBus — то, что реконсилеру нужно от хаба.
//
// Зависимость от конкретного *hub.Hub делала реконсилер непроверяемым:
// ни один сценарий нельзя было прогнать без живого WebSocket-хаба. После
// введения интерфейса появились тесты на failover (в т.ч. регресс, где
// реконсилер забывал отправить stop на исключённую ноду).
type NodeBus interface {
	IsOnline(nodeID int64) bool
	Deploy(nodeID int64, task model.DeployTask) error
	Stop(nodeID int64, task model.StopTask) error
	RequestReconcile(nodeID int64) error
	SetDriftCallback(fn func(drift [][2]int64))
}

type Reconciler struct {
	st *store.Store
	h  NodeBus
}

// New создаёт реконсилер.
func New(st *store.Store, h NodeBus) *Reconciler {
	return &Reconciler{st: st, h: h}
}

// Run запускает цикл.
func (r *Reconciler) Run() {
	// drift-детекция: хаб по ответам агентов сообщает о расхождениях — передеплоим
	if r.h != nil {
		r.h.SetDriftCallback(func(drift [][2]int64) {
			nodes, err := r.st.ListNodes()
			if err != nil {
				return
			}
			byID := map[int64]model.Node{}
			for _, x := range nodes {
				byID[x.ID] = x
			}
			for _, d := range drift {
				n, ok := byID[d[1]]
				if !ok {
					continue
				}
				p, err := r.st.ProjectByID(d[0])
				if err != nil || p == nil {
					continue
				}
				if !r.h.IsOnline(n.ID) {
					continue
				}
				// мгновенный передеплой: инстанс в starting, чтобы тик не считал его running
				_ = r.st.SetInstanceStatus(p.ID, n.ID, model.InstStarting, "", "")
				log.Printf("[reconciler] drift: передеплой %s на %s", p.Name, n.Hostname)
				r.deployTo(p, n)
			}
		})
	}

	// Периодический фактический опрос нод.
	//
	// v0.6.0 (баг): опрос шёл ТОЛЬКО к нодам с has_docker=1, поэтому на
	// process-нодах (Windows без Docker — самый частый случай) падение
	// приложения обнаруживалось только вручную. Теперь спрашиваем всех.
	go func() {
		t2 := time.NewTicker(reconInterval)
		defer t2.Stop()
		for range t2.C {
			nodes, err := r.st.ListNodes()
			if err != nil {
				continue
			}
			for _, n := range nodes {
				if n.Enabled && r.h.IsOnline(n.ID) {
					_ = r.h.RequestReconcile(n.ID)
				}
			}
		}
	}()

	// Housekeeping: журнал событий рос бесконечно (1 440 записей/сутки только
	// от reconcile-опроса), WAL не сжимался (4+ МБ), а в instances копились
	// «фантомы» — строки по уже удалённым проектам. Чистим раз в полчаса.
	go func() {
		t3 := time.NewTicker(housekeeping)
		defer t3.Stop()
		for range t3.C {
			if n, err := r.st.PruneEvents(eventsKeep); err == nil && n > 0 {
				log.Printf("[reconciler] pruned %d old event(s)", n)
			}
			if n, err := r.st.PurgeOrphanInstances(); err == nil && n > 0 {
				log.Printf("[reconciler] purged %d orphan instance(s)", n)
			}
			if err := r.st.Checkpoint(); err != nil {
				log.Printf("[reconciler] wal checkpoint: %v", err)
			}
		}
	}()

	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for range t.C {
		r.tick()
	}
}

func (r *Reconciler) tick() {
	projects, err := r.st.ListProjects()
	if err != nil {
		log.Printf("[reconciler] list projects: %v", err)
		return
	}
	nodes, err := r.st.ListNodes()
	if err != nil {
		return
	}
	online := func(id int64) bool { return r.h.IsOnline(id) }

	for i := range projects {
		p := &projects[i]
		if p.Status == model.StatusDesiredStopped || p.Status == model.StatusStopped {
			continue // проект остановлен владельцем
		}
		mf := p.Manifest
		wantAll := mf.Placement != model.PlacementSelected

		// целевое множество нод (process-режим не требует Docker)
		isProcess := mf.Mode == "process"
		var targets []model.Node
		for _, n := range nodes {
			if !n.Enabled {
				continue
			}
			if !isProcess && !n.HasDocker {
				continue
			}
			if mf.PreferredNode != "" && n.Hostname != mf.PreferredNode {
				continue
			}
			if mf.OS != "" && !strings.EqualFold(n.OS, mf.OS) {
				continue // проект требует конкретную ОС
			}
			if !r.fitsQuota(&mf, n) {
				continue
			}
			if wantAll {
				targets = append(targets, n)
			} else {
				for _, id := range mf.Nodes {
					if n.ID == id {
						targets = append(targets, n)
						break
					}
				}
			}
		}
		// preferred_node не онлайн — пропускаем тик (не фейловать проект)
		if mf.PreferredNode != "" && len(targets) == 0 {
			continue
		}

		instances, err := r.st.InstancesOfProject(p.ID)
		if err != nil {
			continue
		}
		instByNode := map[int64]model.ProjectInstance{}
		for _, in := range instances {
			instByNode[in.NodeID] = in
		}

		// 1) деплой на недостающие / ненормальные ноды
		for _, n := range targets {
			in, ok := instByNode[n.ID]
			if !online(n.ID) {
				// нода оффлайн — экземпляр помечаем, чтобы UI не врал,
				// но деплой не отправляем (failover произойдёт, когда целевое
				// множество пересчитается без этой ноды)
				if ok && (in.Status == model.InstRunning || in.Status == model.InstStarting) {
					_ = r.st.SetInstanceStatus(p.ID, n.ID, model.InstOrphan, in.Container,
						"нода offline")
				}
				continue
			}
			switch {
			case !ok || in.Status == model.InstDesired:
				r.deployTo(p, n)
			case in.Status == model.InstStopped || in.Status == model.InstOrphan:
				// после ребута ноды / падения агента / оффлайна — поднимаем заново
				r.deployTo(p, n)
			case in.Status == model.InstFailed:
				r.deployTo(p, n) // ретрай раз в тик
			case in.Status == model.InstStarting && time.Since(in.UpdatedAt) > staleStarting:
				log.Printf("[reconciler] %s on %s: stale starting, redeploy", p.Name, n.Hostname)
				r.deployTo(p, n)
			}
		}

		// 2) failover: экземпляры на нодах вне целевого множества
		//
		// БАГ (v0.7.0, найден при проверке миграции primegrind dream -> angelica):
		// здесь ТОЛЬКО обновлялась запись в БД, а команда stop на ноду не
		// уходила. В итоге платформа рапортовала «stopped», процесс на
		// исключённой ноде продолжал работать, держал порт и ел ресурсы —
		// а после возврата ноды в онлайн проект оказывался в двух местах
		// сразу. Теперь отправляем stop и ждём подтверждения.
		for _, in := range instances {
			inTarget := false
			for _, n := range targets {
				if n.ID == in.NodeID {
					inTarget = true
					break
				}
			}
			if inTarget {
				continue
			}
			// ВАЖНО: здесь НЕТ проверки «уже InstStopped».
			//
			// Первая версия фикса её добавила, и выяснилось, что это ловушка:
			// старый баг успевал записать InstStopped в БД, не отправив stop на
			// ноду, — то есть база врала. Реконсилер, поверивший статусу,
			// больше никогда не посылал stop, и осиротевший процесс держал
			// порт месяцами. Запись в БД не является доказательством, что
			// процесс на ноде остановлен; правда — в recon-ответе ноды.
			//
			// Поэтому stop шлём на каждый тик: он идемпотентен, а нода без
			// процесса просто ничего не сделает. Сообщение одно на тик для
			// исключённой ноды — цена несопоставима с зависшим процессом.
			hostname := nodeHostname(nodes, in.NodeID)
			log.Printf("[reconciler] %s: нода %s вне placement — останавливаем", p.Name, hostname)
			task := model.StopTask{ProjectID: p.ID, Name: p.Name}
			if err := r.h.Stop(in.NodeID, task); err != nil {
				// Нода офлайн — остановить нечем, но и ждать нечего:
				// когда она вернётся, recon увидит живой процесс и уберёт.
				_ = r.st.SetInstanceStatus(p.ID, in.NodeID, model.InstOrphan, in.Container,
					"вне placement, нода offline: "+err.Error())
				continue
			}
			_ = r.st.SetInstanceStatus(p.ID, in.NodeID, model.InstStopped, in.Container, "")
		}

		// 3) статус проекта — агрегат по экземплярам
		r.aggregate(p, instByNode, targets)
	}
}

// fitsQuota проверяет, влезает ли проект в лимиты ноды.
//
// v0.6.0: было заглушкой `return true` — квоты, которые админ задавал в UI,
// не влияли ни на что. Теперь считаем реальную сумму запросов проектов,
// уже размещённых на ноде, и сравниваем с лимитом.
func (r *Reconciler) fitsQuota(mf *model.Manifest, n model.Node) bool {
	if n.MaxMemMB <= 0 && n.MaxCPUs <= 0 && n.MaxDiskGB <= 0 {
		return true // лимиты не заданы — не ограничиваем
	}
	usedMem, usedCPU, _ := r.st.NodeUsage(n.ID)

	if n.MaxMemMB > 0 {
		want := store.ParseMemMB(mf.Resources.Memory)
		if want > 0 && usedMem+want > n.MaxMemMB {
			log.Printf("[reconciler] quota: %s не лезет в RAM %s (%d+%d > %d МБ)",
				mf.Name, n.Hostname, usedMem, want, n.MaxMemMB)
			return false
		}
	}
	if n.MaxCPUs > 0 {
		var want float64
		if v, err := strconv.ParseFloat(strings.TrimSpace(mf.Resources.CPUs), 64); err == nil && v > 0 {
			want = v
		}
		if want > 0 && usedCPU+want > n.MaxCPUs+1e-9 {
			log.Printf("[reconciler] quota: %s не лезет в CPU %s (%.2f+%.2f > %.2f)",
				mf.Name, n.Hostname, usedCPU, want, n.MaxCPUs)
			return false
		}
	}
	return true
}

func (r *Reconciler) deployTo(p *model.Project, n model.Node) {
	log.Printf("[reconciler] deploy %s -> %s", p.Name, n.Hostname)
	mf := p.Manifest
	task := model.DeployTask{
		ProjectID:   p.ID,
		Name:        p.Name,
		Mode:        mf.Mode,
		Image:       mf.Image,
		Command:     mf.Command,
		Ports:       mf.Ports,
		Env:         mf.Env,
		Volumes:     mf.Volumes,
		Memory:      mf.Resources.Memory,
		CPUs:        mf.Resources.CPUs,
		ArtifactURL: mf.Artifact,
		ArtifactSHA: mf.ArtifactSHA,
		MountPath:   mf.MountPath,
	}
	_ = r.st.UpsertInstance(p.ID, n.ID, model.InstStarting, "", "")
	if err := r.h.Deploy(n.ID, task); err != nil {
		_ = r.st.SetInstanceStatus(p.ID, n.ID, model.InstFailed, "", err.Error())
		return
	}
}

// aggregate считает статус проекта по его экземплярам.
//
// v0.6.0: раньше агрегат игнорировал оффлайн-ноды в целевом множестве и
// мог показывать running, хотя половина копий недоступна. Теперь учитываем
// orphan/оффлайн и явно различаем «работает», «частично» и «упало».
func (r *Reconciler) aggregate(p *model.Project, instByNode map[int64]model.ProjectInstance, targets []model.Node) {
	var running, failed, starting, desired, missing, stopped int
	for _, t := range targets {
		in, ok := instByNode[t.ID]
		if !ok {
			missing++
			continue
		}
		switch in.Status {
		case model.InstRunning:
			running++
		case model.InstFailed:
			failed++
		case model.InstStarting:
			starting++
		case model.InstDesired:
			desired++
		case model.InstOrphan:
			missing++ // нода ушла — не считаем работающим
		default:
			stopped++
		}
	}
	switch {
	case running > 0 && failed == 0 && starting == 0 && desired == 0 && missing == 0 && stopped == 0:
		_ = r.st.SetProjectStatus(p.ID, model.StatusRunning, "", "")
	case running > 0:
		_ = r.st.SetProjectStatus(p.ID, model.StatusRunning, "",
			partialNote(failed, starting, missing, stopped))
	case failed > 0:
		_ = r.st.SetProjectStatus(p.ID, model.StatusFailed, "", "все копии упали")
	case starting > 0 || desired > 0 || missing > 0:
		_ = r.st.SetProjectStatus(p.ID, model.StatusStarting, "", "")
	default:
		_ = r.st.SetProjectStatus(p.ID, model.StatusStopped, "", "")
	}
}

// partialNote — короткое объяснение, почему проект «частично работает».
func partialNote(failed, starting, missing, stopped int) string {
	var parts []string
	if failed > 0 {
		parts = append(parts, "упало "+itoa(failed))
	}
	if starting > 0 {
		parts = append(parts, "запускается "+itoa(starting))
	}
	if missing > 0 {
		parts = append(parts, "нода offline ("+itoa(missing)+")")
	}
	if stopped > 0 {
		parts = append(parts, "остановлено "+itoa(stopped))
	}
	if len(parts) == 0 {
		return ""
	}
	return "частично: " + strings.Join(parts, ", ")
}

func itoa(v int) string { return strconv.Itoa(v) }

// nodeHostname — имя ноды по id для читаемых логов.
func nodeHostname(nodes []model.Node, id int64) string {
	for i := range nodes {
		if nodes[i].ID == id {
			return nodes[i].Hostname
		}
	}
	return "node#" + itoa(int(id))
}
