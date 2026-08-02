<script setup lang="ts">
import { computed, onMounted, ref, nextTick } from 'vue'
import { chat, fetchChainNodes, fetchChains, fetchProjects, type ChainDTO, type NodeDTO, type ProjectDTO } from './api'
import { computeLayout, DIAM_SMALL, DIAM_BIG, type LayoutNode } from './layout'

// 模板可访问的直径常量
const diamSmall = DIAM_SMALL
const diamBig = DIAM_BIG

// ── 状态 ──
const projects = ref<ProjectDTO[]>([])
const chains = ref<ChainDTO[]>([])
const selectedChain = ref<string>('')
const nodes = ref<NodeDTO[]>([])
const messages = ref<Array<{ role: string; content: string }>>([])
const input = ref('')
const streaming = ref(false)
const toolLog = ref<string[]>([])
const view = ref<'chat' | 'graph'>('chat')
const expandedProject = ref<string | null>(null) // 展开的项目（树形）
const scrollBox = ref<HTMLElement | null>(null)

async function refresh() {
  const [pr, chs] = await Promise.all([fetchProjects(), fetchChains()])
  projects.value = pr
  chains.value = chs
  if (projects.value.length > 0 && !expandedProject.value) {
    expandedProject.value = projects.value[0].id
  }
  if (chains.value.length > 0 && !chains.value.find(c => c.id === selectedChain.value)) {
    selectedChain.value = chains.value[0].id
  }
  if (selectedChain.value) {
    nodes.value = await fetchChainNodes(selectedChain.value)
  }
}

async function ensureChain(): Promise<string | null> {
  if (selectedChain.value) return selectedChain.value
  if (chains.value.length === 0) {
    const res = await fetch('/api/chains', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: '对话 1' }),
    })
    if (!res.ok) return null
    const ch = await res.json() as ChainDTO
    await refresh()
    selectedChain.value = ch.id
    return ch.id
  }
  selectedChain.value = chains.value[0].id
  return selectedChain.value
}

async function selectChain(id: string) {
  if (streaming.value) return
  selectedChain.value = id
  nodes.value = await fetchChainNodes(id)
  messages.value = []
}

async function send() {
  const msg = input.value.trim()
  if (!msg || streaming.value) return
  const chainId = await ensureChain()
  if (!chainId) return
  input.value = ''
  streaming.value = true
  toolLog.value = []
  messages.value.push({ role: 'user', content: msg })
  messages.value.push({ role: 'assistant', content: '' })
  const aiIdx = messages.value.length - 1
  try {
    await chat(chainId, msg, (ev) => {
      if (ev.type === 'text') {
        messages.value[aiIdx].content += ev.data
        scrollToBottom()
      } else if (ev.type === 'tool') {
        toolLog.value.push(ev.data)
      } else if (ev.type === 'error') {
        toolLog.value.push('错误: ' + ev.data)
      } else if (ev.type === 'done') {
        streaming.value = false
        refresh()
      }
    })
  } catch (e) {
    messages.value[aiIdx].content = '请求失败: ' + String(e)
  }
  streaming.value = false
  refresh()
}

function scrollToBottom() {
  nextTick(() => {
    if (scrollBox.value) scrollBox.value.scrollTop = scrollBox.value.scrollHeight
  })
}

interface LayoutResultWithStatus {
  node: LayoutNode
  y: number
  status: string
}

const braceletItems = computed<LayoutResultWithStatus[]>(() => {
  const lns: LayoutNode[] = nodes.value.map(n => ({
    id: n.id,
    important: n.kind === 'important',
    ghost: n.kind === 'ghost',
  }))
  const laid = computeLayout(lns)
  const statusById = new Map(nodes.value.map(n => [n.id, n.status]))
  return laid.map(it => ({ ...it, status: statusById.get(it.node.id) || 'done' } as LayoutResultWithStatus))
})

const braceletHeight = computed(() => {
  if (braceletItems.value.length === 0) return 80
  const last = braceletItems.value[braceletItems.value.length - 1]
  return last.y + 20
})

onMounted(refresh)
</script>

