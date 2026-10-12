export const manualEn = {
  mode: 'Manual enrollment', confirmed: 'Manually confirmed', vehicles: 'Vehicles', orders: 'Manual orders', enroll: 'Add member',
  contact: 'Contact administrator', contactMissing: 'Administrator contact is not configured.', contactSetup: 'Set the site contact details before opening recruitment.',
  notice: 'Enrollment is confirmed by an administrator after an off-platform transfer.', policy: 'Full refund before service starts, processed off-platform and recorded by an administrator.',
  search: 'Registered user email or ID', searchAction: 'Search users', noUsers: 'No matching active users', identity: 'Selected account',
  channel: 'Receipt channel', alipay: 'Alipay', wechat: 'WeChat', amount: 'Amount received', received: 'Received at', recorded: 'Recorded at',
  reference: 'Transaction reference (optional)', note: 'Internal note (optional)', receivedConfirm: 'I have independently verified receipt of the full amount.',
  review: 'Review enrollment', confirmEnroll: 'Confirm member', back: 'Back', validation: 'Select an active account and check the time, reference and confirmation.',
  invalidTime: 'Enter a valid, nonfuture time, not earlier than receipt for a refund.', invalidReference: 'Reference must be at most 128 non-space ASCII characters.',
  saved: 'Manual record saved', pending: 'Refund pending', refunded: 'Refund recorded', none: 'No refund requested', refundFilter: 'Refund status',
  withdraw: 'Remove member', refund: 'Record refund', reason: 'Withdrawal reason', withdrawConfirm: 'I confirm removal and the creation of a full pending refund.',
  refundConfirm: 'I have already refunded the full amount off-platform.', refundedAt: 'Refunded at', refundReference: 'Refund reference (optional)',
  offlineOnly: 'This action records an off-platform refund. It does not transfer money.', receiptBy: 'Receipt recorded by', refundBy: 'Refund recorded by',
  retryUnknown: 'The result is unknown. The original account, amount and request are locked for a safe retry.', retry: 'Retry original request',
  recover: 'Resume unresolved request', reconcile: 'Check existing records', reconciled: 'I checked the records and off-platform transfer and confirm this request no longer needs retrying.',
  finishReview: 'Finish reconciliation', key: 'Request ID', disabled: 'New manual enrollment is disabled.', reviewCar: 'Vehicle needs administrator review',
  userId: 'User ID', carId: 'Vehicle ID', status: 'Order status', detail: 'Order and receipt', requestConflict: 'The vehicle or order changed. Review the current records before proceeding.'
}

export const manualZh: typeof manualEn = {
  mode: '人工入车', confirmed: '人工已确认', vehicles: '车辆', orders: '人工订单', enroll: '添加成员',
  contact: '联系管理员', contactMissing: '暂未配置管理员联系方式。', contactSetup: '请先在站点设置中补充联系方式。',
  notice: '站外转账由管理员核实到账后确认入车。', policy: '未开通服务前退出、招募失败或车辆取消，全额退款，由管理员在站外办理并登记。',
  search: '已注册用户邮箱或编号', searchAction: '搜索用户', noUsers: '未找到匹配的有效用户', identity: '已选账号',
  channel: '收款渠道', alipay: '支付宝', wechat: '微信', amount: '实收金额', received: '收款时间', recorded: '登记时间',
  reference: '交易参考号（选填）', note: '内部备注（选填）', receivedConfirm: '我已自行核实全额到账。',
  review: '核对入车信息', confirmEnroll: '确认添加成员', back: '返回修改', validation: '请选择有效账号，并检查时间、参考号及到账确认。',
  invalidTime: '请输入有效且不晚于当前的时间；退款时间不得早于收款时间。', invalidReference: '参考号最多 128 个非空格 ASCII 字符。',
  saved: '人工登记已保存', pending: '待人工退款', refunded: '已人工退款', none: '无需退款', refundFilter: '退款状态',
  withdraw: '移除成员', refund: '登记退款', reason: '退出原因', withdrawConfirm: '我确认移除成员，并生成全额待退款记录。',
  refundConfirm: '我已在站外完成全额退款。', refundedAt: '退款时间', refundReference: '退款参考号（选填）',
  offlineOnly: '此操作仅登记站外退款结果，不会转账。', receiptBy: '收款登记人', refundBy: '退款登记人',
  retryUnknown: '请求结果尚未确定。原账号、金额和请求已锁定，重试不会新建另一笔登记。', retry: '重试原请求',
  recover: '继续核对未确定请求', reconcile: '核对已有记录', reconciled: '我已核对平台记录及站外流水，确认此请求无需继续重试。',
  finishReview: '结束核对', key: '请求编号', disabled: '暂不开放新增人工入车。', reviewCar: '车辆待管理员处理',
  userId: '用户编号', carId: '车辆编号', status: '订单状态', detail: '订单与收款详情', requestConflict: '车辆或订单状态已变化，请核对当前记录后再操作。'
}
