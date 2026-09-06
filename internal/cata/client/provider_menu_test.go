package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cata/internal/cata/config"
	"cata/internal/llm"
)

// writeTestConfig 在临时 CATA_HOME 写入 config.json。
func writeTestConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.EnvCataHome, dir)
	t.Setenv(config.EnvConfigFile, "")
	path := filepath.Join(dir, config.DefaultAppConfigName)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestOpenProviderPickerIsMenu /provider 打开选项式 provider 列表菜单（含激活标记）。
func TestOpenProviderPickerIsMenu(t *testing.T) {
	writeTestConfig(t, `{
  "llm": {
    "provider": "mock",
    "api_url": "http://127.0.0.1:1/v1/chat/completions",
    "model": "m1",
    "enabled": true
  },
  "llm_alt": {"provider": "alt", "api_url": "http://127.0.0.1:2/v1/chat/completions"}
}
`)
	m := newTestModel()
	nm, cmd := m.handleProviderCmd("")
	if cmd != nil {
		t.Fatalf("打开菜单不应返回 cmd：%v", cmd)
	}
	mm, ok := nm.(*model)
	if !ok || mm.overlay == nil || mm.overlay.mode != overlayProvider {
		t.Fatalf("overlay 未打开为 provider 菜单")
	}
	items := mm.overlay.list.Items()
	if len(items) != 2 { // default + alt（迁移自 llm_alt）
		t.Fatalf("菜单项=%d 期望 2", len(items))
	}
	first := items[0].(pickItem)
	if first.id != "alt" {
		t.Fatalf("第一项应为 alt（按名排序），实际 %q", first.id)
	}
	// default（active）项带 ● 激活标记。
	foundActive := false
	for _, it := range items {
		if strings.Contains(it.(pickItem).title, "●") {
			foundActive = true
		}
	}
	if !foundActive {
		t.Fatalf("菜单中应存在激活标记 ●")
	}
}

// TestEnterProviderPickStartsBackgroundProbe 选中未探测 provider → 触发后台探测 cmd（不阻塞 UI）。
func TestEnterProviderPickStartsBackgroundProbe(t *testing.T) {
	writeTestConfig(t, `{
  "llm": {
    "provider": "mock",
    "api_url": "http://127.0.0.1:1/v1/chat/completions",
    "model": "m1",
    "enabled": true
  },
  "llm_px": {"provider": "px", "api_url": "http://127.0.0.1:2/v1/chat/completions"}
}
`)
	m := newTestModel()
	nm, _ := m.handleProviderCmd("")
	mm, _ := nm.(*model)
	// names 排序：[default, px] → px 在下标 1。
	mm.overlay.list.Select(1)
	nm2, cmd := mm.enterProviderPick()
	mm2, ok := nm2.(*model)
	if !ok {
		t.Fatalf("enterProviderPick 返回非 model")
	}
	if cmd == nil {
		t.Fatalf("未探测 provider 应触发后台探测 cmd")
	}
	if !mm2.overlay.probing || mm2.overlay.providerName != "px" {
		t.Fatalf("overlay 应标记 probing provider=%q", mm2.overlay.providerName)
	}
}

