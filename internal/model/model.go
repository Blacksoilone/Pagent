// Package model 定义 Pagent 的领域模型。
//
// 设计依据：docs/agent-workspace-product-draft.md
// 核心原则：节点图是不可变的追加记录（2.1）——节点一经创建不可修改内容，
// 修正通过 fork，删除通过隐藏。
package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ═══════════════ ID 生成（UUIDv7，含时序） ═══════════════

// NewID 生成 UUIDv7（RFC 9562）：48 位 unix 毫秒时间戳 + version 7 + 随机位。
// 全局唯一、含时序、不可变。设计依据：3.4 节点 ID。
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[6:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	ts := uint64(time.Now().UnixMilli())
	b[0] = byte(ts >> 40)
	b[1] = byte(ts >> 32)
	b[2] = byte(ts >> 24)
	b[3] = byte(ts >> 16)
	b[4] = byte(ts >> 8)
	b[5] = byte(ts)
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx

	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}

// ═══════════════ 枚举 ═══════════════

// NodeKind 节点类型。
type NodeKind string

const (
	NodeKindNormal    NodeKind = "normal"     // 普通节点（一次对话）
	NodeKindImportant NodeKind = "important"  // 重要节点（被提升）
	NodeKindGhost     NodeKind = "ghost"      // 虚节点（尚未发生的下一次 turn）
	NodeKindGitCommit NodeKind = "git_commit" // Git 提交节点（3.11）
	NodeKindConflict  NodeKind = "conflict"   // 冲突节点（跨链文件冲突）
)

// NodeStatus 节点状态。
type NodeStatus string

const (
	NodeStatusPending NodeStatus = "pending" // 执行中（agentic loop 未完成）
	NodeStatusDone    NodeStatus = "done"    // 完成
	NodeStatusPartial NodeStatus = "partial" // 中断（9.3.5）
	NodeStatusFailed  NodeStatus = "failed"  // 失败（模型/工具全失败）
)

// NodePartRole part 角色（WAL 语义的对话记录，3.12）。
type NodePartRole string

const (
	NodePartRoleUser       NodePartRole = "user"        // 用户输入
	NodePartRoleAssistant  NodePartRole = "assistant"   // 模型输出
	NodePartRoleToolCall   NodePartRole = "tool_call"   // 工具调用请求
	NodePartRoleToolResult NodePartRole = "tool_result" // 工具执行结果
	NodePartRoleSystem     NodePartRole = "system"      // 系统消息
	NodePartRoleEvent      NodePartRole = "event"       // 运行事件（fallback 等）
)

// StatelineState 横线状态（10.1）。
type StatelineState string

const (
	StatelineDraft StatelineState = "draft" // 虚横线：未消费
	StatelineSolid StatelineState = "solid" // 实横线：已消费/已提交
)

// ═══════════════ 基础结构 ═══════════════

// Workspace 工作空间（3.1）。
type Workspace struct {
	ID   string
	Name string
}

// Project 项目（3.2）：稳定逻辑身份 + 一个或多个挂载目录。
type Project struct {
	ID     string
	Name   string
	Mounts []string
}

// Chain 链（3.3）：持续存在的工作线路。
type Chain struct {
	ID        string
	ProjectID string
	Name      string
	Status    string // active | archived（12.2）

	mu    sync.Mutex
	nodes []*Node // 按创建顺序
}

// Node 节点（3.4-3.6）。不可变：创建后可追加 part，不可修改已有 part。
type Node struct {
	ID         string
	ChainID    string
	Seq        int    // 链内序号（db 生成）
	ParentID   string // 前驱节点（"" = 链根）
	Branch     string // 分支 ID（默认 = 首节点 ID；fork 创建新分支）
	Kind       NodeKind
	Status     NodeStatus
	Title      string     // 重要节点标题
	Summary    string     // 重要节点总结（冻结摘要）
	Visible    bool       // 隐藏标记（9.2，默认 true）
	CopiedFrom string     // 独立/fork 来源节点 ID
	Parts      []NodePart // 对话 part（WAL 流式落盘）
	mu         sync.Mutex
}

// NodePart 节点内容的一部分（3.12 会话运行时）。
type NodePart struct {
	NodeID     string
	Seq        int
	Role       NodePartRole
	Content    string
	TokenCount int
}

// Reference 跨链引用（4 章）：惰性引用 + 冻结摘要。
type Reference struct {
	ID              string
	FromNodeID      string
	ToNodeID        string
	FromChainID     string
	ToChainID       string
	Kind            string // lazy
	SummarySnapshot string
}

