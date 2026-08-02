<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { chat, fetchChainNodes, fetchChains, fetchStatelines, type ChainDTO, type NodeDTO, type StatelineDTO } from './api'

const chains = ref<ChainDTO[]>([])
const selectedChain = ref<string>('')
const nodes = ref<NodeDTO[]>([])
const statelines = ref<StatelineDTO[]>([])
const input = ref('')
const streaming = ref(false)
const streamText = ref('')
const toolLog = ref<string[]>([])

async function refresh() {
  chains.value = await fetchChains()
  statelines.value = await fetchStatelines()
  if (!selectedChain.value && chains.value.length > 0) {
    selectedChain.value = chains.value[0].id
  }
  if (selectedChain.value) {
    nodes.value = await fetchChainNodes(selectedChain.value)
  }
}

async function selectChain(id: string) {
  selectedChain.value = id
  nodes.value = await fetchChainNodes(id)
}

async function send() {
  const msg = input.value.trim()
  if (!msg || !selectedChain.value || streaming.value) return
  input.value = ''
  streaming.value = true
  streamText.value = ''
  toolLog.value = []
  try {
    await chat(selectedChain.value, msg, (ev) => {
      if (ev.type === 'text') streamText.value += ev.data
      else if (ev.type === 'tool') toolLog.value.push(ev.data)
      else if (ev.type === 'error') toolLog.value.push('错误: ' + ev.data)
      else if (ev.type === 'done') {
        streaming.value = false
        refresh()
      }
    })
  } catch (e) {
    toolLog.value.push('请求失败: ' + String(e))
  }
  streaming.value = false
  refresh()
}

onMounted(refresh)

// ── 链图布局 ──
const layout = computed(() => {
  const items: Array<{ node: NodeDTO; y: number; isGhost: boolean }> = []
  let y = 20
  for (const n of nodes.value) {
    const isGhost = n.kind === 'ghost'
    items.push({ node: n, y, isGhost })
    y += 64
  }
  const graphBottom = y + 20
  const lines = statelines.value.map((sl, i) => ({ sl, y: graphBottom + 40 + i * 40 }))
  return { items, graphBottom, lines }
})
</script>

<template>
  <div class="app">
    <aside class="sidebar">
      <h2>工作空间</h2>
      <ul class="chain-list">
        <li v-for="c in chains" :key="c.id" :class="{ active: c.id === selectedChain }" @click="selectChain(c.id)">
          {{ c.name }}
        </li>
      </ul>
      <div class="statelines" v-if="statelines.length > 0">
        <h3>文件状态</h3>
        <div v-for="sl in statelines" :key="sl.id" class="stateline" :class="sl.state">
          <span class="sl-state">{{ sl.state === 'solid' ? '●' : '○' }}</span>
          <span class="sl-files">{{ Object.keys(sl.file_diffs).join(', ') }}</span>
        </div>
      </div>
    </aside>

    <main class="main">
      <section class="graph">
        <svg class="chain-svg" :viewBox="'0 0 600 ' + (layout.graphBottom + 80)">
          <line v-for="(it, i) in layout.items.filter(x => !x.isGhost)" :key="'line-' + i"
            x1="80" :y1="it.y" x2="80" :y2="it.y + 64" stroke="#4a90d9" stroke-width="2" opacity="0.4" />
          <g v-for="it in layout.items" :key="it.node.id" :transform="'translate(80,' + it.y + ')'">
            <circle v-if="!it.isGhost" :r="it.node.kind === 'important' ? 7 : 4"
              :fill="it.node.status === 'partial' ? '#888' : '#4a90d9'"
              :stroke="it.node.kind === 'important' ? '#2a6db5' : 'none'" stroke-width="2" />
            <circle v-else r="5" fill="none" stroke="#4a90d9" stroke-width="1.5" stroke-dasharray="3,2" opacity="0.5" />
          </g>
          <g v-for="l in layout.lines" :key="l.sl.id">
            <line x1="20" :y1="l.y" x2="580" :y2="l.y"
              :stroke="l.sl.state === 'solid' ? '#c0392b' : '#e67e22'"
              :stroke-dasharray="l.sl.state === 'solid' ? 'none' : '6,4'" stroke-width="2" />
            <text x="590" :y="l.y + 4" font-size="10" fill="#888" text-anchor="end">
              {{ Object.keys(l.sl.file_diffs).join(', ') }}
            </text>
          </g>
        </svg>
      </section>

      <section class="chat">
        <div class="tool-log" v-if="toolLog.length > 0">
          <div v-for="(t, i) in toolLog" :key="i" class="tool-item">{{ t }}</div>
        </div>
        <div class="stream" v-if="streamText">{{ streamText }}</div>
        <div class="input-row">
          <input v-model="input" placeholder="输入消息，Enter 发送" :disabled="streaming" @keydown.enter="send" />
          <button :disabled="streaming || !input.trim()" @click="send">{{ streaming ? '…' : '发送' }}</button>
        </div>
      </section>
    </main>
  </div>
</template>

<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: system-ui, sans-serif; }
.app { display: flex; height: 100vh; }
.sidebar { width: 220px; background: #f5f5f0; border-right: 1px solid #e0e0db; padding: 16px; overflow-y: auto; }
.sidebar h2 { font-size: 14px; margin-bottom: 12px; color: #333; }
.chain-list { list-style: none; }
.chain-list li { padding: 8px 10px; border-radius: 6px; cursor: pointer; font-size: 13px; color: #444; }
.chain-list li:hover { background: #ecece5; }
.chain-list li.active { background: #4a90d9; color: #fff; }
.statelines { margin-top: 24px; }
.statelines h3 { font-size: 12px; color: #888; margin-bottom: 8px; }
.stateline { font-size: 11px; padding: 4px 0; color: #555; }
.stateline .sl-state { margin-right: 6px; }
.stateline.draft .sl-state { color: #e67e22; }
.stateline.solid .sl-state { color: #c0392b; }
.main { flex: 1; display: flex; flex-direction: column; }
.graph { flex: 1; overflow: auto; background: #fafaf7; padding: 12px; }
.chain-svg { width: 100%; min-height: 200px; }
.chat { border-top: 1px solid #e0e0db; padding: 12px; background: #fff; }
.tool-log { max-height: 80px; overflow-y: auto; margin-bottom: 8px; }
.tool-item { font-size: 11px; color: #666; font-family: monospace; padding: 2px 0; }
.stream { font-size: 13px; color: #222; white-space: pre-wrap; margin-bottom: 8px; max-height: 120px; overflow-y: auto; }
.input-row { display: flex; gap: 8px; }
.input-row input { flex: 1; padding: 8px 12px; border: 1px solid #ccc; border-radius: 6px; font-size: 14px; }
.input-row button { padding: 8px 20px; background: #4a90d9; color: #fff; border: none; border-radius: 6px; cursor: pointer; }
.input-row button:disabled { background: #ccc; cursor: default; }
</style>