<template>
  <div class="app">
    <!-- 左侧边栏：项目 + session 列表 -->
    <aside class="sidebar">
      <div class="sidebar-brand">Pagent</div>

      <!-- 上：项目列表（树形） -->
      <div class="sidebar-section">
        <div class="section-label">项目</div>
        <div v-for="p in projects" :key="p.id" class="project-item">
          <div class="project-row" @click="expandedProject = expandedProject === p.id ? null : p.id">
            <span class="caret" :class="{ open: expandedProject === p.id }">▸</span>
            <span class="project-name">{{ p.name }}</span>
          </div>
          <div v-if="expandedProject === p.id" class="project-chains">
            <div v-for="c in p.chains" :key="c.id"
              class="chain-item" :class="{ active: c.id === selectedChain }"
              @click="selectChain(c.id)">
              {{ c.name }}
            </div>
          </div>
        </div>
        <div class="empty-hint" v-if="projects.length === 0">尚无项目</div>
      </div>

      <!-- 下：session 列表 -->
      <div class="sidebar-section bottom">
        <div class="section-label">会话</div>
        <div v-for="c in chains" :key="c.id"
          class="session-item" :class="{ active: c.id === selectedChain }"
          @click="selectChain(c.id)">
          {{ c.name }}
        </div>
        <div class="empty-hint" v-if="chains.length === 0">尚无会话</div>
      </div>
    </aside>

    <!-- 主区 -->
    <div class="main">
      <!-- 顶部栏 -->
      <header class="topbar">
        <div class="current-chain">{{ chains.find(c => c.id === selectedChain)?.name || '新对话' }}</div>
        <button class="view-btn" @click="view = view === 'chat' ? 'graph' : 'chat'">
          {{ view === 'chat' ? '链视图' : '对话' }}
        </button>
      </header>

      <!-- 对话视图 -->
      <main v-if="view === 'chat'" class="chat-main" ref="scrollBox">
        <div class="messages">
          <div v-if="messages.length === 0" class="welcome">
            <h1>Pagent</h1>
            <p>多 Agent 对话工作空间</p>
          </div>
          <template v-for="(m, i) in messages" :key="i">
            <div class="msg-row" :class="m.role">
              <div class="avatar" v-if="m.role === 'assistant'">P</div>
              <div class="bubble">{{ m.content }}<span v-if="streaming && i === messages.length - 1" class="cursor">▍</span></div>
            </div>
          </template>
          <div class="tool-log" v-if="toolLog.length > 0">
            <div v-for="(t, i) in toolLog" :key="'t' + i" class="tool-item">{{ t }}</div>
          </div>
        </div>
      </main>

      <!-- 链视图（手串） -->
      <main v-else class="graph-main">
        <div class="graph-toolbar">
          <span>{{ chains.find(c => c.id === selectedChain)?.name || '未选择' }}</span>
          <span class="node-count">{{ nodes.length }} 个节点</span>
        </div>
        <div class="bracelet-wrap">
          <svg class="chain-svg" :viewBox="'0 0 200 ' + (braceletHeight + 40)" width="200">
            <!-- 链线（串绳） -->
            <line x1="100" :y1="8" x2="100" :y2="braceletHeight - 8"
              stroke="#d8d3c8" stroke-width="2.5" stroke-linecap="round" opacity="0.6" />
            <!-- 珠子 -->
            <g v-for="it in braceletItems" :key="it.node.id" :transform="'translate(100,' + it.y + ')'">
              <circle
                :r="(it.node.important || it.node.ghost) ? diamBig / 2 : diamSmall / 2"
                :fill="it.node.important ? '#2a6db5' : (it.node.ghost ? 'none' : '#7fa8d9')"
                :stroke="it.node.important ? '#1a4f8a' : (it.node.ghost ? '#7fa8d9' : 'none')"
                :stroke-width="it.node.ghost ? 2 : (it.node.important ? 0 : 0)"
                :stroke-dasharray="it.node.ghost ? '3,2' : 'none'"
                :opacity="it.status === 'partial' ? 0.5 : 1"
              />
            </g>
          </svg>
        </div>
      </main>

      <!-- 输入区 -->
      <footer class="input-area">
        <div class="input-box">
          <textarea
            v-model="input" rows="1" placeholder="输入消息…"
            :disabled="streaming"
            @keydown.enter.exact.prevent="send"
          ></textarea>
          <button class="send-btn" :disabled="streaming || !input.trim()" @click="send">
            <svg v-if="!streaming" width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
              <path d="M3.4 20.4l17.45-7.48a1 1 0 000-1.84L3.4 3.6a.993.993 0 00-1.39.91L2 9.12c0 .5.37.93.87.99L17 12 2.87 13.88c-.5.07-.87.5-.87 1l.01 4.61c0 .71.73 1.2 1.39.91z"/>
            </svg>
            <span v-else class="spinner"></span>
          </button>
        </div>
        <div class="input-hint">Enter 发送 · Shift+Enter 换行</div>
      </footer>
    </div>
  </div>
</template>

<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #fff; }
.app { display: flex; height: 100vh; }

