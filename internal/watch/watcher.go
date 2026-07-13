// Package watch 监听各 agent 的会话存储路径，文件一有变化就（节流后）触发回调。
// 回调是"信号"语义：不携带变更内容，由消费方自行重扫/重拉。
package watch

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher 用 fsnotify 递归监听目录（或单个文件），并以节流方式触发 onChange。
// 节流语义：一段事件风暴内，从第一个事件起若干时间后触发一次——持续写入
// 不会饿死回调（区别于"尾随防抖"）。
//
// 双档窗口：追加写等常规事件走 slow 窗口（活跃会话的持续写入不至于高频
// 触发全量重索引）；递归根下的 Create（新会话文件、codex 日期目录等）走
// fast 窗口，且允许把已排队的慢触发拉前，保住"新会话秒级出现"。
type Watcher struct {
	fsw      *fsnotify.Watcher
	fast     time.Duration
	slow     time.Duration
	onChange func()

	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	// recursiveDirs 记录以递归语义监听的目录：其下新建的子目录要动态补挂监听
	// （codex 按日期建目录、claude 按项目建目录，漏挂会导致新会话不触发）。
	recursiveDirs map[string]bool
	// filePrefixes：非递归监听的父目录 -> 文件路径前缀白名单。
	// 用于 opencode 这类单 SQLite 文件源：只关心 opencode.db / -wal / -shm，
	// 父目录里其他文件的事件一律忽略。
	filePrefixes map[string][]string
}

func New(fast, slow time.Duration, onChange func()) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		fsw:           fsw,
		fast:          fast,
		slow:          slow,
		onChange:      onChange,
		recursiveDirs: make(map[string]bool),
		filePrefixes:  make(map[string][]string),
	}, nil
}

// Add 挂载一个监听根：目录按递归语义监听，文件则非递归监听其父目录并
// 只放行该文件（及其派生文件，如 SQLite 的 -wal/-shm）的事件。
// 单个根挂载失败只记日志不返回错误——一个 agent 目录不可读不应拖垮其他 agent 的监听。
func (w *Watcher) Add(root string) {
	info, err := os.Stat(root)
	if err != nil {
		log.Printf("[watch] skip %s: %v", root, err)
		return
	}
	if !info.IsDir() {
		dir := filepath.Dir(root)
		if err := w.fsw.Add(dir); err != nil {
			log.Printf("[watch] add %s: %v", dir, err)
			return
		}
		w.mu.Lock()
		w.filePrefixes[dir] = append(w.filePrefixes[dir], root)
		w.mu.Unlock()
		return
	}
	w.addDirRecursive(root)
}

func (w *Watcher) addDirRecursive(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // 不可读的子目录跳过即可
		}
		if err := w.fsw.Add(path); err != nil {
			log.Printf("[watch] add %s: %v", path, err)
			return nil
		}
		w.mu.Lock()
		w.recursiveDirs[path] = true
		w.mu.Unlock()
		return nil
	})
}

// Run 阻塞消费 fsnotify 事件直到 Close。应在独立 goroutine 中调用。
func (w *Watcher) Run() {
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("[watch] error: %v", err)
		}
	}
}

func (w *Watcher) Close() error { return w.fsw.Close() }

func (w *Watcher) handleEvent(ev fsnotify.Event) {
	// Chmod 噪音大且不代表内容变化
	if ev.Op == fsnotify.Chmod {
		return
	}

	dir := filepath.Dir(ev.Name)

	w.mu.Lock()
	prefixes, restricted := w.filePrefixes[dir]
	parentRecursive := w.recursiveDirs[dir]
	w.mu.Unlock()

	// 非递归根（单文件源）：只放行白名单前缀
	if restricted && !parentRecursive {
		matched := false
		for _, p := range prefixes {
			if strings.HasPrefix(ev.Name, p) {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
	}

	// 递归根下新建目录 → 补挂监听（先挂再触发，避免漏掉紧随的文件事件）
	if ev.Op.Has(fsnotify.Create) && parentRecursive {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			w.addDirRecursive(ev.Name)
		}
	}

	// 递归根下的 Create 是"新会话出现"信号，走快窗口；其余（追加写、
	// 单文件源的 -wal 等）走慢窗口。非递归根的 Create 不豁免——opencode
	// 的 -wal/-shm 在 checkpoint 时反复创建，属于写入噪音。
	if ev.Op.Has(fsnotify.Create) && parentRecursive {
		w.trigger(w.fast)
	} else {
		w.trigger(w.slow)
	}
}

// trigger 节流触发：首个事件起 d 后回调一次，风暴期间最多每 d 一次。
// 已有待触发回调时只允许把它拉前（fast 路径），不允许推后——保证持续
// 写入不会饿死回调。
func (w *Watcher) trigger(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()

	target := time.Now().Add(d)
	if w.timer != nil {
		// Reset 失败说明回调正在触发中，新事件很快就会被那次回调覆盖到
		if target.Before(w.deadline) && w.timer.Reset(d) {
			w.deadline = target
		}
		return
	}
	w.deadline = target
	w.timer = time.AfterFunc(d, func() {
		w.mu.Lock()
		w.timer = nil
		w.mu.Unlock()
		w.onChange()
	})
}
