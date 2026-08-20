package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogPathsUnderServerDir 验证 cata-server.log 与 llm.log 统一落在 ~/.cata/logs/server/ 下，
// 方便集中排查；产出区拆分文件在其下的 llm/ 子目录。
func TestLogPathsUnderServerDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)

	if got := ServerLogPath(); got != filepath.Join(home, "logs", "server", "cata-server.log") {
		t.Fatalf("ServerLogPath = %q, want under logs/server", got)
	}

	// 有产出区 → logs/server/llm/<sanitized>.log
	if got := LLMLogPathFor("/a/b"); got != filepath.Join(home, "logs", "server", "llm", llmLogFileName("/a/b")) {
		t.Fatalf("LLMLogPathFor(/a/b) = %q, want under logs/server/llm", got)
	}

	// 无产出区 → logs/server/llm.log
	if got := LLMLogPathFor(""); got != filepath.Join(home, "logs", "server", "llm.log") {
		t.Fatalf("LLMLogPathFor(\"\") = %q, want logs/server/llm.log", got)
	}
}

func TestPruneArchivedLogs(t *testing.T) {
	dir := t.TempDir()
	// 12 个归档日志（文件名含 YYYYMMDD-HHMMSS-RRR）。
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("llm.202608%02d-000000-000.log", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// 当前活跃日志（非归档，不应被删）。
	if err := os.WriteFile(filepath.Join(dir, "llm.log"), []byte("active"), 0644); err != nil {
		t.Fatal(err)
	}

	pruneArchivedLogs(dir)

	entries, _ := os.ReadDir(dir)
	// 10 个归档 + 1 个活跃 = 11
	if len(entries) != maxArchivedLogs+1 {
		t.Fatalf("expected %d files, got %d", maxArchivedLogs+1, len(entries))
	}
	// 最旧的两个应被删，最新的保留。
	if _, err := os.Stat(filepath.Join(dir, "llm.20260801-000000-000.log")); !os.IsNotExist(err) {
		t.Fatal("oldest archived log should be pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "llm.20260812-000000-000.log")); err != nil {
		t.Fatal("newest archived log should remain")
	}
	if _, err := os.Stat(filepath.Join(dir, "llm.log")); err != nil {
		t.Fatal("active log should remain")
	}
}

// TestMigrateLegacyLogs 验证旧布局（CATA_HOME 根 cata-server.log* / llm.log*、~/.cata/llm/*）
// 会迁移到新统一目录 logs/server/ 下，且幂等（重复运行不覆盖新布局数据）。
func TestMigrateLegacyLogs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CATA_HOME", home)

	// 旧布局文件。
	legacyRoot := []string{FileServerLog, FileServerLog + ".20260101-000000-000.log", FileLLMLog}
	for _, n := range legacyRoot {
		if err := os.WriteFile(filepath.Join(home, n), []byte("root "+n), 0644); err != nil {
			t.Fatal(err)
		}
	}
	legacyLLMDir := filepath.Join(home, DirLLMLogs)
	_ = os.MkdirAll(legacyLLMDir, 0755)
	for _, n := range []string{"wsA.log", "wsA.20260101-000000-000.log"} {
		if err := os.WriteFile(filepath.Join(legacyLLMDir, n), []byte("llm "+n), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := migrateLegacyLogs(); err != nil {
		t.Fatalf("migrateLegacyLogs: %v", err)
	}

	expect := []string{
		filepath.Join(home, "logs", "server", FileServerLog),
		filepath.Join(home, "logs", "server", FileServerLog+".20260101-000000-000.log"),
		filepath.Join(home, "logs", "server", filepath.Base(FileLLMLog)),
		filepath.Join(home, "logs", "server", "llm", "wsA.log"),
		filepath.Join(home, "logs", "server", "llm", "wsA.20260101-000000-000.log"),
	}
	for _, p := range expect {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("migrated file missing: %s (%v)", p, err)
		}
	}
	// 旧位置应已清空。
	for _, p := range append(legacyRoot[:1], filepath.Join(legacyLLMDir, "wsA.log")) {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("legacy file should be moved away: %s", p)
		}
	}

	// 幂等：再跑一次不报错、不搬家新布局已有内容。
	if err := migrateLegacyLogs(); err != nil {
		t.Fatalf("second migrateLegacyLogs: %v", err)
	}
	if data, err := os.ReadFile(expect[0]); err != nil || !strings.Contains(string(data), "root "+FileServerLog) {
		t.Fatalf("migrated destination content clobbered on re-run: %q err=%v", string(data), err)
	}
}
