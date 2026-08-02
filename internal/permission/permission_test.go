package permission

import "testing"

// ═══════════════ 风险分级 ═══════════════

func TestClassify_Should_AssignRiskTierByOperation(t *testing.T) {
	tests := []struct {
		name string
		op   Operation
		want Tier
	}{
		{"read project file", Operation{Tool: "read_file", Path: "/work/app/src/foo.go"}, Tier0},
		{"write project file", Operation{Tool: "write_file", Path: "/work/app/src/foo.go"}, Tier1},
		{"run command", Operation{Tool: "run_command", Path: "/work/app", Command: "make test"}, Tier2},
		{"write dotenv", Operation{Tool: "write_file", Path: "/work/app/.env"}, Tier3},
		{"write ssh config", Operation{Tool: "write_file", Path: "/home/u/.ssh/config"}, Tier3},
		{"delete recursive", Operation{Tool: "run_command", Path: "/work/app", Command: "rm -rf /"}, Tier3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.op)
			if got != tt.want {
				t.Errorf("classify(%+v) = %v, want %v", tt.op, got, tt.want)
			}
		})
	}
}

// ═══════════════ 作用域与路径定位 ═══════════════

func TestMatcher_Should_LocatePathInMounts(t *testing.T) {
	// 挂载目录：/work/app（项目 A）、/work/lib（项目 A）
	m := NewMatcher([]string{"/work/app", "/work/lib"})

	tests := []struct {
		path     string
		inMount  bool
		mountIdx int
	}{
		{"/work/app/src/foo.go", true, 0},
		{"/work/app", true, 0},
		{"/work/lib/x.go", true, 1},
		{"/tmp/outside", false, -1},
		{"/work/other", false, -1},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			idx, ok := m.Locate(tt.path)
			if ok != tt.inMount || idx != tt.mountIdx {
				t.Errorf("Locate(%s) = (%d,%v), want (%d,%v)", tt.path, idx, ok, tt.mountIdx, tt.inMount)
			}
		})
	}
}

func TestMatcher_Should_UseLongestPrefix_WhenNested(t *testing.T) {
	// 嵌套挂载：/work/app 和 /work/app/sub
	m := NewMatcher([]string{"/work/app", "/work/app/sub"})
	idx, ok := m.Locate("/work/app/sub/deep/file.go")
	if !ok {
		t.Fatal("should be in mount")
	}
	if idx != 1 {
		t.Errorf("locate = %d, want 1 (longest prefix)", idx)
	}
}

// ═══════════════ 规则链评估 ═══════════════

func TestEngine_Should_EvaluatePriorityChain(t *testing.T) {
	eng := NewEngine([]string{"/work/app"})

	// 全局：T1 允许
	eng.AddRule(Rule{Scope: ScopeGlobal, Action: ActionAllow, MaxTier: Tier1, Priority: 0})
	// 项目：T2 允许（更高优先级）
	eng.AddRule(Rule{Scope: ScopeProject, Action: ActionAllow, MaxTier: Tier2, Priority: 1})
	// 项目临时：T3 允许（最高）
	eng.AddRule(Rule{Scope: ScopeProjectTemp, Action: ActionAllow, MaxTier: Tier3, Priority: 2})

	// 项目内 T3 操作 → 临时规则生效，允许
	op := Operation{Tool: "run_command", Path: "/work/app", Command: "rm file"}
	d := eng.Decide(op)
	if d != DecisionAllow {
		t.Errorf("decide = %v, want allow (temp rule wins)", d)
	}
}

func TestEngine_Should_GlobalDeny_OverrideEverything(t *testing.T) {
	eng := NewEngine([]string{"/work/app"})
	eng.AddRule(Rule{Scope: ScopeGlobal, Action: ActionDeny, Pattern: `^rm -rf`, Priority: 0})
	// 项目临时规则 T3 全允许
	eng.AddRule(Rule{Scope: ScopeProjectTemp, Action: ActionAllow, MaxTier: Tier3, Priority: 2})

	// 全局 deny 不可被任何层覆盖
	op := Operation{Tool: "run_command", Path: "/work/app", Command: "rm -rf /"}
	d := eng.Decide(op)
	if d != DecisionDeny {
		t.Errorf("decide = %v, want deny (global deny is absolute)", d)
	}
}

func TestEngine_Should_AuthorizationNotEscapeMount(t *testing.T) {
	eng := NewEngine([]string{"/work/app"})
	// 项目临时 T3 全允许——但只能作用于项目内
	eng.AddRule(Rule{Scope: ScopeProjectTemp, Action: ActionAllow, MaxTier: Tier3, Priority: 2})

	// 项目外路径：项目规则无权发言 → 全局默认（ask）
	op := Operation{Tool: "write_file", Path: "/tmp/escape.txt"}
	d := eng.Decide(op)
	if d != DecisionAsk {
		t.Errorf("decide = %v, want ask (project rules cannot authorize outside mounts)", d)
	}
}

func TestEngine_Should_Ask_WhenNoProjectDirectory(t *testing.T) {
	eng := NewEngine(nil) // 无挂载目录
	op := Operation{Tool: "read_file", Path: "/work/app/src/foo.go"}
	d := eng.Decide(op)
	if d != DecisionAsk {
		t.Errorf("decide = %v, want ask (no mounts = everything asks)", d)
	}
}

// ═══════════════ 挂载目录规则收窄 ═══════════════

func TestEngine_Should_MountRule_NarrowProjectAllow(t *testing.T) {
	eng := NewEngine([]string{"/work/app", "/work/lib"})
	// 项目：T3 允许
	eng.AddRule(Rule{Scope: ScopeProject, Action: ActionAllow, MaxTier: Tier3, Priority: 1})
	// /work/lib 挂载目录：只读（T0 上限）
	eng.AddRule(Rule{Scope: ScopeMount, ScopeID: "/work/lib", Action: ActionAllow, MaxTier: Tier0, Priority: 0})

	// /work/app 下 T2 允许
	d := eng.Decide(Operation{Tool: "run_command", Path: "/work/app", Command: "make"})
	if d != DecisionAllow {
		t.Errorf("app decide = %v, want allow", d)
	}
	// /work/lib 下 T1 被收窄为 ask
	d = eng.Decide(Operation{Tool: "write_file", Path: "/work/lib/foo.go"})
	if d != DecisionAsk {
		t.Errorf("lib decide = %v, want ask (mount rule narrows)", d)
	}
}

// ═══════════════ 最高匹配规则 ═══════════════

func TestEngine_Should_FirstMatchingRuleWins(t *testing.T) {
	eng := NewEngine([]string{"/work/app"})
	// 两条同优先级规则：deny 先加
	eng.AddRule(Rule{Scope: ScopeGlobal, Action: ActionDeny, Pattern: `foo`, Priority: 0})
	eng.AddRule(Rule{Scope: ScopeGlobal, Action: ActionAllow, MaxTier: Tier2, Priority: 0})

	d := eng.Decide(Operation{Tool: "read_file", Path: "/work/app/foo.go"})
	if d != DecisionDeny {
		t.Errorf("decide = %v, want deny (first matching rule)", d)
	}
}

func TestEngine_Should_Ask_ForT0OutsideMount_WithoutRules(t *testing.T) {
	eng := NewEngine([]string{"/work/app"})
	// 项目外、无任何规则：T0 只读也不默认放行（越界 = 全局说了算）
	d := eng.Decide(Operation{Tool: "read_file", Path: "/tmp/outside.txt"})
	if d != DecisionAsk {
		t.Errorf("decide = %v, want ask (outside mount, no rules)", d)
	}
}
