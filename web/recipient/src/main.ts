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

const defaultBrowserAgentID = 'browser_recipient'
const app = document.querySelector<HTMLDivElement>('#app')
const token = readTokenFromPath()
let socket: WebSocket | undefined
let currentDeviceID = ''

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
    sendEnvelope(ws, { type: 'agent.hello', agent_id: issued.browser_agent_id || defaultBrowserAgentID, device_id: currentDeviceID })
    setStatus('Receiver signaling socket connected.')
    logEvent('Sent browser receiver hello.')
  })
  ws.addEventListener('message', (event: MessageEvent<string>) => {
    handleSignalingMessage(ws, transfer, issued.browser_agent_id || defaultBrowserAgentID, event.data)
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

function handleSignalingMessage(ws: WebSocket, transfer: PublicTransfer, browserAgentID: string, raw: string) {
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
      logEvent('WebRTC offer received; answer/data-channel handling arrives in Milestone 8.')
      break
    case 'webrtc.ice':
      if (!isExpectedSenderEnvelope(envelope, transfer, browserAgentID)) {
        logEvent('Ignored ICE candidate outside this transfer.')
        return
      }
      logEvent('ICE candidate received; WebRTC handling arrives in Milestone 8.')
      break
    case 'error':
      setStatus('Signaling error received.')
      logEvent(`Error: ${JSON.stringify(envelope.payload ?? {})}`)
      break
    default:
      logEvent(`Received ${envelope.type}.`)
  }
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
