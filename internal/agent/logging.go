package agent

// Логирование агента в файл.
//
// Зачем: когда агент стоит как служба Windows, его stdout уходит в никуда.
// При сбое старта (например, SCM не дождался рукопожатия — Event ID 7009) в
// Event Log попадает только бесполезная строка, а разбираться приходится
// вслепую. С ноды удалённо работали по SSH, и диагностировать падение было
// нечем.
//
// Теперь агент пишет живой лог в <data-dir>/agent.log с ротацией по размеру
// и сохраняет предыдущий файл как agent.log.1.

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dreamcatchered/swagcore/internal/model"
)

const (
	agentLogMaxBytes = 2 << 20 // 2 MiB
	agentLogKeep     = 3       // сколько ротаций хранить
)

var logOnce sync.Once

// SetupFileLogging подключает лог к файлу в dataDir. Идемпотентна.
func SetupFileLogging(dataDir string) {
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	logOnce.Do(func() {
		path := filepath.Join(dataDir, "agent.log")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return
		}
		rotateAgentLog(path)

		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		// часы + дата + уровень/файл:строка — сразу читаемо при разборе
		log.SetFlags(log.Ldate | log.Ltime)

		// ПОРЯДОК ВАЖЕН: файл ПЕРВЫМ, и stderr обёрнут в bestEffort.
		//
		// У процесса, запущенного SCM, нет консоли: хендл 2 (stderr)
		// невалиден, и запись в него падает с «The handle is invalid».
		// io.MultiWriter возвращается на ПЕРВОЙ ошибке и до следующих
		// писателей не доходит. При MultiWriter(os.Stderr, file) файл
		// поэтому не получал НИЧЕГО, и у агента под службой не было
		// ни одного лога — именно это делало падение SCM (Event 7009)
		// невозможно диагностировать удалённо.
		log.SetOutput(io.MultiWriter(f, bestEffort{w: os.Stderr}))

		log.Printf("[agent] file logging enabled: %s", path)
		log.Printf("[agent] build=%s pid=%d data=%s", model.VersionTag(), os.Getpid(), dataDir)
	})
}

// bestEffort — писатель, который не возвращает ошибку записи.
//
// Нужен для stderr под службой Windows: хендл может быть невалиден, и мы не
// хотим этим ни ронять логирование, ни терять запись в файл.
type bestEffort struct{ w io.Writer }

func (b bestEffort) Write(p []byte) (int, error) {
	n, _ := b.w.Write(p)
	return n, nil
}

// rotateAgentLog сдвигает agent.log -> agent.log.1 -> ... и освобождает место.
func rotateAgentLog(path string) {
	if fi, err := os.Stat(path); err != nil || fi.Size() < agentLogMaxBytes {
		return
	}
	// сначала удаляем самый старый
	_ = os.Remove(fmt.Sprintf("%s.%d", path, agentLogKeep-1))
	for i := agentLogKeep - 1; i > 0; i-- {
		from := fmt.Sprintf("%s.%d", path, i-1)
		to := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, to)
		}
	}
	_ = os.Rename(path, path+".1")
}

// DataDirFromArgs вытаскивает --data из аргументов агента (нужно ДО разбора
// флагов, чтобы лог открылся до первого log.Printf).
func DataDirFromArgs(argv []string) string {
	def := defaultDataDir()
	for i, a := range argv {
		if a == "--data" && i+1 < len(argv) {
			return argv[i+1]
		}
		if strings.HasPrefix(a, "--data=") {
			return strings.TrimPrefix(a, "--data=")
		}
	}
	return def
}
