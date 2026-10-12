import { apiClient } from './client'

export interface CarInput {
  name: string
  capacity: number
  price_minor: number
  total_price_minor?: number | null
  currency: 'CNY'
  service_days: number
  recruit_until: string
  delivery_hours: number
  refund_policy: string
}
export type CarStatus = 'draft' | 'recruiting' | 'awaiting_deployment' | 'needs_review' | 'closed_unfilled' | 'cancelled' | 'active' | 'paused' | 'completed'
export interface Car extends CarInput {
  enrollment_mode: 'test' | 'manual'
  id: string
  status: CarStatus
  version: number
  created_at: string
  published_at: string | null
  full_at: string | null
  delivery_due: string | null
  confirmed_count: number
  held_count: number
  available_count: number
  reservable: boolean
  server_time: string
}
export interface Membership {
  user_email?: string
  payment?: ManualPayment
  receipt?: ManualReceipt
  refund_record?: ManualRefundRecord
  id: string
  car_id: string
  user_id: number
  seat_no: number
  order_id: string
  status: 'waiting_deployment' | 'cancelled' | 'active' | 'paused' | 'revoked' | 'expired'
  created_at: string
  service_start: string | null
  service_end: string | null
}
export interface Order {
  id: string
  car_id: string
  user_id: number
  seat_no: number
  mode: 'test' | 'manual'
  status: 'pending' | 'confirmed' | 'expired' | 'cancelled'
  snapshot: CarInput
  expires_at: string | null
  confirmed_at: string | null
  created_at: string
  membership: Membership | null
  payment?: ManualPayment
}
export interface ManualPayment {
  amount_minor: number; currency: 'CNY'; channel: 'alipay' | 'wechat'; received_at: string; recorded_at: string
  refund: { status: 'pending' | 'refunded'; amount_minor: number; requested_at: string | null; refunded_at: string | null } | null
}
export interface ManualReceipt { recorded_by: number; reference: string; note: string }
export interface ManualRefundRecord { reason: string; requested_by: number | null; recorded_at: string | null; recorded_by: number | null; reference: string }
export interface ManualOrder extends Order { user_email: string; receipt: ManualReceipt; refund_record: ManualRefundRecord | null }
export interface EnrollmentInput { user_id: number; amount_minor: number; channel: 'alipay' | 'wechat'; received_at: string; reference: string; note: string; confirm_received: boolean }
export interface WithdrawalInput { reason: string; confirm: boolean }
export interface RefundInput { amount_minor: number; refunded_at: string; reference: string; confirm_refunded: boolean }
export interface ManualOrderQuery extends PageQuery { car_id?: string; refund_status?: '' | 'none' | 'pending' | 'refunded' }
export interface Page<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  server_time: string
}
export interface PageQuery { page?: number; page_size?: number; status?: string }
export type NodePageQuery = Pick<PageQuery, 'page' | 'page_size'>
export interface NodeInput { car_id: string; hostname: string; label: string }
export interface CarpoolNode extends NodeInput { id: string; status: string; created_at: string }
export interface NodeConnectionInput {
  origin: string; node_instance_id: string; admin_key: string; expected_generation: number; disabled?: boolean
}
export type NodeConnectionStatus = 'unverified' | 'ready' | 'error' | 'disabled'
export interface NodeConnection {
  main_instance_id: string; node_id: string; car_id: string; hostname: string; origin: string
  node_instance_id: string; generation: number; status: NodeConnectionStatus
  credential_configured: boolean; credential_mask: string; last_error_code: string
  check_operation: string; verified_at: string | null; updated_at: string
}
export type NodeSyncDelivery = 'pending' | 'leased' | 'synced' | 'error'
export interface NativeAccount { id: number; name: string; platform: string }
export interface NativeQuota {
  weekly_limit_usd: number; utilization: number | null; estimated_total: number | null
  observed_at: string | null; resets_at: string | null; last_checked_at: string | null
  quota_updated_at: string | null; cycle_synced_at: string | null
  state: 'ready' | 'unavailable' | 'stale' | 'error' | 'synchronizing'; last_error_code: string; cache_pending: boolean
}
export interface NativeSubscription {
  group_name: string; platform: 'openai' | 'anthropic'; status: string; starts_at: string; expires_at: string
  weekly_window_start: string | null; weekly_used_usd: number; weekly_limit_usd: number; weekly_remaining_usd: number
  account: NativeQuota; api_base_url: string; server_time: string
}
export interface NativeCarStatus {
  account_id: number; group_id: number; capacity: number; prepared_at: string | null
  service_start: string | null; service_end: string | null; can_activate: boolean; can_prepare: boolean
}
export interface NodeSyncMember {
  membership_id: string; user_id: number; status: 'prepared' | 'active' | 'paused' | 'revoked'
  delivery: NodeSyncDelivery; version: number; ack_version: number; attempts: number
  last_error_code: string; available_at: string; synced_at: string | null; lease_until: string | null
}
export interface NodeConsole {
  native_subscription?: boolean
  car_id: string; member_status: Membership['status']; connection_status: NodeConnectionStatus | 'unconfigured'
  sync_status: NodeSyncDelivery; access_status: 'unactivated' | 'active' | 'paused' | 'revoked' | 'expired' | 'unavailable' | 'synchronizing'
  last_error_code: string; api_base_url: string; can_read_node: boolean; can_create_key: boolean
  service_start: string | null; service_end: string | null; synced_at: string | null; server_time: string
}
export interface NodeKey { id: number; name: string; status: string; expires_at: string | null; key?: string }
export interface NodeKeyUpdate { name?: string; status?: 'active' | 'disabled' }
export interface NodePage<T> { items: T[]; total: number; page: number; page_size: number; pages: number }
export interface NodeUsageQuery extends NodePageQuery { start_date: string; end_date: string }
export interface NodeUsage {
  id: number; api_key_id: number; model: string; input_tokens: number; output_tokens: number
  cache_creation_tokens: number; cache_read_tokens: number; total_cost: number; actual_cost: number; created_at: string
}
export interface NodeUsageStats {
  total_requests: number; total_tokens: number; total_input_tokens: number; total_output_tokens: number
  total_cache_creation_tokens: number; total_cache_read_tokens: number; total_cost: number; total_actual_cost: number
}
const carsPath = (admin: boolean) => `${admin ? '/admin' : ''}/carpool/cars`
const idPath = (id: string) => encodeURIComponent(id)

