// 手串式链布局算法。
//
// 规则（设计讨论）：
// - 绝大多数是普通节点，间距必须极小：相邻普通点中心距 = 普通点直径 d
// - 重要节点/虚节点之间有最小距离：中心距 ≥ 6 × 重要点直径 D（下限，可被小点推远）
// - 相邻节点中心距 = 两半径之和（相切不重叠）：小-小 = d，小-大 = d/2 + D/2
// - 重要节点之间的普通节点在满足相切约束下尽量均匀填充
// - 虚节点（ghost）沿用重要节点的距离规则与尺寸（实化前是可操作对象）
// - 链的起始节点与末尾节点自动按大节点处理（避免两端小点悬挂）
//
// 输出：每个节点的 y 中心坐标（沿链方向），按原节点顺序返回。

export interface LayoutNode {
  id: string
  important: boolean
  ghost: boolean
}

export interface LayoutResult {
  node: LayoutNode
  y: number
  big: boolean // 是否按大节点规则布局/渲染
}

export const DIAM_SMALL = 8     // 普通节点直径 d（默认值）
export const DIAM_BIG = 16      // 重要节点/虚节点直径 D（默认值）
export const GAP_IMPORTANT = 6 * DIAM_BIG // 重要节点最小中心距 6D（默认值）

export interface LayoutOptions {
  diamSmall?: number  // 普通节点直径
  diamBig?: number    // 重要节点/虚节点直径
  gapImportant?: number // 重要节点最小中心距
  lineWidth?: number  // 链线粗细
  colorSmall?: string // 普通节点颜色
  colorBig?: string   // 重要节点颜色
  colorLine?: string  // 链线颜色
}

export const DEFAULT_OPTIONS: LayoutOptions = {
  diamSmall: DIAM_SMALL,
  diamBig: DIAM_BIG,
  gapImportant: GAP_IMPORTANT,
  lineWidth: 2.5,
  colorSmall: '#7fa8d9',
  colorBig: '#2a6db5',
  colorLine: '#d8d3c8',
}

export function computeLayout(nodes: LayoutNode[], options?: Partial<LayoutOptions>): LayoutResult[] {
  const opt = {
    diamSmall: options?.diamSmall ?? DIAM_SMALL,
    diamBig: options?.diamBig ?? DIAM_BIG,
    gapImportant: options?.gapImportant ?? GAP_IMPORTANT,
    lineWidth: options?.lineWidth ?? DEFAULT_OPTIONS.lineWidth,
    colorSmall: options?.colorSmall ?? DEFAULT_OPTIONS.colorSmall,
    colorBig: options?.colorBig ?? DEFAULT_OPTIONS.colorBig,
    colorLine: options?.colorLine ?? DEFAULT_OPTIONS.colorLine,
  }
  const d = opt.diamSmall
  const D = opt.diamBig
  const gapImp = opt.gapImportant
  const R_SMALL = d / 2
  const R_BIG = D / 2

  const n = nodes.length
  if (n === 0) return []

  // 该位置是否按大节点处理（重要/虚/首/尾）
  const isBigAt = (i: number): boolean =>
    nodes[i].important || nodes[i].ghost || i === 0 || i === n - 1

  // 大节点位置（首尾自动包含）
  const bigIdx: number[] = []
  for (let i = 0; i < n; i++) {
    if (isBigAt(i)) bigIdx.push(i)
  }

  const result: LayoutResult[] = new Array(n)

  // 首节点（必是大节点）
  result[0] = { node: nodes[0], y: R_BIG, big: true }

  // 每个区间 [bigIdx[s], bigIdx[s+1]]：大节点 A → 大节点 B，中间 k 个普通节点
  for (let s = 0; s < bigIdx.length - 1; s++) {
    const a = bigIdx[s]
    const b = bigIdx[s + 1]
    const k = b - a - 1
    const startY = result[a].y

    // 基础段 = A→n1 相切 + 中间相切 + nk→B 相切；k=0 时两个大节点相切 2R_BIG
    const baseTotal = k === 0
      ? 2 * R_BIG
      : (R_SMALL + R_BIG) + (k - 1) * d + (R_SMALL + R_BIG)
    const segLen = Math.max(gapImp, baseTotal)
    const extra = segLen - baseTotal

    // 各段长度：首尾相切段 + 中间相切段。
    // 富余空间只分配到首尾段（大点与小点之间）——小点之间保持固定间距 d，
    // 调整大点间距不影响小点节奏。
    const segCount = k + 1
    const segs: number[] = []
    for (let i = 0; i < segCount; i++) {
      let base: number
      if (k === 0) {
        base = 2 * R_BIG
      } else {
        base = (i === 0 || i === segCount - 1) ? R_SMALL + R_BIG : d
      }
      segs.push(base)
    }
    if (segCount >= 2 && extra > 0) {
      segs[0] += extra / 2
      segs[segCount - 1] += extra / 2
    } else if (segCount === 1) {
      segs[0] += extra
    }

    let pos = startY
    for (let i = 0; i < k; i++) {
      pos += segs[i]
      result[a + 1 + i] = { node: nodes[a + 1 + i], y: pos, big: false }
    }
    pos += segs[k]
    result[b] = { node: nodes[b], y: pos, big: true }
  }

  // 兜底：若某位置未赋值（如仅 1 个节点），按大节点补
  for (let i = 0; i < n; i++) {
    if (!result[i]) {
      result[i] = { node: nodes[i], y: R_BIG + i * d, big: isBigAt(i) }
    }
  }

  return result
}
