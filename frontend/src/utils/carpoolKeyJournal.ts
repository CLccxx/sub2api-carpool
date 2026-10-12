export interface NodeKeyOperation { actor: number; car: string; name: string; operation: string }
const prefix = 'carpool:node-key:'
const memory = new Map<string, NodeKeyOperation>()
const key = (actor: number, car: string) => `${prefix}${actor}:${car}`
function store(value: NodeKeyOperation) {
  const metadata = { actor: value.actor, car: value.car, name: value.name, operation: value.operation }
  memory.set(key(value.actor, value.car), metadata)
  try { sessionStorage.setItem(key(value.actor, value.car), JSON.stringify(metadata)) } catch { /* Memory fallback contains metadata only. */ }
  return { ...metadata }
}
export function readNodeKeyOperation(actor: number, car: string): NodeKeyOperation | null {
  let value: Partial<NodeKeyOperation> | null = memory.get(key(actor, car)) ?? null
  try {
    const raw = sessionStorage.getItem(key(actor, car))
    if (raw) value = JSON.parse(raw)
  } catch { /* Recover the in-memory operation when storage is unavailable. */ }
  if (!value || value.actor !== actor || value.car !== car || typeof value.name !== 'string' || !value.name.trim() || value.name.length > 100 ||
      typeof value.operation !== 'string' || !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(value.operation)) return null
  return store({ actor, car, name: value.name, operation: value.operation })
}
export function beginNodeKeyOperation(actor: number, car: string, name: string): NodeKeyOperation {
  return readNodeKeyOperation(actor, car) ?? store({ actor, car, name, operation: crypto.randomUUID() })
}
export function finishNodeKeyOperation(value: NodeKeyOperation): void {
  if (readNodeKeyOperation(value.actor, value.car)?.operation !== value.operation) return
  memory.delete(key(value.actor, value.car))
  try { sessionStorage.removeItem(key(value.actor, value.car)) } catch { /* Memory was already cleared. */ }
}
// Auth cleanup must also remove journals belonging to already-unmounted pages.
export function clearNodeKeyJournals(): void {
  memory.clear()
  try {
    for (let index = sessionStorage.length - 1; index >= 0; index--) {
      const name = sessionStorage.key(index)
      if (name?.startsWith(prefix)) sessionStorage.removeItem(name)
    }
  } catch { /* Private storage may be disabled. */ }
}
