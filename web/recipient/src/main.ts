import './styles.css'

type PublicTransfer = {
  transfer_id: string
  status: string
  from_agent_id: string
  file_name: string
  file_size_bytes: number
  mime_type?: string
  file_sha256?: string
  expires_at: string
  password_required: boolean
}

type ReceiverTicket = {
  transfer_id: string
  receiver_ticket: string
  expires_at: string
  websocket_url: string
  browser_agent_id: string
}

type SignalingEnvelope = {
  type: string
  agent_id?: string
  device_id?: string
  transfer_id?: string
  from_agent_id?: string
  to_agent_id?: string
  payload?: unknown
}

type ChunkFrame = {
  v: number
  type: 'chunk'
  transfer_id: string
  seq?: number
  offset?: number
  data?: string
  enc?: { alg: string; nonce: string }
}

type ManifestFrame = {
  v: number
  type: 'manifest'
  transfer_id: string
  total_bytes: number
  chunk_count: number
  sha256: string
}

type BrowserReceiveState = {
  transfer: PublicTransfer
  browserAgentID: string
  peer?: RTCPeerConnection
  channel?: RTCDataChannel
  key: CryptoKey
  chunks: Uint8Array<ArrayBuffer>[]
  totalBytes: number
  nextSeq: number
  hasherReady: boolean
  pendingICE: RTCIceCandidateInit[]
  messageQueue: Promise<void>
  completed: boolean
}

const defaultBrowserAgentID = 'browser_recipient'
const defaultICEServers: RTCIceServer[] = [{ urls: 'stun:stun.l.google.com:19302' }]
const app = document.querySelector<HTMLDivElement>('#app')
const token = readTokenFromPath()
let socket: WebSocket | undefined
let currentDeviceID = ''
let receiveState: BrowserReceiveState | undefined

