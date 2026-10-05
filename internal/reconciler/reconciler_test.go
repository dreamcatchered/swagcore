package reconciler

// Тесты на failover: проект, у которого нода выпала из placement,
// ОБЯЗАН быть остановлен на этой ноде.
//
// БАГ (v0.7.0, найден при проверке миграции primegrind dream -> angelica):
// ветка «failover» только обновляла запись в БД (InstStopped) и НЕ отправляла
// команду stop на ноду. Платформа рапортовала «stopped», а процесс на
// исключённой ноде продолжал работать, держал порт и ел ресурсы — и после
// возврата ноды в онлайн проект оказывался запущен в двух местах сразу.
//
// Проверяем это через fakeNode: если Stop не вызвали — тест падает.

import (
	"encoding/json"
	"testing"

	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/store"
)

// fakeNode — заглушка хаба: записывает отправленные команды.
type fakeNode struct {
	online    map[int64]bool
	deployed  []model.DeployTask
	stopped   []model.StopTask
	stopCalls int
}

func (f *fakeNode) IsOnline(id int64) bool { return f.online[id] }

func (f *fakeNode) Deploy(nodeID int64, task model.DeployTask) error {
	f.deployed = append(f.deployed, task)
	return nil
}

func (f *fakeNode) Stop(nodeID int64, task model.StopTask) error {
	f.stopCalls++
	f.stopped = append(f.stopped, task)
	return nil
}

func (f *fakeNode) RequestReconcile(nodeID int64) error { return nil }

func (f *fakeNode) SetDriftCallback(fn func(drift [][2]int64)) {}

// newTestReconciler собирает реконсилер с фейковым хабом.
func newTestReconciler(st *store.Store, f *fakeNode) *Reconciler {
	return New(st, f)
}

// ---- минимальный интерфейс, который использует Reconciler ----

// Стоп-фейковер собирает два узла: 5=angelica, 6=dream.
func seedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	for _, hn := range []string{"angelica", "dream"} {
		if _, err := st.UpsertNodeByToken("hash-"+hn, hn, "windows", "amd64", "0.7.0", "10.0.0.1", false, 0, 0, 0, "inst-"+hn); err != nil {
			t.Fatalf("UpsertNodeByToken(%s): %v", hn, err)
		}
		// второй вызов обновляет существующую запись и ставит status='online'
		// (именно так нода становится online в проде — при подключении).
		if _, err := st.UpsertNodeByToken("hash-"+hn, hn, "windows", "amd64", "0.7.0", "10.0.0.1", false, 0, 0, 0, "inst-"+hn); err != nil {
			t.Fatalf("UpsertNodeByToken online(%s): %v", hn, err)
		}
		if err := st.SetNodeEnabled(nodeIDByName(t, st, hn), true); err != nil {
			t.Fatalf("SetNodeEnabled(%s): %v", hn, err)
		}
	}
	return st
}

func nodeIDByName(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	nodes, err := st.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Hostname == name {
			return n.ID
		}
	}
	t.Fatalf("нода %q не найдена", name)
	return 0
}