// TestEnterProviderPickProbedOpensModelMenu 已探测 provider → 直接出模型选择菜单（选项式）。
func TestEnterProviderPickProbedOpensModelMenu(t *testing.T) {
	now := time.Now().Add(-time.Minute).Format(time.RFC3339)
	writeTestConfig(t, fmt.Sprintf(`{
  "llm": {
    "provider": "mock",
    "api_url": "http://127.0.0.1:1/v1/chat/completions",
    "model": "vision-model",
    "enabled": true
  },
  "llm_providers": {
    "active": "default",
    "providers": {
      "default": {
        "name": "default",
        "api_url": "http://127.0.0.1:1/v1/chat/completions",
        "model": "vision-model",
        "enabled": true,
        "probe": {
          "models": ["text-model", "vision-model"],
          "capabilities": {
            "text-model": {"modalities": ["text"]},
            "vision-model": {"modalities": ["text", "image"]}
          },
          "probed_at": "%s"
        }
      }
    }
  }
}
`, now))
	m := newTestModel()
	nm, _ := m.handleProviderCmd("")
	mm, _ := nm.(*model)
	nm2, cmd := mm.enterProviderPick()
	if cmd != nil {
		t.Fatalf("已探测不应触发探测 cmd")
	}
	mm2, ok := nm2.(*model)
	if !ok || mm2.overlay == nil || mm2.overlay.mode != overlayProviderModel {
		t.Fatalf("应直接进入模型菜单；overlay=%v", mm2.overlay)
	}
	if len(mm2.overlay.list.Items()) != 2 {
		t.Fatalf("模型菜单项=%d 期望 2", len(mm2.overlay.list.Items()))
	}
	// 能力标注：vision-model 项 desc 应含 image。
	foundVision := false
	for _, it := range mm2.overlay.list.Items() {
		pi := it.(pickItem)
		if pi.id == "vision-model" {
			foundVision = true
			if !strings.Contains(pi.desc, "image") {
				t.Fatalf("vision-model 能力标注缺失：%q", pi.desc)
			}
		}
	}
	if !foundVision {
		t.Fatalf("模型菜单缺少 vision-model 项")
	}
}

// TestProviderProbeDoneOpensModelMenu 后台探测成功 → 自动进入模型菜单（无需再手输）。
func TestProviderProbeDoneOpensModelMenu(t *testing.T) {
	writeTestConfig(t, `{
  "llm": {
    "provider": "mock",
    "api_url": "http://127.0.0.1:1/v1/chat/completions",
    "model": "m1",
    "enabled": true
  },
  "llm_px": {"provider": "px", "api_url": "http://127.0.0.1:2/v1/chat/completions"}
}
`)
	m := newTestModel()
	nm, _ := m.handleProviderCmd("")
	mm, _ := nm.(*model)
	mm.overlay.list.Select(1)
	nm2, cmd := mm.enterProviderPick()
	mm2, _ := nm2.(*model)
	if cmd == nil {
		t.Fatal("期望探测 cmd")
	}
	msg := providerProbeDoneMsg{
		name: "px",
		ok:   true,
		rep: llm.ProbeReport{
			Models: []string{"px-m1", "px-vision"},
			Capabilities: map[string]config.ModelCapCfg{
				"px-m1":     {Modalities: []string{"text"}},
				"px-vision": {Modalities: []string{"text", "image"}},
			},
		},
	}
	nm3, cmd2 := mm2.handleProviderProbeDone(msg)
	if cmd2 != nil {
		t.Fatalf("探测完成不应返回 cmd")
	}
	mm3, ok := nm3.(*model)
	if !ok || mm3.overlay == nil || mm3.overlay.mode != overlayProviderModel {
		t.Fatalf("探测完成后应自动进入模型菜单")
	}
	if len(mm3.overlay.list.Items()) != 2 {
		t.Fatalf("模型菜单项=%d 期望 2", len(mm3.overlay.list.Items()))
	}
}

// TestProviderProbeDoneFailureKeepsMenu 探测失败（后台完成）→ 关闭菜单、报错，不覆盖配置。
func TestProviderProbeDoneFailureKeepsMenu(t *testing.T) {
	writeTestConfig(t, `{
  "llm": {
    "provider": "mock",
    "api_url": "http://127.0.0.1:1/v1/chat/completions",
    "model": "m1",
    "enabled": true
  },
  "llm_px": {"provider": "px", "api_url": "http://127.0.0.1:2/v1/chat/completions"}
}
`)
	m := newTestModel()
	nm, _ := m.handleProviderCmd("")
	mm, _ := nm.(*model)
	mm.overlay.list.Select(1)
	nm2, _ := mm.enterProviderPick()
	mm2, _ := nm2.(*model)
	nm3, _ := mm2.handleProviderProbeDone(providerProbeDoneMsg{name: "px", ok: false})
	mm3, ok := nm3.(*model)
	if !ok || mm3.overlay != nil {
		t.Fatalf("探测失败应关闭菜单（不进入模型菜单）")
	}
	if !strings.Contains(mm3.log, "failed") {
		t.Fatalf("应提示探测失败：\n%s", mm3.log)
	}
}