function readTokenFromPath(): string {
  const raw = window.location.pathname.replace(/^\/p\//, '').split('/')[0] ?? ''
  try {
    return decodeURIComponent(raw)
  } catch {
    return ''
  }
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let size = value / 1024
  let idx = 0
  while (size >= 1024 && idx < units.length - 1) {
    size /= 1024
    idx += 1
  }
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[idx]}`
}

function clearApp(className = 'card'): HTMLElement | undefined {
  if (!app) return undefined
  app.textContent = ''
  const section = document.createElement('section')
  section.className = className
  section.setAttribute('aria-labelledby', 'recipient-title')
  app.append(section)
  return section
}

function appendText<K extends keyof HTMLElementTagNameMap>(parent: HTMLElement, tag: K, text: string, className?: string): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag)
  if (className) el.className = className
  el.textContent = text
  parent.append(el)
  return el
}

function renderLoading() {
  const section = clearApp()
  if (!section) return
  appendText(section, 'p', 'postamat', 'eyebrow')
  appendText(section, 'h1', 'Secure transfer').id = 'recipient-title'
  appendText(section, 'p', 'Loading transfer metadata…')
}

function renderError(message: string) {
  const section = clearApp('card card--error')
  if (!section) return
  appendText(section, 'p', 'postamat', 'eyebrow')
  appendText(section, 'h1', 'Transfer unavailable').id = 'recipient-title'
  appendText(section, 'p', message)
}

function renderTransfer(transfer: PublicTransfer) {
  const section = clearApp()
  if (!section) return

  appendText(section, 'p', 'postamat secure link', 'eyebrow')
  appendText(section, 'h1', `Receive ${transfer.file_name}`).id = 'recipient-title'

  const metadata = document.createElement('dl')
  metadata.className = 'metadata'
  appendMetadata(metadata, 'From', transfer.from_agent_id)
  appendMetadata(metadata, 'Size', formatBytes(transfer.file_size_bytes))
  appendMetadata(metadata, 'Status', transfer.status)
  appendMetadata(metadata, 'Expires', new Date(transfer.expires_at).toLocaleString())
  section.append(metadata)

  const consentLabel = document.createElement('label')
  consentLabel.className = 'consent'
  const consent = document.createElement('input')
  consent.id = 'consent'
  consent.type = 'checkbox'
  consentLabel.append(consent, document.createTextNode('I consent to receive this file from the sender.'))
  section.append(consentLabel)

  if (transfer.password_required) {
    const password = document.createElement('input')
    password.id = 'password'
    password.className = 'password'
    password.type = 'password'
    password.placeholder = 'Password'
    password.autocomplete = 'current-password'
    section.append(password)
  }

  const connect = document.createElement('button')
  connect.id = 'connect'
  connect.type = 'button'
  connect.textContent = 'Connect browser receiver'
  section.append(connect)

  const status = appendText(section, 'p', '', 'status')
  status.id = 'status'
  status.setAttribute('role', 'status')

  const log = document.createElement('ol')
  log.id = 'event-log'
  log.className = 'event-log'
  section.append(log)

  connect.addEventListener('click', async () => {
    const password = document.querySelector<HTMLInputElement>('#password')?.value ?? ''
    if (!consent.checked) {
      setStatus('Consent is required before connecting.')
      return
    }
    try {
      connect.disabled = true
      setStatus('Issuing receiver ticket…')
      const issued = await issueReceiverTicket(consent.checked, password)
      setStatus(`Connecting receiver socket. Ticket expires ${new Date(issued.expires_at).toLocaleTimeString()}.`)
      connectReceiver(transfer, issued)
    } catch (err) {
      connect.disabled = false
      setStatus(err instanceof Error ? err.message : 'Could not issue receiver ticket.')
    }
  })
}

function appendMetadata(parent: HTMLElement, label: string, value: string) {
  const row = document.createElement('div')
  appendText(row, 'dt', label)
  appendText(row, 'dd', value)
  parent.append(row)
}

function setStatus(message: string) {
  const status = document.querySelector<HTMLParagraphElement>('#status')
  if (status) status.textContent = message
}

function logEvent(message: string) {
  const log = document.querySelector<HTMLOListElement>('#event-log')
  if (!log) return
  const item = document.createElement('li')
  item.textContent = message
  log.prepend(item)
}

function connectReceiver(transfer: PublicTransfer, issued: ReceiverTicket) {
  socket?.close()
  currentDeviceID = `browser_${crypto.randomUUID?.() ?? Math.random().toString(36).slice(2)}`
  const ws = new WebSocket(receiverWebSocketURL(issued.receiver_ticket))
  socket = ws

  ws.addEventListener('open', () => {
    void (async () => {
      const browserAgentID = issued.browser_agent_id || defaultBrowserAgentID
      sendEnvelope(ws, { type: 'agent.hello', agent_id: browserAgentID, device_id: currentDeviceID })
      await prepareBrowserReceive(transfer, browserAgentID)
      sendEnvelope(ws, {
        type: 'transfer.accepted',
        transfer_id: transfer.transfer_id,
        from_agent_id: browserAgentID,
        to_agent_id: transfer.from_agent_id,
      })
      setStatus('Receiver signaling socket connected.')
      logEvent('Sent browser receiver hello and transfer acceptance.')
    })()
  })
  ws.addEventListener('message', (event: MessageEvent<string>) => {
    void handleSignalingMessage(ws, transfer, issued.browser_agent_id || defaultBrowserAgentID, event.data)
  })
  ws.addEventListener('close', () => {
    setStatus('Receiver signaling socket closed.')
    logEvent('Socket closed.')
  })
  ws.addEventListener('error', () => {
    setStatus('Receiver signaling socket error.')
    logEvent('Socket error.')
  })
}

function receiverWebSocketURL(receiverTicket: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const path = `/api/public/transfers/${encodeURIComponent(token)}/receiver/ws`
  return `${protocol}//${window.location.host}${path}?ticket=${encodeURIComponent(receiverTicket)}`
}

