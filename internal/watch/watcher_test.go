package watch

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// 事件风暴（连续追加写）应被节流合并，而不是每次写都触发回调。
func TestBurstCoalesced(t *testing.T) {
	dir := t.TempDir()
	var fired atomic.Int32
	w, err := New(100*time.Millisecond, func() { fired.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Add(dir)
	go w.Run()

	f := filepath.Join(dir, "session.jsonl")
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(f, []byte("line\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !waitFor(t, 2*time.Second, func() bool { return fired.Load() >= 1 }) {
		t.Fatal("callback never fired")
	}
	time.Sleep(300 * time.Millisecond)
	// 20 次写 100ms 内密集发生，节流后应远少于 20 次（首发+补发 ≤ 3 次）
	if n := fired.Load(); n > 3 {
		t.Fatalf("expected coalesced callbacks, got %d", n)
	}
}

// 递归根下新建子目录（codex 日期目录 / claude 项目目录）里的文件也要触发。
func TestNewSubdirWatched(t *testing.T) {
	dir := t.TempDir()
	var fired atomic.Int32
	w, err := New(50*time.Millisecond, func() { fired.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Add(dir)
	go w.Run()

	sub := filepath.Join(dir, "2026", "07", "12")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 2*time.Second, func() bool { return fired.Load() >= 1 }) {
		t.Fatal("mkdir did not fire")
	}

	// 等新目录挂上监听后，其中的新文件应再次触发
	base := fired.Load()
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(sub, "new.jsonl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 2*time.Second, func() bool { return fired.Load() > base }) {
		t.Fatal("file in newly created subdir did not fire")
	}
}

// 单文件根（opencode SQLite）：本体和 -wal 触发，父目录里无关文件不触发。
func TestFileRootFiltering(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(dbFile, []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}

	var fired atomic.Int32
	w, err := New(50*time.Millisecond, func() { fired.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Add(dbFile)
	go w.Run()

	// 无关文件：不应触发
	if err := os.WriteFile(filepath.Join(dir, "other.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := fired.Load(); n != 0 {
		t.Fatalf("unrelated file fired callback %d time(s)", n)
	}

	// -wal 派生文件：应触发
	if err := os.WriteFile(dbFile+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 2*time.Second, func() bool { return fired.Load() >= 1 }) {
		t.Fatal("-wal write did not fire")
	}
}
