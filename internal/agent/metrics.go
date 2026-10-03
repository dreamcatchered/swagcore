// Сбор метрик хоста без внешних зависимостей.
// Реализации cpu/mem — в metrics_linux.go / metrics_windows.go / metrics_other.go,
// диск — в disk_linux.go / disk_windows.go / disk_other.go,
// аптайм — в uptime_*.go.
package agent

import (
	"github.com/dreamcatchered/swagcore/internal/model"
)

// lastCPU хранит суммарное/занятое время CPU между замерами (Linux).
var lastCPU [2]uint64

// HostMetrics собирает текущие метрики хоста.
func HostMetrics(containers int) model.Metrics {
	cpuPct := cpuPercent()
	memUsed, memTotal := memInfo()
	diskUsed, diskTotal := diskUsage("/")
	return model.Metrics{
		CPUPct:      cpuPct,
		MemUsedMB:   memUsed / 1024 / 1024,
		MemTotalMB:  memTotal / 1024 / 1024,
		DiskUsedGB:  round1(diskUsed),
		DiskTotalGB: round1(diskTotal),
		Containers:  containers,
		UptimeSec:   uptimeSec(),
	}
}

func round1(f float64) float64 {
	return float64(int(f*10)) / 10
}
