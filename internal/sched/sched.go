// Package sched — планировщик: выбирает ноду для деплоя проекта.
package sched

import (
	"fmt"
	"math"

	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/store"
)

// PickNode выбирает ноду для проекта.
// Правила:
//  1. если в манифесте указан preferred_node — берём её (если онлайн);
//  2. иначе least-loaded по свободной RAM (среди online);
//  3. нода без метрик считается последней.
func PickNode(st *store.Store, mf model.Manifest, online func(nodeID int64) bool) (*model.Node, error) {
	nodes, err := st.ListNodes()
	if err != nil {
		return nil, err
	}
	// preferred node по имени хоста
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

	var best *model.Node
	bestScore := math.MaxFloat64
	for i := range nodes {
		n := &nodes[i]
		if n.Status != "online" || !online(n.ID) {
			continue
		}
		score := 1e18 // нода без метрик — в конец
		if m := n.Metrics; m != nil && m.MemTotalMB > 0 {
			memUsedPct := float64(m.MemUsedMB) / float64(m.MemTotalMB)
			score = memUsedPct * 100
			// бонус: меньше контейнеров — чуть приоритетнее
			score += float64(m.Containers) * 0.5
		}
		if score < bestScore {
			bestScore = score
			best = n
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no online nodes available")
	}
	return best, nil
}
