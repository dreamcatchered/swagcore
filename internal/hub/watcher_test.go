package hub

// Тесты наблюдателя за нодами.
//
// ПРОБЛЕМА (v0.7.0, нода angelica): падение ноды было полностью немым —
// SweepOffline писал в лог «swept 1 offline node(s)» без имени и больше никуда
// не сообщал. Про пропавший компьютер можно было не знать сутки.

import (
	"strings"
	"testing"
	"time"

	"github.com/dreamcatchered/swagcore/internal/store"
)

// newTestStore — store с двумя нодами.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, hn := range []string{"dream", "angelica"} {
		if _, err := st.UpsertNodeByToken("h-"+hn, hn, "windows", "amd64", "0.7.0", "10.0.0.1", false, 0, 0, 0, "i-"+hn); err != nil {
			t.Fatalf("UpsertNodeByToken(%s): %v", hn, err)
		}
	}
	return st
}

func nodeID(t *testing.T, st *store.Store, name string) int64 {
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
	t.Fatalf("нет ноды %q", name)
	return 0
}

// РЕГРЕССИЯ: переход online -> offline обязан дать событие в панели.
func TestWatcher_AlertsOnNodeDown(t *testing.T) {
	st := newTestStore(t)
	angelica := nodeID(t, st, "angelica")

	w := newWatcher(st, &Hub{st: st})

	// 1-й тик: запоминаем текущие статусы (все online)
	w.check()

	// нода пропала
	st.NodeOffline(angelica)
	w.check()

	evs, err := st.RecentEvents(50, "node")
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	found := false
	for _, e := range evs {
		if strings.Contains(e.Message, "angelica") && strings.Contains(e.Message, "перестала отвечать") {
			found = true
		}
	}
	if !found {
		t.Fatalf("падение ноды не попало в события панели — про неё можно не узнать. События: %v", evs)
	}
}

// РЕГРЕССИЯ: сообщение обязано называть ноду, а не «swept 1 offline».
func TestWatcher_EventNamesTheNode(t *testing.T) {
	st := newTestStore(t)
	w := newWatcher(st, &Hub{st: st})
	w.check()
	st.NodeOffline(nodeID(t, st, "dream"))
	w.check()

	evs, _ := st.RecentEvents(50, "node")
	for _, e := range evs {
		if strings.Contains(e.Message, "перестала отвечать") && !strings.Contains(e.Message, "dream") {
			t.Fatalf("в алерте нет имени ноды: %q", e.Message)
		}
	}
}

// Возвращение ноды — тоже событие (иначе непонятно, что ремонт сработал).
func TestWatcher_AlertsOnNodeUp(t *testing.T) {
	st := newTestStore(t)
	dream := nodeID(t, st, "dream")

	w := newWatcher(st, &Hub{st: st})
	st.NodeOffline(dream)
	w.check() // запомнили offline

	// вернулась
	_, _ = st.UpsertNodeByToken("h-dream", "dream", "windows", "amd64", "0.7.0", "10.0.0.1", false, 0, 0, 0, "i-dream")
	w.check()

	evs, _ := st.RecentEvents(50, "node")
	back := false
	for _, e := range evs {
		if strings.Contains(e.Message, "снова на связи") {
			back = true
		}
	}
	if !back {
		t.Fatalf("возвращение ноды не отмечено событием. События: %v", evs)
	}
}

// Повторные тики без изменений не должны плодить события.
func TestWatcher_NoAlertStorm(t *testing.T) {
	st := newTestStore(t)
	w := newWatcher(st, &Hub{st: st})
	w.check()
	st.NodeOffline(nodeID(t, st, "dream"))
	w.check()

	before, _ := st.RecentEvents(200, "node")
	for i := 0; i < 5; i++ {
		w.check()
	}
	after, _ := st.RecentEvents(200, "node")
	if len(after) != len(before) {
		t.Fatalf("алерты сыпятся без изменений: было %d, стало %d", len(before), len(after))
	}
}


// humanSince — человекочитаемый интервал.
func TestHumanSince(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30 с"},
		{5 * time.Minute, "5 мин"},
		{3 * time.Hour, "3 ч"},
	}
	for _, c := range cases {
		if got := humanSince(c.d); got != c.want {
			t.Errorf("humanSince(%v)=%q, ожидалось %q", c.d, got, c.want)
		}
	}
}



// РЕГРЕССИЯ: узлы, офлайн на момент старта платформы, тоже должны попасть
// в алерт.
//
// Проблема (v0.7.0, angelica): watcher ловит только ПЕРЕХОДЫ. Платформу
// перезапустили, когда нода уже была офлайн, — prev заполнился офлайн-состоянием,
// перехода не произошло, и алерта не было вообще. Именно так мы и не узнали
// про пропавшую ноду.
func TestWatcher_AlertsAboutNodesAlreadyOfflineAtStartup(t *testing.T) {
	st := newTestStore(t)
	st.NodeOffline(nodeID(t, st, "angelica"))

	w := newWatcher(st, &Hub{st: st})
	w.check() // первый тик — раньше просто запоминал и молчал

	evs, _ := st.RecentEvents(50, "node")
	found := false
	for _, e := range evs {
		if strings.Contains(e.Message, "при запуске") && strings.Contains(e.Message, "angelica") {
			found = true
		}
	}
	if !found {
		t.Fatalf("нода, офлайн на момент старта, не попала в алерт. События: %v", evs)
	}
}