async function handleSignalingMessage(ws: WebSocket, transfer: PublicTransfer, browserAgentID: string, raw: string) {
  let envelope: SignalingEnvelope
  try {
    envelope = JSON.parse(raw) as SignalingEnvelope
  } catch {
    logEvent('Received malformed signaling message.')
    return
  }

  switch (envelope.type) {
    case 'agent.presence':
      logEvent(`Presence acknowledged for ${envelope.agent_id ?? browserAgentID}.`)
      break
    case 'transfer.offer':
      if (!isExpectedSenderEnvelope(envelope, transfer, browserAgentID)) {
        logEvent('Ignored transfer offer outside this transfer.')
        return
      }
      logEvent('Transfer offer received; accepting transfer.')
      await prepareBrowserReceive(transfer, browserAgentID)
      sendEnvelope(ws, {
        type: 'transfer.accepted',
        transfer_id: transfer.transfer_id,
        from_agent_id: browserAgentID,
        to_agent_id: transfer.from_agent_id,
      })
      break
    case 'webrtc.offer':
      if (!isExpectedSenderEnvelope(envelope, transfer, browserAgentID)) {
        logEvent('Ignored WebRTC offer outside this transfer.')
        return
      }
      logEvent('WebRTC offer received; creating browser answer.')
      await acceptWebRTCOffer(ws, transfer, browserAgentID, envelope.payload)
      break
    case 'webrtc.ice':
      if (!isExpectedSenderEnvelope(envelope, transfer, browserAgentID)) {
        logEvent('Ignored ICE candidate outside this transfer.')
        return
      }
      const state = receiveState
      const candidate = envelope.payload as RTCIceCandidateInit
      if (state?.peer) {
        await state.peer.addIceCandidate(candidate)
      } else if (state) {
        state.pendingICE.push(candidate)
      }
      logEvent('ICE candidate accepted.')
      break
    case 'transfer.started':
      logEvent('Transfer started.')
      break
    case 'transfer.progress':
      logEvent(`Progress: ${JSON.stringify(envelope.payload ?? {})}`)
      break
    case 'transfer.completed':
      setStatus('Transfer completed.')
      logEvent('Transfer completed.')
      break
    case 'error':
      setStatus('Signaling error received.')
      logEvent(`Error: ${JSON.stringify(envelope.payload ?? {})}`)
      break
    default:
      logEvent(`Received ${envelope.type}.`)
  }
}

