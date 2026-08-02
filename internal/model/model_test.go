package model

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// ═══════════════ ID 生成（UUIDv7，含时序） ═══════════════

func TestNewID_Should_GenerateUniqueIDs(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := NewID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewID_Should_BeUUIDv7Format(t *testing.T) {
	// UUIDv7: 8-4-4-4-12 hex, version nibble = 7
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 100; i++ {
		id := NewID()
		if !re.MatchString(id) {
			t.Fatalf("ID %q is not valid UUIDv7 format", id)
		}
	}
}

func TestNewID_Should_BeChronologicallyOrdered(t *testing.T) {
	ids := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		ids = append(ids, NewID())
		time.Sleep(time.Millisecond)
	}
	// 时间戳部分（前 12 位 hex）应单调递增
	for i := 1; i < len(ids); i++ {
		prev, cur := ids[i-1][:12], ids[i][:12]
		if strings.Compare(prev, cur) > 0 {
			t.Fatalf("IDs not chronological: %s after %s", cur, prev)
		}
	}
}

// ═══════════════ Node 状态机 ═══════════════

func TestNode_Should_TransitionThroughLifecycle(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(n *Node) error
		wantStatus NodeStatus
		wantErr    bool
	}{
		{
			name: "complete node",
			mutate: func(n *Node) error {
				return n.Complete()
			},
			wantStatus: NodeStatusDone,
		},
		{
			name: "partial node",
			mutate: func(n *Node) error {
				return n.MarkPartial()
			},
			wantStatus: NodeStatusPartial,
		},
		{
			name: "failed node",
			mutate: func(n *Node) error {
				return n.MarkFailed()
			},
			wantStatus: NodeStatusFailed,
		},
		{
			name: "complete after partial",
			mutate: func(n *Node) error {
				if err := n.MarkPartial(); err != nil {
					return err
				}
				return n.Complete()
			},
			wantStatus: NodeStatusDone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewNode("chain-1", "parent-1", NodeKindNormal)
			err := tt.mutate(n)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", n.Status, tt.wantStatus)
			}
		})
	}
}

func TestNode_Should_RejectInvalidStateTransition(t *testing.T) {
	n := NewNode("chain-1", "", NodeKindNormal)
	if err := n.Complete(); err != nil {
		t.Fatalf("first complete failed: %v", err)
	}
	// done 状态不能再变
	if err := n.MarkPartial(); err == nil {
		t.Fatal("expected error completing an already-done node")
	}
	if err := n.MarkFailed(); err == nil {
		t.Fatal("expected error failing an already-done node")
	}
}

// ═══════════════ NodePart 追加 ═══════════════

func TestNode_Should_AppendPartsInSequence(t *testing.T) {
	n := NewNode("chain-1", "", NodeKindNormal)

	if err := n.AppendPart(NodePartRoleUser, "hello", 10); err != nil {
		t.Fatalf("append user failed: %v", err)
	}
	if err := n.AppendPart(NodePartRoleAssistant, "hi", 5); err != nil {
		t.Fatalf("append assistant failed: %v", err)
	}
	if err := n.AppendPart(NodePartRoleToolResult, `<tool_result source="read_file">...</tool_result>`, 20); err != nil {
		t.Fatalf("append tool failed: %v", err)
	}

	if len(n.Parts) != 3 {
		t.Fatalf("parts len = %d, want 3", len(n.Parts))
	}
	for i, p := range n.Parts {
		if p.Seq != i+1 {
			t.Fatalf("part %d seq = %d, want %d", i, p.Seq, i+1)
		}
		if p.NodeID != n.ID {
			t.Fatalf("part NodeID = %s, want %s", p.NodeID, n.ID)
		}
	}
}

func TestNode_Should_RejectAppendAfterCompletion(t *testing.T) {
	n := NewNode("chain-1", "", NodeKindNormal)
	_ = n.Complete()
	if err := n.AppendPart(NodePartRoleUser, "too late", 1); err == nil {
		t.Fatal("expected error appending to completed node")
	}
}

// ═══════════════ Chain 模型 ═══════════════

func TestChain_Should_AddNodeAndTrackTail(t *testing.T) {
	ch := NewChain("project-1", "推理链")

	n1 := ch.AddNode(NodeKindNormal)
	if ch.TailID() != n1.ID {
		t.Fatalf("tail after first = %s, want %s", ch.TailID(), n1.ID)
	}
	if n1.ParentID != "" {
		t.Fatalf("first node parent = %q, want empty", n1.ParentID)
	}

	n2 := ch.AddNode(NodeKindNormal)
	if ch.TailID() != n2.ID {
		t.Fatalf("tail after second = %s, want %s", ch.TailID(), n2.ID)
	}
	if n2.ParentID != n1.ID {
		t.Fatalf("second node parent = %s, want %s", n2.ParentID, n1.ID)
	}
}

func TestChain_Should_ComputeBranchesFromFork(t *testing.T) {
	ch := NewChain("project-1", "工作链")
	a1 := ch.AddNode(NodeKindNormal)
	a2 := ch.AddNode(NodeKindNormal)
	a3 := ch.AddNode(NodeKindNormal)

	// fork a2：在 a1 后创建平行分支
	fork := ch.ForkAfter(a1, a2)
	if fork.ParentID != a1.ID {
		t.Fatalf("fork parent = %s, want %s", fork.ParentID, a1.ID)
	}
	if fork.ID == a2.ID {
		t.Fatal("fork node ID collides with original")
	}
	// 尾节点仍应是原分支的 a3
	if ch.TailID() != a3.ID {
		t.Fatalf("tail after fork = %s, want %s", ch.TailID(), a3.ID)
	}
}

// ═══════════════ Stateline（横线） ═══════════════

func TestStateline_Should_StartDraftAndBecomeSolid(t *testing.T) {
	sl := NewStateline("project-1", "chain-1", map[string]string{
		"foo.go": "+12 -3",
	})
	if sl.State != StatelineDraft {
		t.Fatalf("state = %s, want draft", sl.State)
	}

	sl.Consume("node-42")
	if sl.State != StatelineSolid {
		t.Fatalf("state = %s, want solid", sl.State)
	}
	if sl.ConsumedBy != "node-42" {
		t.Fatalf("consumed_by = %s, want node-42", sl.ConsumedBy)
	}
}

func TestStateline_Should_RejectConsumptionOfSolid(t *testing.T) {
	sl := NewStateline("project-1", "chain-1", nil)
	sl.Consume("node-1")
	// 已实化，不能再消费（10.1.3 实横线不可退回，但允许多节点基于它工作）
	sl.Consume("node-2")
	if sl.ConsumedBy != "node-1" {
		t.Fatalf("consumed_by changed to %s, want node-1", sl.ConsumedBy)
	}
}
