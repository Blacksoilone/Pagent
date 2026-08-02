// Package permission 实现工具权限模型（3.2.1）。
//
// 设计依据：docs/agent-workspace-product-draft.md 3.2.1
// - 风险分级 T0-T3（等级由"工具+参数"决定，非工具名）
// - 优先级 × 作用域：授权不越界，禁止可越界
// - 多挂载目录定位（最长前缀优先）
// - 无项目目录 → 全部 ask
// - 全局 deny 是不可逾越的边界
package permission

import (
	"regexp"
	"strings"
)

// Tier 风险等级。
type Tier int

const (
	Tier0 Tier = iota // 只读：read_file/list_dir/grep/web_search → 永远自动允许
	Tier1             // 局部写：edit_file/write_file（项目内）
	Tier2             // 副作用：run_command/网络/项目外写入
	Tier3             // 危险：rm -rf/改 .env/写 ~/.ssh/提权
)

// Decision 权限决策结果。
type Decision int

const (
	DecisionAllow Decision = iota // 自动允许
	DecisionAsk                   // 询问用户
	DecisionDeny                  // 拒绝
)

// Operation 一次工具操作（评估对象）。
type Operation struct {
	Tool    string // 工具名
	Path    string // 目标路径（若适用）
	Command string // 命令内容（run_command）
}

// Action 规则动作。
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
)

// Scope 规则作用域。
type Scope string

const (
	ScopeGlobal      Scope = "global"       // 全局配置（硬边界）
	ScopeProject     Scope = "project"      // 项目永久规则（对所有挂载目录生效）
	ScopeProjectTemp Scope = "project_temp" // 项目临时配置（最高优先级）
	ScopeMount       Scope = "mount"        // 挂载目录独立规则
)

// Rule 权限规则。
// 优先级：ScopeProjectTemp > ScopeProject > ScopeMount > ScopeGlobal。
// 作用域：授权（allow）只作用于匹配的路径；deny 是硬边界不可被下层覆盖。
type Rule struct {
	Scope    Scope
	ScopeID  string // project_id / mount 路径
	Action   Action
	MaxTier  Tier   // allow 时允许的最大风险等级
	Pattern  string // 路径/命令正则（空 = 全部）
	Priority int    // 同作用域内排序
	re       *regexp.Regexp
}

// Matcher 路径定位器：判断路径归属哪个挂载目录（最长前缀优先）。
type Matcher struct {
	mounts []string
}

// NewMatcher 创建路径定位器。
func NewMatcher(mounts []string) *Matcher {
	clean := make([]string, 0, len(mounts))
	for _, m := range mounts {
		m = strings.TrimSuffix(m, "/")
		if m != "" {
			clean = append(clean, m)
		}
	}
	return &Matcher{mounts: clean}
}

// Locate 返回路径归属的挂载目录索引；不在任何挂载目录内返回 (-1, false)。
// 嵌套挂载取最长前缀（类似路由）。
func (m *Matcher) Locate(path string) (int, bool) {
	bestIdx, bestLen := -1, -1
	for i, mount := range m.mounts {
		if path == mount || strings.HasPrefix(path, mount+"/") {
			if len(mount) > bestLen {
				bestIdx, bestLen = i, len(mount)
			}
		}
	}
	return bestIdx, bestLen >= 0
}

// Engine 权限评估引擎。
type Engine struct {
	matcher *Matcher
	rules   []Rule // 按添加顺序（同优先级先匹配先赢）
}

// NewEngine 创建引擎。mounts 为空表示无项目目录（全部 ask）。
func NewEngine(mounts []string) *Engine {
	return &Engine{matcher: NewMatcher(mounts)}
}

// AddRule 添加规则。同作用域内先添加的先匹配。
func (e *Engine) AddRule(r Rule) {
	if r.Pattern != "" {
		if re, err := regexp.Compile(r.Pattern); err == nil {
			r.re = re
		}
	}
	e.rules = append(e.rules, r)
}

