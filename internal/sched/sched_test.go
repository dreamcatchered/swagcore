package sched

// Тесты планировщика.
//
// БАГ, который закрывают эти тесты (v0.7.0): PickNode 完全 игнорировал
// Manifest.Placement и Manifest.Nodes. Манифест и UI давали возможность
// выбрать конкретные ноды, реконсилер это понимал — а планировщик при
// первичном деплое всё равно выбирал least-loaded и мог поставить проект
// на ноду, которую пользователь явно исключил. Воспроизводится ниже.

import (
	"testing"

	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/store"
)

// testStore поднимает store на временной директории с тремя нодами:
// dream (1), angelica (2), spare (3). Все онлайн.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// БД держит файл открытым — без Close t.TempDir() не сможет убрать
	// каталог, и тест падал бы на мусоре, а не на своей проверке.
	t.Cleanup(func() { _ = st.Close() })
	for i, hn := range []string{"dream", "angelica", "spare"} {
		if _, err := st.UpsertNodeByToken("hash-"+hn, hn, "windows", "amd64", "0.7.0", "10.0.0."+string(rune('1'+i)), false, 0, 0, 0, "inst-"+hn); err != nil {
			t.Fatalf("UpsertNodeByToken(%s): %v", hn, err)
		}
	}
	return st
}

func allOnline(int64) bool { return true }

// РЕГРЕССИЯ: placement=selected + nodes=[2] обязан выбрать angelica (id 2),
// а не least-loaded.
func TestPickNode_HonorsPlacementSelected(t *testing.T) {
	st := testStore(t)

	mf := model.Manifest{
		Name:      "worker",
		Mode:      "process",
		Placement: model.PlacementSelected,
		Nodes:     []int64{2},
	}

	n, err := PickNode(st, mf, allOnline)
	if err != nil {
		t.Fatalf("PickNode: %v", err)
	}
	if n.Hostname != "angelica" {
		t.Fatalf("placement=selected nodes=[2]: получена нода %q (id=%d), ожидалась angelica (id=2)", n.Hostname, n.ID)
	}
}

// placement=selected с несколькими нодами — берём одну из указанных.
func TestPickNode_PlacementSelectedMultiple(t *testing.T) {
	st := testStore(t)
	mf := model.Manifest{
		Name:      "worker",
		Mode:      "process",
		Placement: model.PlacementSelected,
		Nodes:     []int64{1, 3},
	}
	n, err := PickNode(st, mf, allOnline)
	if err != nil {
		t.Fatalf("PickNode: %v", err)
	}
	if n.ID != 1 && n.ID != 3 {
		t.Fatalf("получена нода id=%d (%q), ожидалась одна из 1,3", n.ID, n.Hostname)
	}
}

// placement=selected, но ни одна из выбранных нод не онлайн — ошибка, а не
// молчаливый деплой на постороннюю ноду.
func TestPickNode_PlacementSelectedAllOffline(t *testing.T) {
	st := testStore(t)
	mf := model.Manifest{
		Name:      "worker",
		Mode:      "process",
		Placement: model.PlacementSelected,
		Nodes:     []int64{2},
	}
	_, err := PickNode(st, mf, func(id int64) bool { return false })
	if err == nil {
		t.Fatal("ожидалась ошибка: выбранные ноды офлайн, деплоить некуда")
	}
}

// placement=selected с пустым списком nodes — ошибка (иначе проект уедет на
// случайную ноду вопреки явному выбору).
func TestPickNode_PlacementSelectedEmptyNodes(t *testing.T) {
	st := testStore(t)
	mf := model.Manifest{
		Name:      "worker",
		Mode:      "process",
		Placement: model.PlacementSelected,
		Nodes:     nil,
	}
	if _, err := PickNode(st, mf, allOnline); err == nil {
		t.Fatal("ожидалась ошибка: placement=selected без списка nodes — нельзя выбрать ноду")
	}
}

// placement=all (пусто) — любая онлайн-нода.
func TestPickNode_PlacementAllPicksAny(t *testing.T) {
	st := testStore(t)
	mf := model.Manifest{Name: "worker", Mode: "process"}
	n, err := PickNode(st, mf, allOnline)
	if err != nil {
		t.Fatalf("PickNode: %v", err)
	}
	if n.ID < 1 || n.ID > 3 {
		t.Fatalf("получена неизвестная нода id=%d", n.ID)
	}
}

// preferred_node имеет приоритет над placement.
func TestPickNode_PreferredNodeWins(t *testing.T) {
	st := testStore(t)
	mf := model.Manifest{
		Name:          "worker",
		Mode:          "process",
		Placement:     model.PlacementSelected,
		Nodes:         []int64{1},
		PreferredNode: "spare",
	}
	n, err := PickNode(st, mf, allOnline)
	if err != nil {
		t.Fatalf("PickNode: %v", err)
	}
	if n.Hostname != "spare" {
		t.Fatalf("preferred_node=spare: получена %q", n.Hostname)
	}
}

// Нет онлайн-ног — ошибка.
func TestPickNode_NoOnlineNodes(t *testing.T) {
	st := testStore(t)
	mf := model.Manifest{Name: "worker", Mode: "process"}
	if _, err := PickNode(st, mf, func(int64) bool { return false }); err == nil {
		t.Fatal("ожидалась ошибка: нет онлайн-ног")
	}
}