// Stateline 横线（10.1）：项目内唯一的文件状态锚点。
type Stateline struct {
	ID         string
	ProjectID  string
	ChainID    string // 触发提交的链
	State      StatelineState
	FileDiffs  map[string]string // 文件 → 变更摘要（diff 元数据）
	ConsumedBy string            // 消费节点 ID（实化依据）
	CreatedAt  time.Time
}

// ═══════════════ 构造与行为 ═══════════════

func NewWorkspace(name string) *Workspace {
	return &Workspace{ID: NewID(), Name: name}
}

func NewProject(name string, mounts []string) *Project {
	return &Project{ID: NewID(), Name: name, Mounts: mounts}
}

func NewChain(projectID, name string) *Chain {
	return &Chain{
		ID:        NewID(),
		ProjectID: projectID,
		Name:      name,
		Status:    "active",
	}
}

// AddNode 在链尾追加节点（不可变原则：只追加）。
func (c *Chain) AddNode(kind NodeKind) *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	parent := ""
	if len(c.nodes) > 0 {
		parent = c.nodes[len(c.nodes)-1].ID
	}
	n := NewNode(c.ID, parent, kind)
	c.nodes = append(c.nodes, n)
	return n
}

// ForkAfter 在 anchor 之后创建平行分支节点（3.9.1 fork 语义）。
// 不改变链尾；返回新分支根节点。
func (c *Chain) ForkAfter(anchor, _ *Node) *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := NewNode(c.ID, anchor.ID, NodeKindNormal)
	n.CopiedFrom = anchor.ID
	// 注意：fork 节点不加入 c.nodes 主线（独立分支），
	// 链尾保持原分支；分支归属由数据库引用关系表达。
	return n
}

// TailID 返回链尾节点 ID（主线最后一个节点）。
func (c *Chain) TailID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.nodes) == 0 {
		return ""
	}
	return c.nodes[len(c.nodes)-1].ID
}

// Nodes 返回链上全部主线节点（副本，防并发写）。
func (c *Chain) Nodes() []*Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Node, len(c.nodes))
	copy(out, c.nodes)
	return out
}

func NewNode(chainID, parentID string, kind NodeKind) *Node {
	n := &Node{
		ID:       NewID(),
		ChainID:  chainID,
		ParentID: parentID,
		Kind:     kind,
		Status:   NodeStatusPending,
		Visible:  true,
	}
	n.Branch = n.ID // 默认分支 = 自身 ID（首节点即分支根）
	return n
}

var (
	ErrNodeAlreadyDone   = errors.New("node already completed")
	ErrNodeAppendClosed  = errors.New("cannot append part to completed/failed node")
	ErrInvalidTransition = errors.New("invalid node status transition")
)

// AppendPart 追加对话 part（WAL 流式落盘，10.4）。
func (n *Node) AppendPart(role NodePartRole, content string, tokens int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Status == NodeStatusDone || n.Status == NodeStatusFailed {
		return ErrNodeAppendClosed
	}
	n.Parts = append(n.Parts, NodePart{
		NodeID:     n.ID,
		Seq:        len(n.Parts) + 1,
		Role:       role,
		Content:    content,
		TokenCount: tokens,
	})
	return nil
}

// Complete 完成节点（loop 结束）。
func (n *Node) Complete() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Status == NodeStatusDone || n.Status == NodeStatusFailed {
		return ErrNodeAlreadyDone
	}
	n.Status = NodeStatusDone
	return nil
}

// MarkPartial 标记为中断节点（9.3.5）。
func (n *Node) MarkPartial() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Status == NodeStatusDone || n.Status == NodeStatusFailed {
		return ErrInvalidTransition
	}
	n.Status = NodeStatusPartial
	return nil
}

// MarkFailed 标记失败（模型/工具全部失败，11.3）。
func (n *Node) MarkFailed() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Status == NodeStatusDone {
		return ErrInvalidTransition
	}
	n.Status = NodeStatusFailed
	return nil
}

// Hide 隐藏节点（9.2 视觉删除：数据不动，只改可见性）。
func (n *Node) Hide() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Visible = false
}

// Promote 提升为重要节点（3.5）。
func (n *Node) Promote(title, summary string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Kind = NodeKindImportant
	n.Title = title
	n.Summary = summary
}

func NewStateline(projectID, chainID string, diffs map[string]string) *Stateline {
	return &Stateline{
		ID:        NewID(),
		ProjectID: projectID,
		ChainID:   chainID,
		State:     StatelineDraft,
		FileDiffs: diffs,
		CreatedAt: time.Now().UTC(),
	}
}

// Consume 消费横线（虚 → 实）。只允许一次（10.1.3 实横线不可退回）。
func (s *Stateline) Consume(nodeID string) {
	if s.State == StatelineDraft {
		s.State = StatelineSolid
		s.ConsumedBy = nodeID
	}
}
