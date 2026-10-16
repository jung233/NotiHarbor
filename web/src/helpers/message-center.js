import { useEffect, useRef } from 'react';

export const isArchivedMessage = (item) => String(item?.id || '').startsWith('legacy-archive:');

export const messageStatusText = (status) => ({
  pending: '待处理', pending_dispatch: '等待调度', queued: '队列中',
  processing: '发送中', retrying: '重试中', accepted: '已受理',
  succeeded: '已发送', success: '已发送', failed: '失败',
  dead_letter: '重试耗尽', cancelled: '已取消', delivery_unknown: '结果未知',
}[status] || status || '未知');

// Gateway API submissions do not emit the legacy SSE stream. Refresh the unified
// query while visible so all submission paths receive the same updates.
export const useMessageRefresh = (refresh) => {
  const latest = useRef(refresh);
  latest.current = refresh;
  useEffect(() => {
    const timer = setInterval(() => {
      if (!document.hidden) latest.current();
    }, 10000);
    return () => clearInterval(timer);
  }, []);
};
