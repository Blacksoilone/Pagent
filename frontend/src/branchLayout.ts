// 分支布局：倒 Y 分叉，全部纵向，支持同一锚点多次分叉（多 fork 分支）。
//
// 结构：
//   共同祖先段（A1→A2→A3）画在中间列 x=MAIN_X
//   分叉点 P 的子方向 = fork 分支们 + 主链继续段，按对称规则分配列：
//     子方向数 N 为奇数（如 3）：主链继续段竖直对齐（x=MAIN_X），fork 分支对称分布两侧
//     子方向数 N 为偶数（如 2）：全部偏移，fork 在左，主链继续段在最右
//   P 用三次贝塞尔弧线连到每个子方向的第一个节点（左上 → 右下）
//   所有链纵向（从上到下），虚节点在底部

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

export interface LayoutItem extends LayoutResult {
  status: string
  nodeId: string
  parentId: string
}

export interface BranchSegment {
  x: number
  items: LayoutItem[]
  topY: number
  bottomY: number
}

export interface ForkConn {
  from: { x: number; y: number }
  to: { x: number; y: number }
  d: string
}

export const BRANCH_X_OFFSET = 35
export const BRANCH_Y_DROP = 36
export const MAIN_X = 100

// 分叉弧线：三次贝塞尔，先垂直向下再弯向分支（左上 → 右下）。
function forkPath(from: { x: number; y: number }, to: { x: number; y: number }): string {
  const dy = to.y - from.y
  const k = Math.max(20, dy * 0.6)
  return `M ${from.x},${from.y} C ${from.x},${from.y + k} ${to.x},${to.y - k} ${to.x},${to.y}`
}

// 对称分配子方向列位置（相对 MAIN_X 的偏移倍数）。
// 奇数 N：中间 = 主链继续段（竖直对齐）；偶数 N：最右 = 主链继续段。
// 返回 positions[i] = 第 i 个子方向的偏移倍数（i = 子方向序号，最后一个为主链继续段）。
function assignPositions(n: number): number[] {
  const pos: number[] = []
  if (n % 2 === 1) {
    for (let i = 0; i < n; i++) pos.push(i - (n - 1) / 2)
    const center = (n - 1) / 2
    const last = n - 1
    ;[pos[center], pos[last]] = [pos[last], pos[center]]
  } else {
    for (let i = 0; i < n; i++) pos.push(i < n / 2 ? i - n / 2 : i - n / 2 + 1)
  }
  return pos
}

