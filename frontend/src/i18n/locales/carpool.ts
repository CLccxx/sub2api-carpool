import { manualEn, manualZh } from './carpoolManual'
import { controlEn, controlZh } from './carpoolControl'

export const carpoolEn = {
  control: controlEn,
  manual: manualEn,
  lobby: 'Carpool lobby', mine: 'My cars', admin: 'Carpool management',
  testOrders: 'Test orders', readOnly: 'Reservations unavailable',
  lobbyUI: {
    notice: 'Test orders only. No payment will be taken.',
    seatsLeft: 'Seats left', perSeat: 'Per seat', joined: 'Joined',
    closed: 'Recruitment closed',
    seatSummary: '{confirmed} confirmed, {held} reserved, {available} available of {capacity} seats'
  },
  name: 'Car name', capacity: 'Seats', price: 'Price per seat (CNY)', totalPrice: 'Total car price (CNY)', serviceDays: 'Service period',
  deadline: 'Recruitment deadline', deliveryHours: 'Delivery window', refundPolicy: 'Cancellation rules',
  days: '{n} days', hoursAfterFull: 'Within {n} hours after full confirmation',
  created: 'Created', published: 'Published', deliveryDue: 'Delivery due',
  confirmed: 'Confirmed', held: 'Reserved', available: 'Available',
  all: 'All statuses', details: 'Details', reserve: 'Reserve a test seat',
  reserveConfirm: 'Reserve this seat?', reserveNotice: 'Test order only. No payment will be taken. The hold lasts up to 10 minutes, or until recruitment ends.',
  myOrder: 'View my order', noCars: 'No cars available', noOrders: 'No orders yet',
  noMembers: 'No members yet', loading: 'Loading', refresh: 'Refresh', retry: 'Retry',
  close: 'Close', cancel: 'Cancel', save: 'Save', processing: 'Processing',
  remaining: 'Hold remaining', seat: 'Seat {n}', orderId: 'Order ID',
  waiting: 'Waiting for deployment', serviceNotStarted: 'Service not started',
  cancelOrder: 'Cancel reservation', cancelOrderNotice: 'Release this reserved seat? This test order has no payment to refund.',
  reservationSuccess: 'Test seat reserved', cancelledSuccess: 'Reservation cancelled',
  adminUI: {
    createUncertain: 'Creation result is unknown. Review the vehicle list before creating another draft.',
    reviewCreate: 'Review vehicle list',
    create: 'Create draft', edit: 'Edit draft', publish: 'Publish car', cancelCar: 'Cancel car',
    members: 'Members', actions: 'Actions', status: 'Status', times: 'Timeline', userId: 'User ID', memberId: 'Membership ID',
    seatNumber: 'Seat', service: 'Service', serviceDays: 'Service days', deliveryHours: 'Delivery hours',
    publishNotice: 'Publishing freezes these terms and opens recruitment. Confirm the price, deadline and cancellation rules before publishing.',
    cancelNotice: 'Cancellation closes recruitment and cancels reservations and memberships. This cannot be undone. No real payment or refund is performed.',
    cancelReason: 'Cancellation reason', confirmMembers: 'I acknowledge that {n} confirmed members will be cancelled.',
    conflict: 'This car changed. Review the refreshed status and terms before trying again.',
    notDraft: 'This car is no longer a draft. Its terms cannot be edited or published again.',
    notCancellable: 'This car is already closed and cannot be cancelled.',
    requestFailed: 'The request failed and its result may be unknown. Refresh the car list to check before retrying.',
    saved: 'Draft saved', published: 'Car published', cancelled: 'Car cancelled',
    validation: {
      name: 'Enter a name of at most 160 UTF-8 bytes.', capacity: 'Choose 2, 3 or 4 seats.',
      price: 'Enter a positive CNY amount with at most two decimal places, up to 10,000,000,000.00.',
      serviceDays: 'Enter a whole number from 1 to 3650 days.',
      deadline: 'Choose a valid local date and time in the future, less than 10 years away.',
      deliveryHours: 'Enter a whole number from 1 to 8760 hours.',
      refundPolicy: 'Enter cancellation rules of at most 4000 UTF-8 bytes.',
      cancelReason: 'Enter a cancellation reason of at most 2000 UTF-8 bytes.',
      confirmMembers: 'Acknowledge cancellation of confirmed members before continuing.'
    }
  },
  errors: {
    invalid: 'Check the required fields and allowed values.',
    unauthorized: 'Please sign in again.', forbidden: 'You do not have permission for this action.',
    unavailable: 'This resource or feature is unavailable.',
    conflict: 'The seat or order changed. Review the refreshed status before trying again.',
    compliance: 'Administrator acknowledgement is required before this action.',
    rateLimited: 'Too many requests. Please wait before retrying.',
    network: 'Request failed. The result may be unknown. Retry to check the same request.'
  },
  status: { active: 'Active', paused: 'Paused', completed: 'Completed', revoked: 'Revoked', draft: 'Draft', recruiting: 'Recruiting', awaiting_deployment: 'Full · awaiting deployment', closed_unfilled: 'Recruitment ended', cancelled: 'Cancelled', pending: 'Reserved', confirmed: 'Test confirmed', expired: 'Expired', waiting_deployment: 'Awaiting deployment' }
}

