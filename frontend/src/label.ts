// 组合式节点标号（链名哈希_深度_节点哈希）。
//
// 设计：docs 3.4 节点 ID（16B 二进制 = 链名哈希2B + 深度2B + 节点哈希12B）。
// 前端展示用派生标号：各部分取前几位 hex，用 _ 连接。

export interface LabelNode {
  id: string
  parentId: string
  chainKey: string // 链标识（用于哈希）
}

// 简单字符串哈希（FNV-1a 32位）
function fnv1a(str: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

// 深度：从链头到该节点的祖先数（fork 变体共享深度，A4 和 A4' 都是 4）
export function computeDepths(nodes: LabelNode[]): Map<string, number> {
  const parentOf = new Map<string, string>()
  for (const n of nodes) {
    if (n.parentId) parentOf.set(n.id, n.parentId)
  }
  const depthOf = new Map<string, number>()
  const visit = (id: string): number => {
    const cached = depthOf.get(id)
    if (cached !== undefined) return cached
    const pid = parentOf.get(id)
    const d = pid ? visit(pid) + 1 : 1
    depthOf.set(id, d)
    return d
  }
  for (const n of nodes) visit(n.id)
  return depthOf
}

// 链名哈希：链标识哈希后取 hex 前 4 位
export function chainHashHex(chainKey: string, len = 4): string {
  return fnv1a(chainKey).toString(16).padStart(8, '0').slice(0, len)
}

// 节点哈希：节点 ID 取 hex 前 n 位（ID 本身含足够随机性）
export function nodeHashHex(nodeId: string, len = 8): string {
  // 去掉可能的连字符，取前 len 字符
  const clean = nodeId.replace(/[-_]/g, '')
  return clean.slice(0, len).padEnd(len, '0')
}

// 完整组合标号：chainHash_depth_nodeHash
export function nodeLabel(nodeId: string, depth: number, chainKey: string): string {
  return `${chainHashHex(chainKey)}_${String(depth).padStart(4, '0')}_${nodeHashHex(nodeId)}`
}