/* 左侧边栏 */
.sidebar {
  width: 260px; background: #f9f9fb; border-right: 1px solid #ececf1;
  display: flex; flex-direction: column; flex-shrink: 0;
}
.sidebar-brand { padding: 14px 16px 10px; font-size: 15px; font-weight: 600; color: #111; border-bottom: 1px solid #ececf1; }
.sidebar-section { padding: 8px 0; overflow-y: auto; }
.sidebar-section.bottom { border-top: 1px solid #ececf1; flex: 1; }
.section-label { padding: 8px 16px 4px; font-size: 11px; font-weight: 600; color: #8e8ea0; text-transform: uppercase; letter-spacing: 0.04em; }
.project-row { display: flex; align-items: center; gap: 6px; padding: 7px 16px; cursor: pointer; font-size: 13px; color: #333; }
.project-row:hover { background: #f0f0f4; }
.caret { display: inline-block; transition: transform 0.15s; font-size: 10px; color: #8e8ea0; }
.caret.open { transform: rotate(90deg); }
.project-name { font-weight: 500; }
.project-chains { padding-left: 30px; }
.chain-item { padding: 6px 10px; border-radius: 6px; cursor: pointer; font-size: 13px; color: #555; }
.chain-item:hover { background: #f0f0f4; }
.chain-item.active { background: #e8f0fa; color: #1a5fb4; font-weight: 500; }
.session-item { padding: 8px 16px; cursor: pointer; font-size: 13px; color: #555; }
.session-item:hover { background: #f0f0f4; }
.session-item.active { background: #e8f0fa; color: #1a5fb4; font-weight: 500; }
.empty-hint { padding: 8px 16px; font-size: 12px; color: #b0b0c0; }

/* 主区 */
.main { flex: 1; display: flex; flex-direction: column; min-width: 0; }
.topbar {
  display: flex; align-items: center; padding: 8px 20px;
  border-bottom: 1px solid #ececf1; background: #fff;
}
.current-chain { font-size: 14px; font-weight: 500; color: #333; }
.view-btn {
  margin-left: auto; padding: 6px 14px; border: 1px solid #d9d9e3;
  border-radius: 8px; background: #fff; color: #333; font-size: 13px; cursor: pointer;
}
.view-btn:hover { background: #f5f5f7; }

.chat-main { flex: 1; overflow-y: auto; }
.messages { max-width: 760px; margin: 0 auto; padding: 32px 24px 80px; }
.welcome { text-align: center; padding-top: 100px; }
.welcome h1 { font-size: 28px; font-weight: 600; color: #111; }
.welcome p { font-size: 14px; color: #8e8ea0; margin-top: 8px; }

.msg-row { display: flex; gap: 12px; margin-bottom: 24px; }
.msg-row.user { justify-content: flex-end; }
.msg-row.user .bubble { background: #f0f0f5; color: #111; border-radius: 12px 12px 2px 12px; }
.msg-row.assistant { justify-content: flex-start; }
.avatar {
  width: 30px; height: 30px; border-radius: 8px; background: #111;
  color: #fff; display: flex; align-items: center; justify-content: center;
  font-size: 13px; font-weight: 600; flex-shrink: 0;
}
.bubble {
  padding: 10px 16px; border-radius: 12px; font-size: 14px; line-height: 1.6;
  white-space: pre-wrap; word-break: break-word; max-width: 80%;
}
.cursor { animation: blink 1s infinite; }
@keyframes blink { 50% { opacity: 0; } }
.tool-log { margin-top: 8px; }
.tool-item { font-size: 12px; color: #8e8ea0; font-family: ui-monospace, monospace; padding: 3px 0; }

.graph-main { flex: 1; overflow: auto; padding: 16px; }
.graph-toolbar { display: flex; gap: 16px; font-size: 13px; color: #555; padding: 8px 0; }
.bracelet-wrap { display: flex; justify-content: center; }
.chain-svg { min-height: 300px; }

.input-area { border-top: 1px solid #ececf1; padding: 12px 20px 16px; background: #fff; }
.input-box {
  max-width: 760px; margin: 0 auto; display: flex; align-items: flex-end; gap: 8px;
  border: 1px solid #d9d9e3; border-radius: 12px; padding: 8px 8px 8px 16px; background: #fff;
}
.input-box:focus-within { border-color: #4a90d9; box-shadow: 0 0 0 2px rgba(74,144,217,0.15); }
.input-box textarea {
  flex: 1; border: none; outline: none; resize: none; font-size: 14px;
  font-family: inherit; line-height: 1.5; max-height: 160px; background: transparent;
}
.send-btn {
  width: 34px; height: 34px; border-radius: 50%; border: none;
  background: #111; color: #fff; display: flex; align-items: center;
  justify-content: center; cursor: pointer; flex-shrink: 0;
}
.send-btn:disabled { background: #d9d9e3; cursor: default; }
.spinner {
  width: 14px; height: 14px; border: 2px solid #fff; border-top-color: transparent;
  border-radius: 50%; animation: spin 0.8s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
.input-hint { max-width: 760px; margin: 6px auto 0; font-size: 11px; color: #b0b0c0; }
</style>
