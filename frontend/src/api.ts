// Pagent API 客户端（对接 internal/server 的 HTTP/SSE 接口）

export interface ChainDTO {
  id: string
  name: string
  status: string
}

export interface PartDTO {
  seq: number
  role: string
  content: string
}

export interface NodeDTO {
  id: string
  parent_id?: string
  kind: string
  status: string
  title?: string
  summary?: string
  visible: boolean
  parts: PartDTO[]
}

export interface StatelineDTO {
  id: string
  state: string
  file_diffs: Record<string, string>
  consumed_by?: string
}

export async function fetchChains(): Promise<ChainDTO[]> {
  const res = await fetch('/api/chains')
  if (!res.ok) throw new Error(`fetch chains: ${res.status}`)
  return res.json()
}

export async function fetchChainNodes(chainId: string): Promise<NodeDTO[]> {
  const res = await fetch(`/api/chains/${chainId}/nodes`)
  if (!res.ok) throw new Error(`fetch nodes: ${res.status}`)
  return res.json()
}

export async function fetchStatelines(): Promise<StatelineDTO[]> {
  const res = await fetch('/api/statelines')
  if (!res.ok) throw new Error(`fetch statelines: ${res.status}`)
  return res.json()
}

export type ChatEvent =
  | { type: 'text'; data: string }
  | { type: 'tool'; data: string }
  | { type: 'done'; data: string }
  | { type: 'error'; data: string }

// 发送对话，通过 onEvent 接收 SSE 流式事件
export async function chat(
  chainId: string,
  message: string,
  onEvent: (ev: ChatEvent) => void,
): Promise<void> {
  const res = await fetch('/api/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ chain_id: chainId, message }),
  })
  if (!res.ok || !res.body) {
    throw new Error(`chat: ${res.status}`)
  }
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    // 按 \n\n 切分 SSE 事件
    let idx: number
    while ((idx = buf.indexOf('\n\n')) >= 0) {
      const raw = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      const lines = raw.split('\n')
      let event = 'message'
      let data = ''
      for (const line of lines) {
        if (line.startsWith('event: ')) event = line.slice(7).trim()
        else if (line.startsWith('data: ')) data = line.slice(6).trim()
      }
      if (data === '') continue
      let parsed = data
      try {
        parsed = JSON.parse(data)
      } catch {
        // 保持原始字符串
      }
      onEvent({ type: event, data: parsed } as ChatEvent)
    }
  }
}