export const carpoolAPI = {
  async nodeSubscription(carID: string): Promise<NativeSubscription> {
    return (await apiClient.get<NativeSubscription>(`${carsPath(false)}/${idPath(carID)}/subscription`)).data
  },
  async nativeQuota(carID: string): Promise<NativeQuota> {
    return (await apiClient.get<NativeQuota>(`${carsPath(true)}/${idPath(carID)}/native/quota`)).data
  },
  async syncNativeQuota(carID: string): Promise<NativeQuota> {
    return (await apiClient.post<NativeQuota>(`${carsPath(true)}/${idPath(carID)}/native/quota/sync`, {})).data
  },
  async nativeCarStatus(carID: string): Promise<NativeCarStatus> {
    return (await apiClient.get<NativeCarStatus>(`${carsPath(true)}/${idPath(carID)}/native`)).data
  },
  async nativeAccounts(carID: string): Promise<NativeAccount[]> {
    return (await apiClient.get<NativeAccount[]>(`${carsPath(true)}/${idPath(carID)}/native/accounts`)).data
  },
  async prepareNativeCar(carID: string, accountID: number): Promise<NativeCarStatus> {
    return (await apiClient.post<NativeCarStatus>(`${carsPath(true)}/${idPath(carID)}/native/prepare`, { account_id: accountID })).data
  },
  async activateNativeCar(carID: string): Promise<NativeCarStatus> {
    return (await apiClient.post<NativeCarStatus>(`${carsPath(true)}/${idPath(carID)}/native/activate`, { confirm: true })).data
  },
  async controlInfo(): Promise<{ main_instance_id: string; protocol_version: number }> {
    return (await apiClient.get('/admin/carpool/control')).data
  },
  async registerNode(input: NodeInput): Promise<CarpoolNode> {
    return (await apiClient.post<CarpoolNode>('/admin/carpool/nodes', input)).data
  },
  async nodeConnection(carID: string): Promise<NodeConnection> {
    return (await apiClient.get<NodeConnection>(`${carsPath(true)}/${idPath(carID)}/connection`)).data
  },
  async saveNodeConnection(nodeID: string, input: NodeConnectionInput): Promise<NodeConnection> {
    return (await apiClient.put<NodeConnection>(`/admin/carpool/nodes/${idPath(nodeID)}/connection`, input)).data
  },
  async checkNodeConnection(nodeID: string): Promise<NodeConnection> {
    return (await apiClient.post<NodeConnection>(`/admin/carpool/nodes/${idPath(nodeID)}/connection/check`, {})).data
  },
  async nodeSync(carID: string, params: NodePageQuery = {}): Promise<Page<NodeSyncMember>> {
    return (await apiClient.get<Page<NodeSyncMember>>(`${carsPath(true)}/${idPath(carID)}/sync`, { params })).data
  },
  async retryNodeSync(carID: string): Promise<{ queued: boolean }> {
    return (await apiClient.post<{ queued: boolean }>(`${carsPath(true)}/${idPath(carID)}/sync/retry`, {})).data
  },
  async nodeConsole(carID: string): Promise<NodeConsole> {
    return (await apiClient.get<NodeConsole>(`${carsPath(false)}/${idPath(carID)}/console`)).data
  },
  async nodeKeys(carID: string, params: NodePageQuery = {}): Promise<NodePage<NodeKey>> {
    return (await apiClient.get<NodePage<NodeKey>>(`${carsPath(false)}/${idPath(carID)}/keys`, { params })).data
  },
  async createNodeKey(carID: string, name: string, operation: string): Promise<NodeKey> {
    return (await apiClient.post<NodeKey>(`${carsPath(false)}/${idPath(carID)}/keys`, { name }, { headers: { 'Idempotency-Key': operation } })).data
  },
  async updateNodeKey(carID: string, keyID: number, input: NodeKeyUpdate): Promise<NodeKey> {
    return (await apiClient.patch<NodeKey>(`${carsPath(false)}/${idPath(carID)}/keys/${keyID}`, input)).data
  },
  async deleteNodeKey(carID: string, keyID: number): Promise<{ id: number; deleted: boolean }> {
    return (await apiClient.delete<{ id: number; deleted: boolean }>(`${carsPath(false)}/${idPath(carID)}/keys/${keyID}`)).data
  },
  async nodeUsage(carID: string, params: NodeUsageQuery): Promise<NodePage<NodeUsage>> {
    return (await apiClient.get<NodePage<NodeUsage>>(`${carsPath(false)}/${idPath(carID)}/usage`, { params })).data
  },
  async nodeUsageStats(carID: string, params: NodeUsageQuery): Promise<NodeUsageStats> {
    return (await apiClient.get<NodeUsageStats>(`${carsPath(false)}/${idPath(carID)}/usage/stats`, { params })).data
  },
  async enrollManual(id: string, input: EnrollmentInput, key: string): Promise<ManualOrder> {
    return (await apiClient.post<ManualOrder>(`${carsPath(true)}/${idPath(id)}/members`, input, { headers: { 'Idempotency-Key': key } })).data
  },
  async manualOrders(params: ManualOrderQuery = {}): Promise<Page<ManualOrder>> {
    return (await apiClient.get<Page<ManualOrder>>('/admin/carpool/orders', { params })).data
  },
  async manualOrder(id: string): Promise<ManualOrder> {
    return (await apiClient.get<ManualOrder>(`/admin/carpool/orders/${idPath(id)}`)).data
  },
  async withdrawManual(id: string, input: WithdrawalInput, key: string): Promise<ManualOrder> {
    return (await apiClient.post<ManualOrder>(`/admin/carpool/orders/${idPath(id)}/withdraw`, input, { headers: { 'Idempotency-Key': key } })).data
  },
  async refundManual(id: string, input: RefundInput, key: string): Promise<ManualOrder> {
    return (await apiClient.post<ManualOrder>(`/admin/carpool/orders/${idPath(id)}/refund`, input, { headers: { 'Idempotency-Key': key } })).data
  },
  async listCars(params: PageQuery = {}, admin = false): Promise<Page<Car>> {
    return (await apiClient.get<Page<Car>>(carsPath(admin), { params })).data
  },
  async getCar(id: string, admin = false): Promise<Car> {
    return (await apiClient.get<Car>(`${carsPath(admin)}/${idPath(id)}`)).data
  },
  async reserve(id: string, key: string): Promise<Order> {
    return (await apiClient.post<Order>(`${carsPath(false)}/${idPath(id)}/reservations`, {}, { headers: { 'Idempotency-Key': key } })).data
  },
  async mine(params: PageQuery = {}): Promise<Page<Order>> {
    return (await apiClient.get<Page<Order>>('/carpool/mine', { params })).data
  },
  async getOrder(id: string): Promise<Order> {
    return (await apiClient.get<Order>(`/carpool/orders/${idPath(id)}`)).data
  },
  async cancelOrder(id: string): Promise<Order> {
    return (await apiClient.post<Order>(`/carpool/orders/${idPath(id)}/cancel`, {})).data
  },
  async createCar(input: CarInput): Promise<Car> {
    return (await apiClient.post<Car>(carsPath(true), input)).data
  },
  async updateCar(id: string, input: CarInput): Promise<Car> {
    return (await apiClient.patch<Car>(`${carsPath(true)}/${idPath(id)}`, input)).data
  },
  async publishCar(id: string): Promise<Car> {
    return (await apiClient.post<Car>(`${carsPath(true)}/${idPath(id)}/publish`, {})).data
  },
  async cancelCar(id: string, reason: string, confirmMembers: boolean): Promise<Car> {
    return (await apiClient.post<Car>(`${carsPath(true)}/${idPath(id)}/cancel`, { reason, confirm_members: confirmMembers })).data
  },
  async members(id: string, params: PageQuery = {}): Promise<Page<Membership>> {
    return (await apiClient.get<Page<Membership>>(`${carsPath(true)}/${idPath(id)}/members`, { params })).data
  }
}