// РЕГРЕССИЯ: после сужения placement до [angelica] на dream ОБЯЗАН уйти
// реальный stop, а не только запись в БД.
func TestFailover_SendsStopToExcludedNode(t *testing.T) {
	st := seedStore(t)
	angelica := nodeIDByName(t, st, "angelica")
	dream := nodeIDByName(t, st, "dream")

	p, err := st.CreateProject("primegrind", model.Manifest{
		Name:      "primegrind",
		Mode:      "process",
		Command:   "app.exe",
		Placement: model.PlacementSelected,
		Nodes:     []int64{angelica, dream},
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// CreateProject создаёт проект в desired_stopped («ещё не запускали»);
	// реальный деплой переводит его в desired_running. Без этого тик
	// проект пропустит — он считается остановленным владельцем.
	if err := st.SetProjectStatus(p.ID, model.StatusDesiredRunning, "", ""); err != nil {
		t.Fatalf("SetProjectStatus: %v", err)
	}

	f := &fakeNode{online: map[int64]bool{angelica: true, dream: true}}
	r := newTestReconciler(st, f)

	// 1-й тик: деплой на обе ноды
	r.tick()
	if len(f.deployed) != 2 {
		t.Fatalf("ожидался деплой на 2 ноды, получено %d", len(f.deployed))
	}

	// сужаем placement до одной ноды
	if err := st.UpdateManifest(p.ID, model.Manifest{
		Name:      "primegrind",
		Mode:      "process",
		Command:   "app.exe",
		Placement: model.PlacementSelected,
		Nodes:     []int64{angelica},
	}); err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}

	// 2-й тик: dream вне placement -> обязан получить stop
	r.tick()

	if f.stopCalls == 0 {
		t.Fatal("stop не отправлен ни разу — процесс на исключённой ноде " +
			"продолжит работать, а платформа покажет «stopped»")
	}
	found := false
	for _, s := range f.stopped {
		if s.Name == "primegrind" && s.ProjectID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("stop отправлен не для того проекта: %+v", f.stopped)
	}
}


// РЕГРЕССИЯ (JSON): команда stop должна доезжать до агента в нужном виде.
func TestStopTaskJSONShape(t *testing.T) {
	raw, err := json.Marshal(model.StopTask{ProjectID: 7, Name: "primegrind"})
	if err != nil {
		t.Fatal(err)
	}
	var back model.StopTask
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.ProjectID != 7 || back.Name != "primegrind" {
		t.Fatalf("StopTask не пережил JSON: %+v", back)
	}
	if back.Remove {
		t.Fatal("Remove по умолчанию должен быть false — иначе failover удалит каталог")
	}
}

// РЕГРЕССИЯ (вторая итерация фикса): запись InstStopped в БД НЕ является
// доказательством, что процесс на ноде остановлен.
//
// Первая версия фикса добавила «не спамить stop уже остановленной ноды».
// Из-за этого вылез прямо противоположный баг: старый код уже успевал
// записать InstStopped, не отправив stop, и реконсилер больше никогда не
// посылал команду — осиротевший процесс держал порт бесконечно.
//
// Поэтому stop обязан уходить на каждый тик, пока нода вне placement.
func TestFailover_StopSentEvenIfStatusStopped(t *testing.T) {
	st := seedStore(t)
	angelica := nodeIDByName(t, st, "angelica")
	dream := nodeIDByName(t, st, "dream")

	p, err := st.CreateProject("primegrind", model.Manifest{
		Name: "primegrind", Mode: "process", Command: "app.exe",
		Placement: model.PlacementSelected, Nodes: []int64{angelica, dream},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectStatus(p.ID, model.StatusDesiredRunning, "", ""); err != nil {
		t.Fatal(err)
	}

	f := &fakeNode{online: map[int64]bool{angelica: true, dream: true}}
	r := newTestReconciler(st, f)
	r.tick()

	// сужаем placement до angelica
	if err := st.UpdateManifest(p.ID, model.Manifest{
		Name: "primegrind", Mode: "process", Command: "app.exe",
		Placement: model.PlacementSelected, Nodes: []int64{angelica},
	}); err != nil {
		t.Fatal(err)
	}
	r.tick()

	if f.stopCalls == 0 {
		t.Fatal("stop не отправлен")
	}
	// база теперь говорит stopped — но stop всё равно должен уходить
	insts, err := st.InstancesOfProject(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	dreamStopped := false
	for _, in := range insts {
		if in.NodeID == dream && in.Status == model.InstStopped {
			dreamStopped = true
		}
	}
	if !dreamStopped {
		t.Fatal("в БД инстанс на dream не помечен stopped — тест не проверяет то, что думает")
	}

	// ещё два тика: stop обязан продолжать уходить
	before := f.stopCalls
	r.tick()
	r.tick()
	if f.stopCalls <= before {
		t.Fatalf("после того как БД уже говорит stopped, stop перестал уходить "+
			"(было %d, стало %d) — процесс на ноде снова останется висеть", before, f.stopCalls)
	}
}
