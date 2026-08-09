// 分支布局：fork 链以树杈方式展开。
//
// 规则：
// - 每条分支是一条直链（竖直手串），x 位置按 fork 层级偏移
// - 分支根节点的 parent 位于父分支上 → 画弧线连接（fork 点）
// - 所有分支地位相同，无主链

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
  items: (LayoutResult & { status: string; nodeId: string; parentId: string })[]
  level: number      // fork 层级（主链=0）
  forkFrom?: {       // 从哪个节点分出的（画弧线用）
    parentBranch: string
    parentY: number
  }
}

export const BRANCH_X_OFFSET = 80 // 每级 fork 的水平偏移

// 弧线路径：从 fork 点 (x1,y1) 到分支首节点 (x2,y2)
export function forkArc(x1: number, y1: number, x2: number, y2: number): string {
  const dx = x2 - x1
  const cpx = x1 + dx * 0.5
  return `M${x1},${y1} C${cpx},${y1} ${cpx},${y2} ${x2},${y2}`
}

export function computeBranchLayout(
  nodes: BranchNode[],
  options?: LayoutOptions,
): { branches: BranchLayoutItem[]; width: number; height: number } {
  if (nodes.length === 0) return { branches: [], width: 200, height: 200 }

  // 1. 按 branch 分组，分支内按 seq 排序
  const groups = new Map<string, BranchNode[]>()
  for (const n of nodes) {
    if (!groups.has(n.branch)) groups.set(n.branch, [])
    groups.get(n.branch)!.push(n)
  }

  // 2. 确定层级：分支根（copiedFrom 非空或首个）的 parent 所在分支 = 父分支
  //    主分支 = copiedFrom 为空的第一个分支
  const branches: BranchLayoutItem[] = []
  const branchById = new Map<string, BranchNode[]>(groups)
  const levelMap = new Map<string, number>()
  const parentBranchMap = new Map<string, string>()

  // 找主分支：根节点没有 copiedFrom（未被 fork 出的）
  let mainBranch = ''
  for (const [b, ns] of branchById) {
    const isForked = ns.some(n => n.copiedFrom)
    if (!isForked) { mainBranch = b; break }
  }
  if (!mainBranch) mainBranch = [...branchById.keys()][0]

  levelMap.set(mainBranch, 0)

  // BFS 确定 fork 层级：某分支根的 parent 在哪个分支，level = 父 level + 1
  const computeLevels = (branchId: string) => {
    const children = [...branchById.keys()].filter(b => {
      if (b === branchId || levelMap.has(b)) return false
      const ns = branchById.get(b)!
      const root = ns.find(n => n.copiedFrom) || ns[0]
      const parent = nodes.find(n => n.id === root.parentId)
      return parent && parent.branch === branchId
    })
    for (const child of children) {
      levelMap.set(child, (levelMap.get(branchId) ?? 0) + 1)
      parentBranchMap.set(child, branchId)
      computeLevels(child)
    }
  }
  computeLevels(mainBranch)

  // 兜底：未定级的设为 1
  for (const b of branchById.keys()) {
    if (!levelMap.has(b)) levelMap.set(b, 1)
  }

  // 3. 布局每条分支
  let maxY = 0
  let maxX = 0
  for (const [b, ns] of branchById) {
    const sorted = [...ns].sort((a, c) => a.seq - c.seq)
    const lns: LayoutNode[] = sorted.map(n => ({
      id: n.id,
      important: n.kind === 'important',
      ghost: n.kind === 'ghost',
    }))
    const laid = computeLayout(lns, options)
    const level = levelMap.get(b) ?? 0
    const x = 100 + level * BRANCH_X_OFFSET

    const items = laid.map((it, i) => ({
      ...it,
      status: sorted[i].status,
      nodeId: sorted[i].id,
      parentId: sorted[i].parentId,
    }))

    // fork 弧线起点：分支根节点的 parent 在父分支上的位置
    let forkFrom: BranchLayoutItem['forkFrom']
    const parentBranch = parentBranchMap.get(b)
    if (parentBranch) {
      const root = sorted.find(n => n.copiedFrom) || sorted[0]
      const parentNode = nodes.find(n => n.id === root.parentId)
      if (parentNode) {
        const pb = branches.find(br => br.branch === parentBranch)
        const parentItem = pb?.items.find(it => it.nodeId === parentNode.id)
        if (pb && parentItem) {
          forkFrom = { parentBranch, parentY: parentItem.y }
        }
      }
    }

    const branchItem: BranchLayoutItem = { branch: b, x, items, level, forkFrom }
    branches.push(branchItem)
    if (laid.length > 0) {
      maxY = Math.max(maxY, laid[laid.length - 1].y + 20)
    }
    maxX = Math.max(maxX, x + 60)
  }

  return { branches, width: Math.max(300, maxX), height: Math.max(300, maxY) }
}