export function computeBranchLayout(
  nodes: BranchNode[],
  options?: LayoutOptions,
): { segments: BranchSegment[]; forks: ForkConn[]; width: number; height: number } {
  if (nodes.length === 0) return { segments: [], forks: [], width: 300, height: 300 }

  // 1. 按 branch 分组
  const groups = new Map<string, BranchNode[]>()
  for (const n of nodes) {
    if (!groups.has(n.branch)) groups.set(n.branch, [])
    groups.get(n.branch)!.push(n)
  }

  // 2. 找主链（根节点没有 copiedFrom 的分支）与其他分支
  let mainBranch = ''
  for (const [b, ns] of groups) {
    if (!ns.some(n => n.copiedFrom)) { mainBranch = b; break }
  }
  if (!mainBranch) mainBranch = [...groups.keys()][0]

  const mainNodes = [...(groups.get(mainBranch)!)].sort((a, c) => a.seq - c.seq)
  const forkBranches = [...groups.keys()].filter(b => b !== mainBranch)

  if (forkBranches.length === 0) {
    // 无分叉：单列线性布局
    const laid = computeLayout(mainNodes.map(n => ({
      id: n.id, important: n.kind === 'important', ghost: n.kind === 'ghost',
    })), options)
    const items: LayoutItem[] = laid.map((it, i) => ({
      ...it,
      status: mainNodes[i].status,
      nodeId: mainNodes[i].id,
      parentId: mainNodes[i].parentId,
    }))
    return {
      segments: [{
        x: MAIN_X,
        items,
        topY: items.length > 0 ? items[0].y : 8,
        bottomY: items.length > 0 ? items[items.length - 1].y : 8,
      }],
      forks: [],
      width: MAIN_X * 2 + 80,
      height: Math.max(300, (items[items.length - 1]?.y ?? 8) + 20),
    }
  }

  // 3. 找分叉点：fork 分支根（copiedFrom 非空）的 parent 在主链上的节点
  //    同一锚点多次 fork → 多个 fork 分支共享同一分叉点
  let anchor: BranchNode | undefined
  const forkGroups: BranchNode[][] = []
  for (const b of forkBranches) {
    const ns = [...(groups.get(b)!)].sort((a, c) => a.seq - c.seq)
    const root = ns.find(n => n.copiedFrom) || ns[0]
    const a = mainNodes.find(n => n.id === root.parentId)
    if (!a) continue
    if (!anchor) anchor = a
    forkGroups.push(ns)
  }
  if (!anchor || forkGroups.length === 0) {
    // 无法确定分叉点（异常数据）：退化为主链单列
    return computeBranchLayout(mainNodes, options)
  }

  // 4. 分割主链：锚点之前（含锚点）= 祖先段；锚点之后 = 主链继续段
  const anchorIdx = mainNodes.findIndex(n => n.id === anchor!.id)
  const ancestorNodes = anchorIdx >= 0 ? mainNodes.slice(0, anchorIdx + 1) : mainNodes
  const mainTailNodes = anchorIdx >= 0 ? mainNodes.slice(anchorIdx + 1) : []

  // 5. 布局祖先段（中间列）
  const layoutSeq = (ns: BranchNode[]) => {
    const lns: LayoutNode[] = ns.map(n => ({
      id: n.id, important: n.kind === 'important', ghost: n.kind === 'ghost',
    }))
    return computeLayout(lns, options)
  }

  const ancestorLaid = layoutSeq(ancestorNodes)
  const ancestorItems: LayoutItem[] = ancestorLaid.map((it, i) => ({
    ...it,
    status: ancestorNodes[i].status,
    nodeId: ancestorNodes[i].id,
    parentId: ancestorNodes[i].parentId,
  }))
  const anchorY = ancestorItems.find(it => it.nodeId === anchor!.id)?.y ?? 8

  const segments: BranchSegment[] = [{
    x: MAIN_X,
    items: ancestorItems,
    topY: ancestorItems.length > 0 ? ancestorItems[0].y : 8,
    bottomY: ancestorItems.length > 0 ? ancestorItems[ancestorItems.length - 1].y : 8,
  }]
  const forks: ForkConn[] = []
  let maxY = Math.max(300, anchorY + 20)
  if (ancestorItems.length > 0) maxY = Math.max(maxY, ancestorItems[ancestorItems.length - 1].y)

  // 6. 子方向：fork 分支们（按根 seq 排序）+ 主链继续段（最后）
  forkGroups.sort((a, c) => a[0].seq - c[0].seq)
  const dirs: { nodes: BranchNode[]; isTail: boolean }[] = [
    ...forkGroups.map(ns => ({ nodes: ns, isTail: false })),
  ]
  if (mainTailNodes.length > 0) dirs.push({ nodes: mainTailNodes, isTail: true })

  if (dirs.length === 0) {
    return { segments, forks, width: MAIN_X * 2 + 80, height: maxY }
  }

  const positions = assignPositions(dirs.length)
  dirs.forEach((dir, i) => {
    const x = MAIN_X + positions[i] * BRANCH_X_OFFSET
    const laid = layoutSeq(dir.nodes)
    let items: LayoutItem[] = laid.map((it, j) => ({
      ...it,
      status: dir.nodes[j].status,
      nodeId: dir.nodes[j].id,
      parentId: dir.nodes[j].parentId,
    }))
    if (items.length > 0) {
      const shift = anchorY + BRANCH_Y_DROP - items[0].y
      items = items.map(it => ({ ...it, y: it.y + shift }))
    }
    segments.push({
      x,
      items,
      topY: items.length > 0 ? items[0].y : anchorY + BRANCH_Y_DROP,
      bottomY: items.length > 0 ? items[items.length - 1].y : anchorY + BRANCH_Y_DROP,
    })
    const to = { x, y: items[0]?.y ?? anchorY + BRANCH_Y_DROP }
    if (!dir.isTail || x !== MAIN_X) {
      forks.push({ from: { x: MAIN_X, y: anchorY }, to, d: forkPath({ x: MAIN_X, y: anchorY }, to) })
    }
    if (items.length > 0) maxY = Math.max(maxY, items[items.length - 1].y)
  })

  return {
    segments,
    forks,
    width: MAIN_X + Math.max(...positions.map(p => Math.abs(p))) * BRANCH_X_OFFSET + 100,
    height: maxY + 20,
  }
}
