package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/indexer"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/server"
	"github.com/bbsteel/session-insight/internal/watch"
)

//go:embed frontend/dist
var frontend embed.FS

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	frontendFS, err := fs.Sub(frontend, "frontend/dist")
	if err != nil {
		log.Fatalf("failed to get frontend sub-fs: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("failed to get home dir: %v", err)
	}
	dataDir := filepath.Join(homeDir, ".session-insight")
	database, err := db.Open(dataDir)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	readers := reader.Discover()
	log.Printf("Discovered %d agent reader(s)", len(readers))
	for _, r := range readers {
		log.Printf("  - %s", r.AgentType())
	}

	idx := indexer.New(database, readers)
	srv := server.New(database, readers)

	// 索引轮产生实际变更后才通知：SSE 发出时数据已落库，侧栏重拉读到的
	// 就是新数据（/api/sessions 直接从 SQLite 出），也不会跟索引轮抢 CPU。
	idx.OnChanged = srv.NotifySessionsChanged

	// 首次索引同步完成（10s 超时），保证服务启动时已有基础索引
	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := idx.RunOnce(initCtx); err != nil {
		log.Printf("initial indexing incomplete: %v", err)
	}
	cancel()

	// 后台增量更新
	go idx.RunBackground(context.Background())

	// 文件监听：会话文件一变 → 踢一轮增量索引（落库后由 OnChanged 通知侧栏）。
	// 追加写走 5s 慢窗口——活跃会话的持续写入不再每 500ms 全量重索引，
	// 代价只是侧栏计数/搜索晚几秒；新会话 Create 走 500ms 快窗口，秒级出现。
	// 打开中的会话走 revision 轮询直读文件，不经过这条索引管道，不受影响。
	// 监听器起不来只降级为"手动刷新页面"，不影响其他功能。
	watcher, err := watch.New(500*time.Millisecond, 5*time.Second, func() {
		idx.Kick()
	})
	if err != nil {
		log.Printf("file watcher unavailable, sidebar live refresh disabled: %v", err)
	} else {
		roots := 0
		for _, r := range readers {
			if p, ok := r.(reader.WatchRootProvider); ok {
				for _, root := range p.WatchRoots() {
					watcher.Add(root)
					roots++
				}
			}
		}
		go watcher.Run()
		log.Printf("Watching %d session root(s) for live sidebar refresh", roots)
	}

	fileServer := http.FileServer(http.FS(frontendFS))
	srv.Mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" || p == "/index.html" {
			// index.html must revalidate so the browser picks up new asset hashes after a build.
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			// Vite content-hashes all JS/CSS/font filenames; safe to cache indefinitely.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	}))

	// Loopback only: the API exposes session contents and (via the editor
	// command setting + open-file) command execution, so it must never be
	// reachable from the network.
	log.Printf("SessionInsight listening on http://127.0.0.1:%s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, srv.Mux))
}
