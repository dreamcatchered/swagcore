// swagcore-server — центральный сервер платформы (control-plane).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dreamcatchered/swagcore/internal/ai"
	"github.com/dreamcatchered/swagcore/internal/api"
	"github.com/dreamcatchered/swagcore/internal/hub"
	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/reconciler"
	"github.com/dreamcatchered/swagcore/internal/store"
)

func main() {
	var (
		listen = flag.String("listen", envOr("LISTEN", "127.0.0.1:8181"), "listen address (default binds localhost only)")
		data   = flag.String("data", envOr("DATA_DIR", "./data"), "data directory (sqlite)")
		public = flag.String("public-url", envOr("PUBLIC_URL", ""), "public base URL of the panel (for install commands)")
	)
	flag.Parse()

	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		log.Fatal("ADMIN_TOKEN env var is required (random string)")
	}

	st, err := store.Open(*data)
	defer func() { _ = st.Close() }()
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	// первый запуск: сидируем дефолтный токен ноды. Для совместимости с v0.1/v0.2
	// используем NODE_TOKEN из env (если задан), иначе генерируем случайный.
	if cnt, err := st.CountTokens(); err == nil && cnt == 0 {
		tok := os.Getenv("NODE_TOKEN")
		if tok == "" {
			tok = genToken()
			log.Printf("created default node token: %s", tok)
		} else {
			log.Printf("seeded default node token from NODE_TOKEN env")
		}
		if err := st.CreateToken("default", tok, hub.HashToken(tok), 0, 0, 0); err != nil {
			log.Fatalf("create default node token: %v", err)
		}
	}

	h := hub.New(st)
	h.SetLogsCallback(func(projectID int64, data string) {
		st.AppendLog(projectID, data)
	})

	rec := reconciler.New(st, h)
	go rec.Run()

	apiSrv := api.New(st, h, adminToken, *public)
	// ИИ-агент (Groq): включается при наличии GROQ_API_KEY в env
	if groq := os.Getenv("GROQ_API_KEY"); groq != "" {
		wsRoot := envOr("AI_WORKSPACE", "/workspace")
		dbDir := filepath.Join(*data, "ai")
		_ = os.MkdirAll(dbDir, 0o755)
		agent := ai.New(st, h, apiSrv, groq, wsRoot, dbDir)
		apiSrv.AI = agent
		log.Printf("ai agent enabled (groq), workspace=%s", wsRoot)
	} else {
		log.Printf("ai agent disabled (GROQ_API_KEY not set)")
	}
	mux := http.NewServeMux()
	apiSrv.RegisterRoutes(mux)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Sweep(ctx)

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// Ограничения на тело запроса: в v0.5.x их не было вовсе, из-за чего
		// один запрос мог съесть память сервера.
		ReadTimeout:       10 * time.Minute, // большие загрузки артефактов
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Printf("swagcore-server %s listening on %s (public: %s)", model.VersionTag(), *listen, apiSrv.PublicBase)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")
	cancel()
	// финальный чекпоинт WAL, чтобы БД не осталась с хвостом WAL-файла
	_ = st.Checkpoint()
	shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shCancel()
	_ = srv.Shutdown(shCtx)
}

func genToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
