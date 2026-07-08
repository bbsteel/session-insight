//go:build sqlite_fts5

package db

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkSearch_ShortLIKE(b *testing.B) {
	db := benchDB(b, 700, 10)
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.SearchTurns("折叠", 30)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearch_LongFTS(b *testing.B) {
	db := benchDB(b, 700, 10)
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.SearchTurns("性能优化建议", 30)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearch_ShortASCII(b *testing.B) {
	db := benchDB(b, 700, 10)
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.SearchTurns("go", 30)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearch_LongASCII(b *testing.B) {
	db := benchDB(b, 700, 10)
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.SearchTurns("performance", 30)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchDB(tb testing.TB, nSessions, turnsPer int) *DB {
	tb.Helper()
	db, err := Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	for s := 0; s < nSessions; s++ {
		sid := fmt.Sprintf("sess-%05d", s)
		turns := make([]TurnText, 0, turnsPer+1)
		for t := 0; t < turnsPer; t++ {
			turns = append(turns, TurnText{
				TurnIndex: t,
				Role:      "user",
				Content:   benchContent(s, t),
			})
		}
		turns = append(turns, TurnText{
			TurnIndex: -1,
			Role:      "meta",
			Content:   fmt.Sprintf("Session-%05d %s %s %s", s, pickLabel(s, 0), pickLabel(s, 1), sid),
		})
		if err := db.UpsertTurns("bench", sid, turns, int64(1000+s)); err != nil {
			db.Close()
			tb.Fatalf("UpsertTurns %s: %v", sid, err)
		}
	}
	return db
}

var benchLabels = []string{
	"性能优化 内存泄漏排查",
	"折叠面板 交互设计",
	"配置文件热加载",
	"数据库迁移脚本",
	"API 鉴权中间件",
	"日志采集 pipeline",
	"docker compose 部署",
	"websocket 断线重连",
	"gRPC 流式传输",
	"单元测试覆盖率",
}

func pickLabel(s, t int) string {
	return benchLabels[(s+t)%len(benchLabels)]
}

func benchContent(s, t int) string {
	cjk := pickLabel(s, t)
	ascii := strings.Repeat("lorem ipsum dolor sit amet ", 3)
	return fmt.Sprintf("# Turn %d\n\n%s\n\n%s\n\nrevision: %d", t, cjk, ascii, s*100+t)
}
