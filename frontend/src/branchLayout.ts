// 分支布局：fork 链以"倒 Y"方式展开。
//
// 规则：
// - 两条链都从 fork 点分叉向下（二叉树式），无主链
// - fork 分支的节点序列 = [fork 点锚点] + 分支自己的节点
//   （锚点 = 分支根节点的 parent，位于父分支上）
// - 分支从锚点的 y 位置开始向下，虚节点在每条链的末尾（时间向下）
// - 分支 x 位置：主链=100，fork 分支=180（二叉树分叉）

import { computeLayout, type LayoutNode, type LayoutResult, type LayoutOptions } from './layout'

export interface BranchNode {
  id: string
  seq: number
  parentId: string
  branch: string
  kind: string
  status: string
  copiedFrom: string
  parts: { seq: number; role: string; content: string }[]
}

export interface BranchLayoutItem {
  branch: string
  x: number          // 分支的 x 位置
  items: (LayoutResult & { status: string; nodeId: string; parentId: string; label: string })[]
  anchorNodeId: string // 分支的起点节点（fork 分支 = 父分支上的锚点）
  topY: number       // 链线起点（锚点的 y）
  bottomY: number    // 链线终点（该分支最后节点）
}

export const BRANCH_X_OFFSET = 80 // 每级 fork 的水平偏移

// 弧线路径：从父链锚点 (x1,y1) 到子链锚点 (x2,y2)——实际是共享锚点，不需要弧线
export function forkArc(x1: number, y1: number, x2: number, y2: number): string {
  const dx = x2 - x1
  const cpx = x1 + dx * 0.5
  return `M${x1},${y1} C${cpx},${y1} ${cpx},${y2} ${x2},${y2}`
}

export function computeBranchLayout(
  nodes: BranchNode[],
  options?: LayoutOptions,
  labelFor?: (nodeId: string, depth: number) => string,
): { branches: BranchLayoutItem[]; width: number; height: number } {
  if (nodes.length === 0) return { branches: [], width: 200, height: 200 }

  // 1. 按 branch 分组，分支内按 seq 排序
  const groups = new Map<string, BranchNode[]>()
  for (const n of nodes) {
    if (!groups.has(n.branch)) groups.set(n.branch, [])
    groups.get(n.branch)!.push(n)
  }

  // 2. 确定分支层级与父分支
  //    主分支 = 根节点没有 copiedFrom（未被 fork 出）
  const levelMap = new Map<string, number>()
  const parentBranchMap = new Map<string, string>()
  const anchorMap = new Map<string, BranchNode>() // 分支 → 锚点（父分支上的节点）

  // 找主分支
  let mainBranch = ''
  for (const [b, ns] of groups) {
    const isForked = ns.some(n => n.copiedFrom)
    if (!isForked) { mainBranch = b; break }
  }
  if (!mainBranch) mainBranch = [...groups.keys()][0]
  levelMap.set(mainBranch, 0)

  // 计算每级 fork：分支根的 parent = 锚点（位于父分支）
  for (const [b, ns] of groups) {
    if (b === mainBranch) continue
    const root = ns.find(n => n.copiedFrom) || ns[0]
    const anchor = nodes.find(n => n.id === root.parentId)
    if (anchor) {
      anchorMap.set(b, anchor)
      parentBranchMap.set(b, anchor.branch)
      levelMap.set(b, (levelMap.get(anchor.branch) ?? 0) + 1)
    }
  }
  // 兜底：未定级的设为 1
  for (const b of groups.keys()) {
    if (!levelMap.has(b)) levelMap.set(b, 1)
  }

  // 3. 布局每条分支
  const branches: BranchLayoutItem[] = []
  let maxY = 0
  let maxX = 0

  // 主分支先布局（其他分支的锚点 y 依赖它）
  const order = [mainBranch, ...[...groups.keys()].filter(b => b !== mainBranch)]

  for (const b of order) {
    const ns = groups.get(b)!
    const sorted = [...ns].sort((a, c) => a.seq - c.seq)
    const level = levelMap.get(b) ?? 0
    const x = 100 + level * BRANCH_X_OFFSET

    // 节点序列：fork 分支 = [锚点] + 分支自己的节点
    // （主分支 = 自己的节点）
    const seqNodes: BranchNode[] = []
    const anchor = anchorMap.get(b)
    if (anchor) seqNodes.push(anchor)
    seqNodes.push(...sorted)

    const lns: LayoutNode[] = seqNodes.map(n => ({
      id: n.id,
      important: n.kind === 'important',
      ghost: n.kind === 'ghost',
    }))
    const laid = computeLayout(lns, options)

    // 计算深度（从链头到各节点）
    const depthOf = new Map<string, number>()
    const parentOf = new Map<string, string>()
    for (const n of seqNodes) if (n.parentId) parentOf.set(n.id, n.parentId)
    const visit = (id: string): number => {
      const c2 = depthOf.get(id)
      if (c2 !== undefined) return c2
      const p = parentOf.get(id)
      const d = p ? visit(p) + 1 : 1
      depthOf.set(id, d)
      return d
    }
    for (const n of seqNodes) visit(n.id)

    let items = laid.map((it, i) => ({
      ...it,
      status: seqNodes[i].status,
      nodeId: seqNodes[i].id,
      parentId: seqNodes[i].parentId,
      label: labelFor ? labelFor(seqNodes[i].id, depthOf.get(seqNodes[i].id) ?? 0) : '',
    }))

    // 锚点 y = 父分支上锚点的 y（对齐分叉点）
    // 布局从顶部开始，需要平移使锚点对齐父分支位置
    if (anchor) {
      const parentBranch = parentBranchMap.get(b)
      const pb = branches.find(br => br.branch === parentBranch)
      const anchorItem = pb?.items.find(it => it.nodeId === anchor.id)
      if (pb && anchorItem) {
        const shift = anchorItem.y - items[0].y
        items = items.map(it => ({ ...it, y: it.y + shift }))
      }
    }

    // 分支链线范围：从锚点开始到分支末尾
    const topY = items.length > 0 ? items[0].y : 8
    const bottomY = items.length > 0 ? items[items.length - 1].y : 8

    branches.push({
      branch: b, x, items,
      anchorNodeId: anchor ? anchor.id : (sorted[0]?.id ?? ''),
      topY, bottomY,
    })

    if (items.length > 0) {
      maxY = Math.max(maxY, items[items.length - 1].y + 20)
    }
    maxX = Math.max(maxX, x + 60)
  }

  return { branches, width: Math.max(300, maxX), height: Math.max(300, maxY) }
}