function readBrowserTransferKey(): Uint8Array<ArrayBuffer> {
  const params = new URLSearchParams(window.location.hash.replace(/^#/, ''))
  const encoded = params.get('postamat_key') ?? ''
  if (!encoded) throw new Error('Missing browser receive key in URL fragment.')
  const key = base64ToBytes(encoded)
  if (key.byteLength !== 32) throw new Error('Invalid browser receive key length.')
  return key
}

async function prepareBrowserReceive(transfer: PublicTransfer, browserAgentID: string) {
  if (receiveState?.transfer.transfer_id === transfer.transfer_id) return
  const rawKey = readBrowserTransferKey()
  const key = await crypto.subtle.importKey('raw', rawKey, 'AES-GCM', false, ['decrypt'])
  receiveState = { transfer, browserAgentID, key, chunks: [], totalBytes: 0, nextSeq: 0, hasherReady: true, pendingICE: [], messageQueue: Promise.resolve(), completed: false }
  setStatus('Browser receiver key loaded; waiting for WebRTC offer.')
}

async function acceptWebRTCOffer(ws: WebSocket, transfer: PublicTransfer, browserAgentID: string, payload: unknown) {
  await prepareBrowserReceive(transfer, browserAgentID)
  const state = receiveState
  if (!state) throw new Error('Browser receive state was not initialized.')
  const peer = new RTCPeerConnection({ iceServers: defaultICEServers })
  state.peer?.close()
  state.peer = peer
  peer.onicecandidate = (event) => {
    if (!event.candidate) return
    sendEnvelope(ws, {
      type: 'webrtc.ice',
      transfer_id: transfer.transfer_id,
      from_agent_id: browserAgentID,
      to_agent_id: transfer.from_agent_id,
      payload: event.candidate.toJSON(),
    })
  }
  peer.ondatachannel = (event) => {
    state.channel = event.channel
    state.channel.binaryType = 'arraybuffer'
    state.channel.onopen = () => {
      setStatus('Data channel open; receiving encrypted chunks…')
      logEvent(`Data channel ${state.channel?.label ?? ''} opened.`)
    }
    state.channel.onmessage = (message) => {
      const payload = message.data
      state.messageQueue = state.messageQueue.then(() => handleDataChannelMessage(ws, state, payload))
    }
    state.channel.onerror = () => {
      if (state.completed) return
      setStatus('Data channel error.')
      logEvent('Data channel error.')
    }
  }
  await peer.setRemoteDescription(payload as RTCSessionDescriptionInit)
  for (const candidate of state.pendingICE.splice(0)) {
    await peer.addIceCandidate(candidate)
  }
  const answer = await peer.createAnswer()
  await peer.setLocalDescription(answer)
  sendEnvelope(ws, {
    type: 'webrtc.answer',
    transfer_id: transfer.transfer_id,
    from_agent_id: browserAgentID,
    to_agent_id: transfer.from_agent_id,
    payload: peer.localDescription?.toJSON() ?? answer,
  })
  setStatus('WebRTC answer sent; waiting for data channel.')
}

async function handleDataChannelMessage(ws: WebSocket, state: BrowserReceiveState, data: string | ArrayBuffer | Blob) {
  try {
    const raw = await dataChannelPayloadToString(data)
    const frame = JSON.parse(raw) as ChunkFrame | ManifestFrame
    if (frame.transfer_id !== state.transfer.transfer_id) throw new Error('Unexpected transfer id.')
    if (frame.type === 'chunk') {
      await acceptBrowserChunk(ws, state, frame)
      return
    }
    if (frame.type === 'manifest') {
      await completeBrowserReceive(ws, state, frame)
      return
    }
    throw new Error('Unsupported frame type.')
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Browser receive failed.'
    setStatus(message)
    logEvent(message)
    sendEnvelope(ws, {
      type: 'transfer.failed',
      transfer_id: state.transfer.transfer_id,
      from_agent_id: state.browserAgentID,
      to_agent_id: state.transfer.from_agent_id,
      payload: { reason: message },
    })
  }
}

async function acceptBrowserChunk(ws: WebSocket, state: BrowserReceiveState, frame: ChunkFrame) {
  const seq = frame.seq ?? 0
  const offset = frame.offset ?? 0
  if (seq !== state.nextSeq) throw new Error('Unexpected chunk sequence.')
  if (offset !== state.totalBytes) throw new Error('Unexpected chunk offset.')
  if (!frame.data || !frame.enc || frame.enc.alg !== 'AES-256-GCM') throw new Error('Encrypted chunk is required.')
  const ciphertext = base64ToBytes(frame.data)
  const plaintext = new Uint8Array(await crypto.subtle.decrypt({
    name: 'AES-GCM',
    iv: base64ToBytes(frame.enc.nonce),
    additionalData: chunkAAD(frame.transfer_id, seq, offset),
  }, state.key, ciphertext))
  if (state.totalBytes + plaintext.byteLength > state.transfer.file_size_bytes) throw new Error('Received bytes exceed declared file size.')
  state.chunks.push(plaintext as Uint8Array<ArrayBuffer>)
  state.totalBytes += plaintext.byteLength
  state.nextSeq += 1
  setStatus(`Receiving… ${formatBytes(state.totalBytes)} of ${formatBytes(state.transfer.file_size_bytes)}`)
  sendEnvelope(ws, {
    type: 'transfer.progress',
    transfer_id: state.transfer.transfer_id,
    from_agent_id: state.browserAgentID,
    to_agent_id: state.transfer.from_agent_id,
    payload: { progress_bytes: state.totalBytes },
  })
}

async function completeBrowserReceive(ws: WebSocket, state: BrowserReceiveState, frame: ManifestFrame) {
  if (frame.total_bytes !== state.totalBytes || frame.chunk_count !== state.nextSeq) {
    throw new Error(`Manifest size/count mismatch: received ${state.totalBytes} B/${state.nextSeq} chunks, manifest ${frame.total_bytes} B/${frame.chunk_count} chunks.`)
  }
  const received = concatChunks(state.chunks, state.totalBytes)
  const digest = bytesToHex(new Uint8Array(await crypto.subtle.digest('SHA-256', received)))
  if (digest !== frame.sha256) throw new Error('Manifest SHA-256 mismatch.')
  const blob = new Blob([received], { type: state.transfer.mime_type || 'application/octet-stream' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = state.transfer.file_name
  link.textContent = `Download ${state.transfer.file_name}`
  link.className = 'download-link'
  document.querySelector('.card')?.append(link)
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
  setStatus(`Transfer complete. SHA-256 ${digest}`)
  logEvent(`Received ${formatBytes(state.totalBytes)}; SHA-256 verified.`)
  state.completed = true
  sendEnvelope(ws, {
    type: 'transfer.completed',
    transfer_id: state.transfer.transfer_id,
    from_agent_id: state.browserAgentID,
    to_agent_id: state.transfer.from_agent_id,
  })
}

async function dataChannelPayloadToString(data: string | ArrayBuffer | Blob): Promise<string> {
  if (typeof data === 'string') return data
  if (data instanceof Blob) return await data.text()
  return new TextDecoder().decode(data)
}

function chunkAAD(transferID: string, sequence: number, offset: number): Uint8Array<ArrayBuffer> {
  return new TextEncoder().encode(`postamat:p2p:v1:${transferID}:${sequence}:${offset}`) as Uint8Array<ArrayBuffer>
}

function base64ToBytes(value: string): Uint8Array<ArrayBuffer> {
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = normalized.padEnd(normalized.length + ((4 - normalized.length % 4) % 4), '=')
  const binary = atob(padded)
  const out = new Uint8Array(new ArrayBuffer(binary.length))
  for (let i = 0; i < binary.length; i += 1) out[i] = binary.charCodeAt(i)
  return out
}

function concatChunks(chunks: Uint8Array<ArrayBuffer>[], totalBytes: number): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(new ArrayBuffer(totalBytes))
  let offset = 0
  for (const chunk of chunks) {
    out.set(chunk, offset)
    offset += chunk.byteLength
  }
  return out
}

function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}

function isExpectedSenderEnvelope(envelope: SignalingEnvelope, transfer: PublicTransfer, browserAgentID: string): boolean {
  return envelope.transfer_id === transfer.transfer_id &&
    envelope.from_agent_id === transfer.from_agent_id &&
    envelope.to_agent_id === browserAgentID
}

function sendEnvelope(ws: WebSocket, envelope: SignalingEnvelope) {
  ws.send(JSON.stringify(envelope))
}

async function loadMetadata(): Promise<PublicTransfer> {
  const res = await fetch(`/api/public/transfers/${encodeURIComponent(token)}`, { headers: { Accept: 'application/json' } })
  if (!res.ok) throw new Error('This transfer link is invalid, expired, or unavailable.')
  return await res.json() as PublicTransfer
}

async function issueReceiverTicket(consent: boolean, password: string): Promise<ReceiverTicket> {
  const res = await fetch(`/api/public/transfers/${encodeURIComponent(token)}/receiver-ticket`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ consent, password }),
  })
  if (!res.ok) throw new Error('Receiver consent/password was rejected.')
  return await res.json() as ReceiverTicket
}

async function main() {
  if (!token) {
    renderError('Missing public transfer token.')
    return
  }
  renderLoading()
  try {
    renderTransfer(await loadMetadata())
  } catch (err) {
    renderError(err instanceof Error ? err.message : 'Could not load transfer metadata.')
  }
}

void main()