// TestModelCmdDirectMenu /model 直接在当前激活 provider 下出模型菜单（不经过供应商列表）。
func TestModelCmdDirectMenu(t *testing.T) {
	now := time.Now().Add(-time.Minute).Format(time.RFC3339)
	writeTestConfig(t, fmt.Sprintf(`{
  "llm": {
    "provider": "mock",
    "api_url": "http://127.0.0.1:1/v1/chat/completions",
    "model": "vision-model",
    "enabled": true
  },
  "llm_providers": {
    "active": "default",
    "providers": {
      "default": {
        "name": "default",
        "api_url": "http://127.0.0.1:1/v1/chat/completions",
        "model": "vision-model",
        "enabled": true,
        "probe": {
          "models": ["text-model", "vision-model"],
          "capabilities": {
            "text-model": {"modalities": ["text"]},
            "vision-model": {"modalities": ["text", "image"]}
          },
          "probed_at": "%s"
        }
      }
    }
  }
}
`, now))
	m := newTestModel()
	nm, cmd := m.handleModelCmd("")
	if cmd != nil {
		t.Fatalf("/model 已探测不应触发探测 cmd")
	}
	mm, ok := nm.(*model)
	if !ok || mm.overlay == nil || mm.overlay.mode != overlayModel {
		t.Fatalf("/model 应直接出 overlayModel 菜单，got overlay=%v", mm.overlay)
	}
	if len(mm.overlay.list.Items()) != 2 {
		t.Fatalf("模型菜单项=%d 期望 2", len(mm.overlay.list.Items()))
	}
	// Esc → 直接关闭（不回供应商列表）。
	nm2, _ := mm.updateOverlayKey(tea.KeyMsg{Type: tea.KeyEsc})
	mm2, _ := nm2.(*model)
	if mm2.overlay != nil {
		t.Fatalf("/model 菜单 Esc 应直接关闭")
	}
}

// TestModelCmdAutoProbesActive /model 时激活 provider 缺探测 → 后台自动补探（overlayModel + probing）。
func TestModelCmdAutoProbesActive(t *testing.T) {
	writeTestConfig(t, `{
  "llm": {
    "provider": "mock",
    "api_url": "http://127.0.0.1:1/v1/chat/completions",
    "model": "m1",
    "enabled": true
  },
  "llm_providers": {
    "active": "default",
    "providers": {
      "default": {
        "name": "default",
        "api_url": "http://127.0.0.1:1/v1/chat/completions",
        "model": "m1",
        "enabled": true
      }
    }
  }
}
`)
	m := newTestModel()
	nm, cmd := m.handleModelCmd("")
	if cmd == nil {
		t.Fatal("激活 provider 缺探测应触发后台探测 cmd")
	}
	mm, ok := nm.(*model)
	if !ok || mm.overlay == nil || mm.overlay.mode != overlayModel || !mm.overlay.probing {
		t.Fatalf("/model 缺探测应停留 overlayModel+probing")
	}
	// 探测成功 → 模型菜单（仍为 overlayModel，不回供应商列表）。
	nm2, _ := mm.handleProviderProbeDone(providerProbeDoneMsg{
		name: "default",
		ok:   true,
		rep: llm.ProbeReport{
			Models: []string{"m1", "m2"},
			Capabilities: map[string]config.ModelCapCfg{
				"m1": {Modalities: []string{"text"}},
				"m2": {Modalities: []string{"text", "image"}},
			},
		},
	})
	mm2, _ := nm2.(*model)
	if mm2.overlay == nil || mm2.overlay.mode != overlayModel {
		t.Fatalf("探测完成后应停留 /model 模型菜单（不回供应商列表）")
	}
}