export const carpoolZh: typeof carpoolEn = {
  control: controlZh,
  manual: manualZh,
  lobby: '拼车大厅', mine: '我的车', admin: '车辆管理',
  testOrders: '测试订单', readOnly: '暂不开放预约',
  lobbyUI: {
    notice: '当前仅支持测试订单，不会产生真实扣款。',
    seatsLeft: '剩余席位', perSeat: '每席价格', joined: '已加入',
    closed: '招募已关闭',
    seatSummary: '共 {capacity} 席，已确认 {confirmed} 席，占位中 {held} 席，空余 {available} 席'
  },
  name: '车辆名称', capacity: '座位数', price: '单座价格（元）', totalPrice: '整车总价（元）', serviceDays: '服务周期',
  deadline: '招募截止时间', deliveryHours: '交付时限', refundPolicy: '取消规则',
  days: '{n} 天', hoursAfterFull: '满员确认后 {n} 小时内',
  created: '发起时间', published: '发布时间', deliveryDue: '预计交付截止',
  confirmed: '已确认', held: '占位中', available: '可用座位',
  all: '全部状态', details: '详情', reserve: '预约测试座位',
  reserveConfirm: '确认预约座位？', reserveNotice: '仅创建测试订单，不会扣款。占位最长保留 10 分钟，招募截止时提前结束。',
  myOrder: '查看我的订单', noCars: '暂无车辆', noOrders: '暂无订单',
  noMembers: '暂无成员', loading: '加载中', refresh: '刷新', retry: '重试',
  close: '关闭', cancel: '取消', save: '保存', processing: '处理中',
  remaining: '占位剩余时间', seat: '座位 {n}', orderId: '订单编号',
  waiting: '等待部署', serviceNotStarted: '服务尚未开始',
  cancelOrder: '取消占位', cancelOrderNotice: '确认释放此座位？此测试订单未扣款，无真实退款。',
  reservationSuccess: '测试座位已预约', cancelledSuccess: '占位已取消',
  adminUI: {
    create: '创建草稿', edit: '编辑草稿', publish: '发布车辆', cancelCar: '取消车辆',
    members: '成员', actions: '操作', status: '状态', times: '时间', userId: '用户编号', memberId: '成员记录编号',
    seatNumber: '座位', service: '服务', serviceDays: '服务天数', deliveryHours: '交付小时数',
    publishNotice: '发布后条款将冻结并开放招募。请核对价格、截止时间及取消规则后确认发布。',
    cancelNotice: '取消将结束招募，并取消预约及成员资格。此操作不可撤销，不会执行真实扣款或退款。',
    cancelReason: '取消原因', confirmMembers: '我确认取消 {n} 位已确认成员的资格。',
    conflict: '车辆信息已变化，请核对刷新后的状态与条款再操作。',
    notDraft: '此车辆已不是草稿，无法修改条款或再次发布。',
    notCancellable: '此车辆已关闭，无法取消。',
    createUncertain: '创建结果尚未确定，请先核对车辆列表，再决定是否新建草稿。',
    reviewCreate: '核对车辆列表',
    requestFailed: '请求失败，结果可能尚未确定。请刷新车辆列表核对后再重试。',
    saved: '草稿已保存', published: '车辆已发布', cancelled: '车辆已取消',
    validation: {
      name: '请输入名称，最多 160 个 UTF-8 字节。', capacity: '请选择 2、3 或 4 个座位。',
      price: '请输入大于零的人民币金额，最多两位小数且不超过 10,000,000,000.00 元。',
      serviceDays: '请输入 1 至 3650 的整数天数。',
      deadline: '请选择有效的本地日期时间，须晚于当前时间且小于未来 10 年。',
      deliveryHours: '请输入 1 至 8760 的整数小时数。',
      refundPolicy: '请输入取消规则，最多 4000 个 UTF-8 字节。',
      cancelReason: '请输入取消原因，最多 2000 个 UTF-8 字节。',
      confirmMembers: '请先明确确认取消已确认成员的资格。'
    }
  },
  errors: {
    invalid: '请检查必填项及字段范围。', unauthorized: '请重新登录。', forbidden: '没有执行此操作的权限。',
    unavailable: '此资源或功能不可用。', conflict: '座位或订单状态已变化，请核对刷新后的状态再操作。',
    compliance: '此操作需要管理员先完成合规确认。', rateLimited: '请求过于频繁，请稍后重试。',
    network: '请求失败，结果可能尚未确定。重试将核对同一请求。'
  },
  status: { active: '服务中', paused: '已暂停', completed: '已结束', revoked: '已撤销', draft: '草稿', recruiting: '招募中', awaiting_deployment: '满员待部署', closed_unfilled: '招募已结束', cancelled: '已取消', pending: '占位中', confirmed: '测试已确认', expired: '已过期', waiting_deployment: '待部署' }
}
