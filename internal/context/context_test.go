package context

import (
	"strings"
	"testing"

	"pagent/internal/model"
)

// ═══════════════ L1 目录组装 ═══════════════

func TestBuildSystemMessage_Should_IncludeDataBoundaryDeclaration(t *testing.T) {
	// 4.6 数据与指令分离：系统消息必须声明 tool_result 不可信
	got := BuildSystemMessage()
	if !strings.Contains(got, "tool_result") {
		t.Error("system message missing tool_result trust declaration")
	}
	if !strings.Contains(got, "不可信") && !strings.Contains(got, "数据") {
		t.Error("system message should declare tool output is data, not instruction")
	}
}

func TestBuildL1Catalog_Should_ListBackgroundAndChains(t *testing.T) {
	bg := []BackgroundEntry{
		{ID: "bg-1", Title: "项目约定", Description: "使用 TDD", When: "写代码时", Key: true},
		{ID: "bg-2", Title: "术语表", Description: "领域术语", When: "遇到术语时", Key: false},
	}
	chains := []ChainEntry{
		{ID: "ch-1", Name: "工作链", Description: "实现功能", ImportantNodes: []string{"N1", "N2"}},
	}

	got := BuildL1Catalog(bg, chains)

	// 关键背景有标记
	if !strings.Contains(got, "[关键]") {
		t.Error("key background should have [关键] marker")
	}
	// 背景包含名称、作用、何时使用
	if !strings.Contains(got, "项目约定") || !strings.Contains(got, "TDD") || !strings.Contains(got, "写代码时") {
		t.Errorf("background entry incomplete: %s", got)
	}
	// 其他链：名称、起始描述、重要节点名称列表
	if !strings.Contains(got, "工作链") || !strings.Contains(got, "实现功能") || !strings.Contains(got, "N1") {
		t.Errorf("chain entry incomplete: %s", got)
	}
}

// ═══════════════ 历史消息组装 ═══════════════

func TestAssemble_Should_InjectSystemThenHistoryThenUser(t *testing.T) {
	hist := []model.NodePart{
		{Role: model.NodePartRoleUser, Content: "用户1"},
		{Role: model.NodePartRoleAssistant, Content: "助手1"},
	}
	inj := []string{"[系统] 文件 foo.go 已变更"}
	bg := []BackgroundEntry{{ID: "bg-1", Title: "项目约定", Description: "TDD", When: "写代码时", Key: true}}
	chains := []ChainEntry{{ID: "ch-1", Name: "工作链", Description: "实现", ImportantNodes: []string{"N1"}}}

	msgs, err := Assemble(AssembleInput{
		History:       hist,
		Injections:    inj,
		Backgrounds:   bg,
		VisibleChains: chains,
		UserInput:     "继续",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 第一条是 system
	if msgs[0].Role != "system" {
		t.Errorf("first message role = %s, want system", msgs[0].Role)
	}
	// 注入消息紧跟 system 之后
	if msgs[1].Role != "system" {
		t.Errorf("injection role = %s, want system", msgs[1].Role)
	}
	// 历史
	foundUser1 := false
	for _, m := range msgs {
		if m.Role == "user" && m.Content == "用户1" {
			foundUser1 = true
		}
	}
	if !foundUser1 {
		t.Error("history user message missing")
	}
	// 用户输入在最后
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "继续" {
		t.Errorf("last message = %+v, want user/继续", last)
	}
}

func TestAssemble_Should_SkipEmptyInjectionsAndBackgrounds(t *testing.T) {
	msgs, err := Assemble(AssembleInput{
		History:   []model.NodePart{{Role: model.NodePartRoleUser, Content: "hi"}},
		UserInput: "hi2",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 只有 system + user(hi) + user(hi2)
	if len(msgs) != 3 {
		t.Errorf("msgs len = %d, want 3: %v", len(msgs), msgs)
	}
}

// ═══════════════ 背景注入 ═══════════════

func TestAssemble_Should_InlineKeyBackgroundsIntoSystem(t *testing.T) {
	bg := []BackgroundEntry{
		{ID: "bg-1", Title: "关键规则", Description: "必须遵守", When: "任何时候", Key: true},
	}
	msgs, err := Assemble(AssembleInput{
		Backgrounds: bg,
		UserInput:   "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 关键背景内容应出现在 system 消息中
	sys := msgs[0].Content
	if !strings.Contains(sys, "关键规则") || !strings.Contains(sys, "必须遵守") {
		t.Errorf("key background not inlined into system: %s", sys)
	}
}
