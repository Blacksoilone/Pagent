package db

import (
	"path/filepath"
	"testing"

	"pagent/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpen_Should_CreateSchema(t *testing.T) {
	db := openTestDB(t)

	// 所有核心表应存在
	tables := []string{
		"workspace", "project", "project_mount", "chain",
		"node", "node_part", "reference",
		"background_node", "system_injection",
		"conflict", "conflict_node",
		"stateline", "file_tx", "file_tx_change",
		"permission_rule", "view_preset",
		"compression_event", "blob", "run_event",
	}
	for _, tb := range tables {
		var name string
		err := db.raw.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			tb,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", tb, err)
		}
	}
}

// ═══════════════ 项目与链 ═══════════════

func TestProject_Should_InsertAndLoad(t *testing.T) {
	db := openTestDB(t)

	p := model.NewProject("缓存方案", []string{"/work/app", "/work/lib"})
	if err := db.CreateProject(p); err != nil {
		t.Fatalf("create project: %v", err)
	}

	got, err := db.GetProject(p.ID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if got.Name != p.Name {
		t.Errorf("name = %s, want %s", got.Name, p.Name)
	}
	if len(got.Mounts) != 2 || got.Mounts[0] != "/work/app" {
		t.Errorf("mounts = %v, want [/work/app /work/lib]", got.Mounts)
	}
}

func TestChain_Should_InsertAndLoadWithNodes(t *testing.T) {
	db := openTestDB(t)

	p := model.NewProject("proj", nil)
	if err := db.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	ch := model.NewChain(p.ID, "工作链")
	if err := db.CreateChain(ch); err != nil {
		t.Fatalf("create chain: %v", err)
	}

	n1 := ch.AddNode(model.NodeKindNormal)
	n1.AppendPart(model.NodePartRoleUser, "实现 CacheService", 10)
	n1.AppendPart(model.NodePartRoleAssistant, "好的，开始", 8)
	n1.Complete()
	if err := db.InsertNode(n1); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	n2 := ch.AddNode(model.NodeKindNormal)
	n2.AppendPart(model.NodePartRoleUser, "继续", 3)
	if err := db.InsertNode(n2); err != nil {
		t.Fatalf("insert node 2: %v", err)
	}

	got, err := db.GetChain(ch.ID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if got.Name != "工作链" {
		t.Errorf("chain name = %s", got.Name)
	}

	nodes, err := db.ListChainNodes(ch.ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes len = %d, want 2", len(nodes))
	}
	// 节点包含 part
	if len(nodes[0].Parts) != 2 {
		t.Errorf("node1 parts = %d, want 2", len(nodes[0].Parts))
	}
	if nodes[0].Status != model.NodeStatusDone {
		t.Errorf("node1 status = %s, want done", nodes[0].Status)
	}
	// parent 链正确
	if nodes[1].ParentID != nodes[0].ID {
		t.Errorf("node2 parent = %s, want %s", nodes[1].ParentID, nodes[0].ID)
	}
}

// ═══════════════ 横线 ═══════════════

func TestStateline_Should_InsertAndLoad(t *testing.T) {
	db := openTestDB(t)

	sl := model.NewStateline("proj-1", "chain-1", map[string]string{
		"foo.go": "+12 -3",
	})
	if err := db.InsertStateline(sl); err != nil {
		t.Fatalf("insert stateline: %v", err)
	}

	// 消费后更新
	sl.Consume("node-42")
	if err := db.UpdateStateline(sl); err != nil {
		t.Fatalf("update stateline: %v", err)
	}

	got, err := db.GetLatestStateline("proj-1")
	if err != nil {
		t.Fatalf("get stateline: %v", err)
	}
	if got.State != model.StatelineSolid {
		t.Errorf("state = %s, want solid", got.State)
	}
	if got.ConsumedBy != "node-42" {
		t.Errorf("consumed_by = %s, want node-42", got.ConsumedBy)
	}
	if got.FileDiffs["foo.go"] != "+12 -3" {
		t.Errorf("file diff = %v", got.FileDiffs)
	}
}

func TestStateline_Should_ReturnNotFound_WhenEmpty(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.GetLatestStateline("empty-proj"); err == nil {
		t.Fatal("expected error for empty project, got nil")
	}
}

// ═══════════════ 系统注入消息 ═══════════════

func TestSystemInjection_Should_QueueAndDeliver(t *testing.T) {
	db := openTestDB(t)

	if err := db.QueueInjection("chain-1", "文件 foo.go 已变更"); err != nil {
		t.Fatalf("queue injection: %v", err)
	}
	if err := db.QueueInjection("chain-1", "第二个通知"); err != nil {
		t.Fatalf("queue injection 2: %v", err)
	}

	msgs, err := db.DrainInjections("chain-1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("injections len = %d, want 2", len(msgs))
	}
	if msgs[0] != "文件 foo.go 已变更" {
		t.Errorf("msg[0] = %q", msgs[0])
	}

	// 已投递的不能再取
	msgs, err = db.DrainInjections("chain-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("second drain len = %d, want 0", len(msgs))
	}
}

// ═══════════════ 引用 ═══════════════

func TestReference_Should_InsertAndLoad(t *testing.T) {
	db := openTestDB(t)

	ref := model.Reference{
		ID:              "ref-1",
		FromNodeID:      "node-a",
		ToNodeID:        "node-b",
		FromChainID:     "chain-a",
		ToChainID:       "chain-b",
		Kind:            "lazy",
		SummarySnapshot: "B3 的结论摘要",
	}
	if err := db.InsertReference(ref); err != nil {
		t.Fatalf("insert reference: %v", err)
	}

	refs, err := db.ListReferencesFrom("chain-a")
	if err != nil {
		t.Fatalf("list references: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs len = %d, want 1", len(refs))
	}
	if refs[0].ToNodeID != "node-b" || refs[0].SummarySnapshot != "B3 的结论摘要" {
		t.Errorf("ref = %+v", refs[0])
	}
}

func TestStateline_Should_FindPendingDraft(t *testing.T) {
	db := openTestDB(t)
	// 没有横线时返回 ErrNoStateline
	if _, err := db.GetPendingStateline("p1"); err == nil {
		t.Fatal("expected ErrNoStateline for empty project")
	}

	// 插入 draft + solid 各一条
	draft := model.NewStateline("p1", "c1", map[string]string{"a.go": "modified"})
	if err := db.InsertStateline(draft); err != nil {
		t.Fatal(err)
	}
	solid := model.NewStateline("p1", "c1", map[string]string{"b.go": "modified"})
	solid.Consume("node-1")
	if err := db.InsertStateline(solid); err != nil {
		t.Fatal(err)
	}

	// 应返回 draft（未消费的那条）
	got, err := db.GetPendingStateline("p1")
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if got.State != model.StatelineDraft {
		t.Errorf("pending state = %s, want draft", got.State)
	}
	if got.FileDiffs["a.go"] != "modified" {
		t.Errorf("pending diffs = %v", got.FileDiffs)
	}
}

func TestStateline_Should_ConsumeAndPersist(t *testing.T) {
	db := openTestDB(t)
	sl := model.NewStateline("p1", "c1", map[string]string{"a.go": "modified"})
	if err := db.InsertStateline(sl); err != nil {
		t.Fatal(err)
	}

	sl.Consume("node-42")
	if err := db.UpdateStateline(sl); err != nil {
		t.Fatal(err)
	}

	// 消费后不再是 pending
	if _, err := db.GetPendingStateline("p1"); err == nil {
		t.Error("consumed stateline should not be pending")
	}
}
