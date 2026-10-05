// Package sched — планировщик: выбирает ноду для деплоя проекта.
package sched

import (
	"fmt"
	"math"

	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/store"
)

// PickNode выбирает ноду для проекта.
//
// Правила (в порядке приоритета):
//  1. preferred_node — точное совпадение по имени хоста, иначе ошибка;
//  2. placement=selected — ТОЛЬКО ноды из mf.Nodes, иначе ошибка;
//  3. иначе least-loaded по свободной RAM среди online;
//  4. нода без метрик считается последней.
//
// БАГ (v0.7.0, закрыт тестами internal/sched/sched_test.go): placement и
// mf.Nodes здесь полностью игнорировались. Пользователь выбирал в манифесте
// конкретные ноды, реконсилер это уважал — а первичный деплой всё равно
// уезжал на least-loaded, то есть на ноду, которую явно исключили.
func PickNode(st *store.Store, mf model.Manifest, online func(nodeID int64) bool) (*model.Node, error) {
	nodes, err := st.ListNodes()
	if err != nil {
		return nil, err
	}

	// 1. preferred_node по имени хоста
	if mf.PreferredNode != "" {
		for i := range nodes {
			if nodes[i].Hostname == mf.PreferredNode {
				if online(nodes[i].ID) {
					return &nodes[i], nil
				}
				return nil, fmt.Errorf("preferred node %q is offline", mf.PreferredNode)
			}
		}
		return nil, fmt.Errorf("preferred node %q not found", mf.PreferredNode)
	}

	// 2. явный список нод
	if mf.Placement == model.PlacementSelected {
		if len(mf.Nodes) == 0 {
			return nil, fmt.Errorf("placement=selected but no nodes listed")
		}
		want := make(map[int64]bool, len(mf.Nodes))
		for _, id := range mf.Nodes {
			want[id] = true
		}
		// Среди разрешённых нод выбираем так же, как при placement=all —
		// наименее загруженную, чтобы явный выбор не означал «всегда первую».
		var best *model.Node
		bestScore := math.MaxFloat64
		for i := range nodes {
			n := &nodes[i]
			if !want[n.ID] || !n.Enabled {
				continue
			}
			if n.Status != "online" || !online(n.ID) {
				continue
			}
			if score := nodeScore(n); score < bestScore {
				bestScore, best = score, n
			}
		}
		if best == nil {
			return nil, fmt.Errorf("none of the selected nodes are online (nodes=%v)", mf.Nodes)
		}
		return best, nil
	}

	// 3. любая онлайн-нода, наименее загруженная
	var best *model.Node
	bestScore := math.MaxFloat64
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || n.Status != "online" || !online(n.ID) {
			continue
		}
		if score := nodeScore(n); score < bestScore {
			bestScore, best = score, n
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no online nodes available")
	}
	return best, nil
}

// nodeScore — чем меньше, тем лучше. Нода без метрик уходит в конец.
func nodeScore(n *model.Node) float64 {
	score := 1e18
	if m := n.Metrics; m != nil && m.MemTotalMB > 0 {
		memUsedPct := float64(m.MemUsedMB) / float64(m.MemTotalMB)
		score = memUsedPct * 100
		// бонус: меньше контейнеров — чуть приоритетнее
		score += float64(m.Containers) * 0.5
	}
	return score
}
