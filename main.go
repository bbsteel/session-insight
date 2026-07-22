package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/indexer"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/server"
	"github.com/bbsteel/session-insight/internal/watch"
)

//go:embed frontend/dist
var frontend embed.FS

// 构建期通过 -ldflags "-X main.version=… -X main.commit=…" 注入：
// release 只注入 tag 版本号；本地 run.sh 构建同时注入 commit 等开发信息。
var (
	version = "dev"
	commit  = ""
)

// indexStatusAdapter maps indexer.Progress to the server HTTP DTO without
// coupling server ↔ indexer packages beyond this thin main-process wire-up.
type indexStatusAdapter struct{ ix *indexer.Indexer }

func (a indexStatusAdapter) SnapshotProgress() server.IndexProgress {
	p := a.ix.SnapshotProgress()
	return server.IndexProgress{
		State:   p.State,
		Done:    p.Done,
		Total:   p.Total,
		Percent: p.Percent,
		Message: p.Message,
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	frontendFS, err := fs.Sub(frontend, "frontend/dist")
	if err != nil {
		log.Fatalf("failed to get frontend sub-fs: %v", err)
	}

	dataDir := os.Getenv("SI_DATA_DIR")
	if dataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("failed to get home dir: %v", err)
		}
		dataDir = filepath.Join(homeDir, ".session-insight")
	}
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
	srv.Version = version
	srv.Commit = commit
	srv.SetIndexStatus(indexStatusAdapter{idx})

	// 索引轮产生实际变更后才通知：SSE 发出时数据已落库，侧栏重拉读到的
	// 就是新数据（/api/sessions 直接从 SQLite 出），也不会跟索引轮抢 CPU。
	idx.OnChanged = srv.NotifySessionsChanged

	// 不阻塞 HTTP 启动：后台一个劲索引；搜索栏通过 /api/index/status 显示进度。
	// Kick 立即跑首轮（schema 升级清空 watermark 后的全量重扫也走这里）。
	go idx.RunBackground(context.Background())
	idx.Kick()

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
	//
	// Try the requested port first. If it's already in use, fall back to an
	// OS-assigned port so the binary always starts regardless of what else is
	// running — important both for the dev workflow (run.sh) and for the
	// packaged release binary.
	listener, err := listenWithFallback("127.0.0.1", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	url := "http://" + listener.Addr().String() + "/"
	log.Printf("SessionInsight listening on %s", url)
	// Open the real bound URL (may differ from PORT when fallback kicks in).
	// Start is fire-and-forget so a slow browser never delays Serve.
	openBrowser(url)
	log.Fatal(http.Serve(listener, srv.Mux))
}

// listenWithFallback attempts to listen on host:port. If the port is already in
// use, it falls back to an OS-assigned port on the same host and logs a
// warning so the user knows the original port was unavailable.
func listenWithFallback(host, port string) (net.Listener, error) {
	return listenWithFallbackFn(host, port, net.Listen)
}

// listenWithFallbackFn is the injectable form used by tests.
func listenWithFallbackFn(host, port string, doListen func(network, addr string) (net.Listener, error)) (net.Listener, error) {
	addr := host + ":" + port
	listener, err := doListen("tcp", addr)
	if err == nil {
		return listener, nil
	}
	if !isAddrInUse(err) {
		return nil, err
	}
	log.Printf("port %s is in use, falling back to an OS-assigned port", port)
	listener, err = doListen("tcp", host+":0")
	if err != nil {
		return nil, fmt.Errorf("fallback listen failed: %w", err)
	}
	return listener, nil
}

// isAddrInUse reports whether err is an EADDRINUSE from a failed listen.
// errors.Is unwraps through *net.OpError → *os.SyscallError → syscall.Errno.
func isAddrInUse(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "listen" && errors.Is(err, syscall.EADDRINUSE)
}

// browserOpenCmd returns the platform command that opens url in the default
// browser. goos is injected so tests can cover Windows/macOS/Linux without
// depending on the host OS. The empty title arg on Windows `start` prevents
// start from treating a quoted URL as the window title.
func browserOpenCmd(goos, url string) (name string, args []string) {
	switch goos {
	case "windows":
		return "cmd", []string{"/c", "start", "", url}
	case "darwin":
		return "open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

// startBrowserCommand launches the OS browser helper. Swapped by tests.
var startBrowserCommand = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	// Detach from our stdio so a chatty helper cannot block Serve shutdown.
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Do not Wait: the helper often stays alive as the browser process tree.
	// Release reaps the process entry on Unix; on Windows it's a no-op-ish
	// cleanup of the handle. Either way we are fire-and-forget.
	_ = cmd.Process.Release()
	return nil
}

// openBrowser opens url in the default browser. Failures are logged only —
// the server still starts so the user can open the URL manually from the log.
func openBrowser(url string) {
	name, args := browserOpenCmd(runtime.GOOS, url)
	if err := startBrowserCommand(name, args...); err != nil {
		log.Printf("failed to open browser: %v (open %s manually)", err, url)
	}
}