// classify 按"工具+参数"评估风险等级（3.2.1：等级由参数决定，不是工具名）。
func classify(op Operation) Tier {
	tool := op.Tool
	switch tool {
	case "read_file", "list_dir", "grep", "web_search":
		return Tier0
	case "write_file", "edit_file":
		// 敏感路径 → T3
		p := op.Path
		if strings.Contains(p, ".env") || strings.Contains(p, ".ssh") ||
			strings.Contains(p, ".pem") || strings.Contains(p, ".key") ||
			strings.HasPrefix(p, "/etc/") {
			return Tier3
		}
		return Tier1
	case "run_command", "shell":
		cmd := op.Command
		if strings.HasPrefix(strings.TrimSpace(cmd), "rm -rf") {
			return Tier3
		}
		if strings.Contains(cmd, "sudo") || strings.Contains(cmd, "chmod") {
			return Tier3
		}
		return Tier2
	case "web_fetch", "http_request":
		return Tier2
	default:
		return Tier2 // 未知工具保守处理
	}
}

// Locate 判断路径是否在任一挂载目录内（透传 Matcher）。
func (e *Engine) Locate(path string) bool {
	if e.matcher == nil {
		return false
	}
	_, ok := e.matcher.Locate(path)
	return ok
}

// Mounts 返回挂载目录列表。
func (e *Engine) Mounts() []string {
	if e.matcher == nil {
		return nil
	}
	return e.matcher.mounts
}

// Decide 评估一次操作。
//
// 流程（3.2.1）：
//  1. 无挂载目录 → 全部 ask
//  2. deny 硬边界：所有匹配的 deny（全局/项目/挂载）先扫——禁止不可被任何层覆盖
//  3. allow 按作用域特异性：项目临时 > 挂载目录 > 项目 > 全局，第一条匹配生效
//  4. 未匹配 → 按风险等级默认（T0 allow，其余 ask）
func (e *Engine) Decide(op Operation) Decision {
	if len(e.matcher.mounts) == 0 {
		return DecisionAsk
	}
	_, inMount := e.matcher.Locate(op.Path)

	// 阶段 2：deny 硬边界（禁止可越界，任何 allow 不可覆盖）
	for _, r := range e.rules {
		if r.Action != ActionDeny || !e.ruleMatches(r, op, inMount) {
			continue
		}
		return DecisionDeny
	}

	// 阶段 3：allow 按作用域特异性
	for _, scope := range []Scope{ScopeProjectTemp, ScopeMount, ScopeProject, ScopeGlobal} {
		for _, r := range e.rules {
			if r.Scope != scope || r.Action != ActionAllow || !e.ruleMatches(r, op, inMount) {
				continue
			}
			if classify(op) <= r.MaxTier {
				return DecisionAllow
			}
			return DecisionAsk
		}
	}

	// 阶段 4：未匹配默认
	// 项目内 T0 自动允许；项目外默认 ask（越出项目边界 = 全局说了算，无规则不默认放行）
	if inMount && classify(op) == Tier0 {
		return DecisionAllow
	}
	return DecisionAsk
}

// ruleMatches 判断规则是否适用于该操作（作用域 + 模式）。
func (e *Engine) ruleMatches(r Rule, op Operation, inMount bool) bool {
	// 项目/挂载规则只能作用于项目内路径
	if (r.Scope == ScopeProject || r.Scope == ScopeProjectTemp || r.Scope == ScopeMount) && !inMount {
		return false
	}
	// 挂载目录规则只作用于对应挂载
	if r.Scope == ScopeMount {
		idx, ok := e.matcher.Locate(op.Path)
		if !ok || e.matcher.mounts[idx] != r.ScopeID {
			return false
		}
	}
	// 模式匹配（路径或命令）
	if r.re != nil {
		target := op.Path
		if op.Command != "" {
			target = op.Command
		}
		if !r.re.MatchString(target) {
			return false
		}
	}
	return true
}
