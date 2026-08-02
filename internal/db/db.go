// Package db 提供 SQLite 持久化。
//
// 设计依据：docs/agent-workspace-product-draft.md 11 章（存储）。
// SQLite WAL 模式；node_part 按 seq 流式追加（WAL 语义，10.4）；
// 崩溃恢复见 11.3。
package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"

	"pagent/internal/model"
)

// DB 封装 SQLite 连接与仓储操作。
type DB struct {
	raw *sql.DB
}

// Open 打开（或创建）数据库并执行迁移。
func Open(path string) (*DB, error) {
	raw, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	d := &DB{raw: raw}
	if err := d.migrate(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// Close 关闭数据库。
func (d *DB) Close() error { return d.raw.Close() }

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS workspace (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS project (
			id TEXT PRIMARY KEY,
			workspace_id TEXT REFERENCES workspace(id),
			name TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS project_mount (
			id TEXT PRIMARY KEY,
			project_id TEXT REFERENCES project(id),
			path TEXT NOT NULL,
			UNIQUE(project_id, path)
		)`,
		`CREATE TABLE IF NOT EXISTS chain (
			id TEXT PRIMARY KEY,
			project_id TEXT REFERENCES project(id),
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS node (
			id TEXT PRIMARY KEY,
			chain_id TEXT REFERENCES chain(id),
			seq INTEGER NOT NULL DEFAULT 0,
			parent_id TEXT REFERENCES node(id),
			kind TEXT NOT NULL DEFAULT 'normal',
			status TEXT NOT NULL DEFAULT 'pending',
			title TEXT,
			summary TEXT,
			visible INTEGER NOT NULL DEFAULT 1,
			copied_from TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_node_chain ON node(chain_id, seq)`,
		`CREATE TABLE IF NOT EXISTS node_part (
			node_id TEXT REFERENCES node(id),
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			token_count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (node_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS reference (
			id TEXT PRIMARY KEY,
			from_node_id TEXT NOT NULL,
			to_node_id TEXT NOT NULL,
			from_chain_id TEXT NOT NULL,
			to_chain_id TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'lazy',
			summary_snapshot TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ref_from_chain ON reference(from_chain_id)`,
		`CREATE TABLE IF NOT EXISTS background_node (
			id TEXT PRIMARY KEY,
			scope TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'normal',
			title TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS system_injection (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chain_id TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			delivered INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS conflict (
			id TEXT PRIMARY KEY,
			file_path TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'unresolved',
			resolution_summary TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			resolved_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS conflict_node (
			conflict_id TEXT REFERENCES conflict(id),
			node_id TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS stateline (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			chain_id TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'draft',
			file_diffs TEXT NOT NULL DEFAULT '{}',
			consumed_by TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stateline_project ON stateline(project_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS file_tx (
			id TEXT PRIMARY KEY,
			node_id TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'open',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			committed_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS file_tx_change (
			tx_id TEXT REFERENCES file_tx(id),
			path TEXT NOT NULL,
			diff TEXT NOT NULL,
			PRIMARY KEY (tx_id, path)
		)`,
		`CREATE TABLE IF NOT EXISTS permission_rule (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			scope_id TEXT,
			priority INTEGER NOT NULL DEFAULT 0,
			tier_max INTEGER,
			pattern TEXT NOT NULL DEFAULT '*',
			action TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS view_preset (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			config TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS compression_event (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chain_id TEXT NOT NULL,
			from_seq INTEGER NOT NULL,
			to_seq INTEGER NOT NULL,
			summary TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS blob (
			sha TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			size INTEGER NOT NULL,
			ref_count INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS run_event (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			seq INTEGER NOT NULL DEFAULT 1,
			type TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
	}
	for _, s := range stmts {
		if _, err := d.raw.Exec(s); err != nil {
			return fmt.Errorf("exec schema: %w\n%s", err, s)
		}
	}
	return nil
}

// ═══════════════ 项目与链 ═══════════════

func (d *DB) CreateWorkspace(w *model.Workspace) error {
	_, err := d.raw.Exec(`INSERT INTO workspace (id, name) VALUES (?, ?)`, w.ID, w.Name)
	return err
}

func (d *DB) CreateProject(p *model.Project) error {
	tx, err := d.raw.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO project (id, name) VALUES (?, ?)`, p.ID, p.Name); err != nil {
		return fmt.Errorf("insert project: %w", err)
	}
	for _, m := range p.Mounts {
		if _, err := tx.Exec(`INSERT INTO project_mount (id, project_id, path) VALUES (?, ?, ?)`, model.NewID(), p.ID, m); err != nil {
			return fmt.Errorf("insert mount: %w", err)
		}
	}
	return tx.Commit()
}

func (d *DB) GetProject(id string) (*model.Project, error) {
	var p model.Project
	if err := d.raw.QueryRow(`SELECT id, name FROM project WHERE id = ?`, id).Scan(&p.ID, &p.Name); err != nil {
		return nil, err
	}
	rows, err := d.raw.Query(`SELECT path FROM project_mount WHERE project_id = ? ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		p.Mounts = append(p.Mounts, m)
	}
	return &p, rows.Err()
}

func (d *DB) CreateChain(c *model.Chain) error {
	_, err := d.raw.Exec(`INSERT INTO chain (id, project_id, name, status) VALUES (?, ?, ?, ?)`,
		c.ID, c.ProjectID, c.Name, c.Status)
	return err
}

// GetChain 加载链（不含节点）。
func (d *DB) GetChain(id string) (*model.Chain, error) {
	c := &model.Chain{}
	err := d.raw.QueryRow(`SELECT id, project_id, name, status FROM chain WHERE id = ?`, id).
		Scan(&c.ID, &c.ProjectID, &c.Name, &c.Status)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListChainNodes 加载链的全部节点（含 parts）。
func (d *DB) ListChainNodes(chainID string) ([]*model.Node, error) {
	rows, err := d.raw.Query(`SELECT id, seq, parent_id, kind, status, title, summary, visible, copied_from
		FROM node WHERE chain_id = ? ORDER BY seq`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*model.Node
	for rows.Next() {
		n := &model.Node{ChainID: chainID}
		var visible int
		var parent sql.NullString
		var seq int
		if err := rows.Scan(&n.ID, &seq, &parent, &n.Kind, &n.Status, &n.Title, &n.Summary, &visible, &n.CopiedFrom); err != nil {
			return nil, err
		}
		if parent.Valid {
			n.ParentID = parent.String
		}
		n.Visible = visible == 1
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 加载 parts
	for _, n := range nodes {
		parts, err := d.loadParts(n.ID)
		if err != nil {
			return nil, err
		}
		n.Parts = parts
	}
	return nodes, nil
}

func (d *DB) loadParts(nodeID string) ([]model.NodePart, error) {
	rows, err := d.raw.Query(`SELECT seq, role, content, token_count FROM node_part WHERE node_id = ? ORDER BY seq`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var parts []model.NodePart
	for rows.Next() {
		p := model.NodePart{NodeID: nodeID}
		if err := rows.Scan(&p.Seq, &p.Role, &p.Content, &p.TokenCount); err != nil {
			return nil, err
		}
		parts = append(parts, p)
	}
	return parts, rows.Err()
}

// InsertNode 插入节点及其 parts（单事务，WAL 语义）。
func (d *DB) InsertNode(n *model.Node) error {
	tx, err := d.raw.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	vis := 0
	if n.Visible {
		vis = 1
	}
	// parent_id 为空字符串时存 NULL（否则 SQLite 外键会尝试匹配 id=''）
	var parent any
	if n.ParentID == "" {
		parent = nil
	} else {
		parent = n.ParentID
	}
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM node WHERE chain_id = ?`, n.ChainID).Scan(&seq); err != nil {
		return fmt.Errorf("compute seq: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO node (id, chain_id, seq, parent_id, kind, status, title, summary, visible, copied_from)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.ChainID, seq, parent, n.Kind, n.Status, n.Title, n.Summary, vis, n.CopiedFrom); err != nil {
		return fmt.Errorf("insert node: %w", err)
	}
	for _, p := range n.Parts {
		if _, err := tx.Exec(`INSERT INTO node_part (node_id, seq, role, content, token_count) VALUES (?, ?, ?, ?, ?)`,
			n.ID, p.Seq, p.Role, p.Content, p.TokenCount); err != nil {
			return fmt.Errorf("insert part: %w", err)
		}
	}
	return tx.Commit()
}

// ═══════════════ 横线 ═══════════════

func (d *DB) InsertStateline(sl *model.Stateline) error {
	diffs, err := json.Marshal(sl.FileDiffs)
	if err != nil {
		return err
	}
	_, err = d.raw.Exec(`INSERT INTO stateline (id, project_id, chain_id, state, file_diffs, consumed_by)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sl.ID, sl.ProjectID, sl.ChainID, sl.State, string(diffs), sl.ConsumedBy)
	return err
}

func (d *DB) UpdateStateline(sl *model.Stateline) error {
	diffs, err := json.Marshal(sl.FileDiffs)
	if err != nil {
		return err
	}
	_, err = d.raw.Exec(`UPDATE stateline SET state = ?, file_diffs = ?, consumed_by = ? WHERE id = ?`,
		sl.State, string(diffs), sl.ConsumedBy, sl.ID)
	return err
}

// GetPendingStateline 返回项目最新未消费（draft）的横线。
// 用于：10.1.1 规则 2（虚横线存在期间不新建，并入）和消费实化。
var ErrNoStateline = errors.New("no stateline")

func (d *DB) GetPendingStateline(projectID string) (*model.Stateline, error) {
	var sl model.Stateline
	var diffs string
	var consumed sql.NullString
	err := d.raw.QueryRow(`SELECT id, project_id, chain_id, state, file_diffs, consumed_by
		FROM stateline WHERE project_id = ? AND state = 'draft'
		ORDER BY created_at DESC, id DESC LIMIT 1`, projectID).
		Scan(&sl.ID, &sl.ProjectID, &sl.ChainID, &sl.State, &diffs, &consumed)
	if err == sql.ErrNoRows {
		return nil, ErrNoStateline
	}
	if err != nil {
		return nil, err
	}
	sl.ConsumedBy = consumed.String
	if err := json.Unmarshal([]byte(diffs), &sl.FileDiffs); err != nil {
		return nil, err
	}
	return &sl, nil
}

// GetLatestStateline 返回项目最新的横线。

func (d *DB) GetLatestStateline(projectID string) (*model.Stateline, error) {
	var sl model.Stateline
	var diffs string
	var consumed sql.NullString
	err := d.raw.QueryRow(`SELECT id, project_id, chain_id, state, file_diffs, consumed_by
		FROM stateline WHERE project_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, projectID).
		Scan(&sl.ID, &sl.ProjectID, &sl.ChainID, &sl.State, &diffs, &consumed)
	if err == sql.ErrNoRows {
		return nil, ErrNoStateline
	}
	if err != nil {
		return nil, err
	}
	sl.ConsumedBy = consumed.String
	if err := json.Unmarshal([]byte(diffs), &sl.FileDiffs); err != nil {
		return nil, err
	}
	return &sl, nil
}

// ═══════════════ 系统注入消息 ═══════════════

// QueueInjection 排队一条系统注入消息（4.5）。
func (d *DB) QueueInjection(chainID, content string) error {
	_, err := d.raw.Exec(`INSERT INTO system_injection (chain_id, content) VALUES (?, ?)`, chainID, content)
	return err
}

// DrainInjections 取出并标记已投递的注入消息（顺序保持）。
func (d *DB) DrainInjections(chainID string) ([]string, error) {
	tx, err := d.raw.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id, content FROM system_injection
		WHERE chain_id = ? AND delivered = 0 ORDER BY id`, chainID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	var msgs []string
	for rows.Next() {
		var id int64
		var m string
		if err := rows.Scan(&id, &m); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE system_injection SET delivered = 1 WHERE id = ?`, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return msgs, nil
}

// ═══════════════ 引用 ═══════════════

func (d *DB) InsertReference(r model.Reference) error {
	_, err := d.raw.Exec(`INSERT INTO reference (id, from_node_id, to_node_id, from_chain_id, to_chain_id, kind, summary_snapshot)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.FromNodeID, r.ToNodeID, r.FromChainID, r.ToChainID, r.Kind, r.SummarySnapshot)
	return err
}

// ListReferencesFrom 返回某链发出的全部引用。
func (d *DB) ListReferencesFrom(chainID string) ([]model.Reference, error) {
	rows, err := d.raw.Query(`SELECT id, from_node_id, to_node_id, from_chain_id, to_chain_id, kind, summary_snapshot
		FROM reference WHERE from_chain_id = ? ORDER BY id`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []model.Reference
	for rows.Next() {
		var r model.Reference
		if err := rows.Scan(&r.ID, &r.FromNodeID, &r.ToNodeID, &r.FromChainID, &r.ToChainID, &r.Kind, &r.SummarySnapshot); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// ListProjects 列出全部项目。
func (d *DB) ListProjects() ([]*model.Project, error) {
	const q = `SELECT id, name FROM project ORDER BY created_at`
	rows, err := d.raw.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Project
	for rows.Next() {
		p := &model.Project{}
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListChainsByProject 列出指定项目的全部链。
func (d *DB) ListChainsByProject(projectID string) ([]*model.Chain, error) {
	const q = `SELECT id, project_id, name, status FROM chain WHERE project_id = ? ORDER BY created_at`
	rows, err := d.raw.Query(q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Chain
	for rows.Next() {
		c := &model.Chain{}
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListChains 列出项目下的全部链。
func (d *DB) ListChains() ([]*model.Chain, error) {
	const q = `SELECT id, project_id, name, status FROM chain ORDER BY created_at`
	rows, err := d.raw.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Chain
	for rows.Next() {
		c := &model.Chain{}
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertNodeStart 创建 pending 状态的节点（10.4 WAL：节点先落盘，part 增量追加）。
func (d *DB) InsertNodeStart(n *model.Node) error {
	vis := 0
	if n.Visible {
		vis = 1
	}
	var parent any
	if n.ParentID == "" {
		parent = nil
	} else {
		parent = n.ParentID
	}
	var seq int
	if err := d.raw.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM node WHERE chain_id = ?`, n.ChainID).Scan(&seq); err != nil {
		return fmt.Errorf("compute seq: %w", err)
	}
	if _, err := d.raw.Exec(`INSERT INTO node (id, chain_id, seq, parent_id, kind, status, title, summary, visible, copied_from)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.ChainID, seq, parent, n.Kind, n.Status, n.Title, n.Summary, vis, n.CopiedFrom); err != nil {
		return fmt.Errorf("insert node: %w", err)
	}
	return nil
}

// AppendPart 增量落盘单个 part（WAL 语义：崩溃最多丢一个 part）。
func (d *DB) AppendPart(p model.NodePart) error {
	_, err := d.raw.Exec(`INSERT INTO node_part (node_id, seq, role, content, token_count) VALUES (?, ?, ?, ?, ?)`,
		p.NodeID, p.Seq, p.Role, p.Content, p.TokenCount)
	return err
}

// UpdateNodeStatus 更新节点状态（pending → done/partial/failed）。
func (d *DB) UpdateNodeStatus(nodeID string, status model.NodeStatus) error {
	_, err := d.raw.Exec(`UPDATE node SET status = ? WHERE id = ?`, status, nodeID)
	return err
}
