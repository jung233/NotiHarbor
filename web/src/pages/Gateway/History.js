import React, { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Button,
  Form,
  Header,
  Label,
  Message,
  Pagination,
  Segment,
  Table,
} from 'semantic-ui-react';
import GatewayNav from '../../components/GatewayNav';
import { API, showError, showSuccess } from '../../helpers';

const PAGE_SIZE = 25;
const STATUS_OPTIONS = [
  { key: 'pending', value: 'pending', text: '待处理' },
  { key: 'pending_dispatch', value: 'pending_dispatch', text: '等待调度' },
  { key: 'queued', value: 'queued', text: '队列中' },
  { key: 'processing', value: 'processing', text: '发送中' },
  { key: 'retrying', value: 'retrying', text: '重试中' },
  { key: 'accepted', value: 'accepted', text: '已受理' },
  { key: 'failed', value: 'failed', text: '失败' },
  { key: 'cancelled', value: 'cancelled', text: '已取消' },
  { key: 'delivery_unknown', value: 'delivery_unknown', text: '结果未知' },
];

const dateValue = (value) => {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
};

const statusColor = (status) => {
  if (['accepted', 'succeeded', 'success'].includes(status)) return 'green';
  if (['failed', 'dead_letter'].includes(status)) return 'red';
  if (['retrying', 'processing'].includes(status)) return 'orange';
  if (status === 'delivery_unknown') return 'yellow';
  return 'grey';
};

const History = () => {
  const [filters, setFilters] = useState({
    from: '',
    to: '',
    source_id: '',
    channel_id: '',
    status: '',
    error: '',
  });
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState([]);

  const load = async (nextPage = page, appliedFilters = filters) => {
    setLoading(true);
    try {
      const normalizedFilters = { ...appliedFilters };
      ['from', 'to'].forEach((field) => {
        if (normalizedFilters[field]) {
          const date = new Date(normalizedFilters[field]);
          if (!Number.isNaN(date.getTime())) normalizedFilters[field] = date.toISOString();
        }
      });
      const params = Object.fromEntries(
        Object.entries({ ...normalizedFilters, page: nextPage, page_size: PAGE_SIZE }).filter(
          ([key, value]) => key === 'page' || key === 'page_size' || value !== ''
        )
      );
      const response = await API.get('/api/v1/admin/notifications', { params });
      if (response.data?.success) {
        const result = response.data.data || {};
        setItems(result.items || []);
        setTotal(Number(result.total || 0));
        setPage(Number(result.page || nextPage));
        setSelected([]);
      } else {
        showError(response.data?.message || '读取消息历史失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load(1);
    // Initial query only; form submissions use the current filter state.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const updateFilter = (name, value) => {
    setFilters((current) => ({ ...current, [name]: value }));
  };

  const runAction = async (path, successMessage, body, confirmMessage) => {
    if (confirmMessage && !window.confirm(confirmMessage)) return;
    setLoading(true);
    try {
      const response = await API.post(path, body || {});
      if (response.data?.success) {
        showSuccess(successMessage);
        await load(page);
      } else {
        showError(response.data?.message || '操作失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const selectAllOnPage = () => {
    setSelected((current) =>
      current.length === items.length ? [] : items.map((item) => item.id)
    );
  };

  return (
    <Segment loading={loading}>
      <Header as='h2'>消息历史</Header>
      <GatewayNav />
      <Form
        onSubmit={(event) => {
          event.preventDefault();
          load(1);
        }}
      >
        <Form.Group widths='equal'>
          <Form.Input
            type='datetime-local'
            label='开始时间'
            value={filters.from}
            onChange={(event, { value }) => updateFilter('from', value)}
          />
          <Form.Input
            type='datetime-local'
            label='结束时间'
            value={filters.to}
            onChange={(event, { value }) => updateFilter('to', value)}
          />
          <Form.Input
            label='来源 ID'
            value={filters.source_id}
            onChange={(event, { value }) => updateFilter('source_id', value)}
          />
          <Form.Input
            label='通道 ID'
            value={filters.channel_id}
            onChange={(event, { value }) => updateFilter('channel_id', value)}
          />
        </Form.Group>
        <Form.Group widths='equal'>
          <Form.Select
            clearable
            label='状态'
            options={STATUS_OPTIONS}
            value={filters.status}
            onChange={(event, { value }) => updateFilter('status', value || '')}
          />
          <Form.Input
            label='失败原因'
            placeholder='错误码或关键字'
            value={filters.error}
            onChange={(event, { value }) => updateFilter('error', value)}
          />
          <Form.Button primary content='筛选' />
          <Form.Button
            type='button'
            content='清除'
            onClick={() => {
              const clearedFilters = { from: '', to: '', source_id: '', channel_id: '', status: '', error: '' };
              setFilters(clearedFilters);
              load(1, clearedFilters);
            }}
          />
        </Form.Group>
      </Form>
      <div className='gateway-actions'>
        <Button
          primary
          disabled={!selected.length}
          onClick={() =>
            runAction(
              '/api/v1/admin/notifications/batch/retry',
              '已提交批量重试',
              { ids: selected },
              items.some((item) => selected.includes(item.id) && item.status === 'delivery_unknown')
                ? '选中项包含结果未知的消息。重试可能造成重复通知，仍要继续吗？'
                : null
            )
          }
        >
          批量重试 ({selected.length})
        </Button>
        <Button onClick={() => load(page)}>刷新</Button>
      </div>
      {!items.length && <Message info>没有符合筛选条件的消息。</Message>}
      <Table selectable stackable>
        <Table.Header>
          <Table.Row>
            <Table.HeaderCell collapsing>
              <input
                type='checkbox'
                aria-label='全选当前页'
                checked={items.length > 0 && selected.length === items.length}
                onChange={selectAllOnPage}
              />
            </Table.HeaderCell>
            <Table.HeaderCell>ID</Table.HeaderCell>
            <Table.HeaderCell>标题</Table.HeaderCell>
            <Table.HeaderCell>来源</Table.HeaderCell>
            <Table.HeaderCell>状态</Table.HeaderCell>
            <Table.HeaderCell>创建时间</Table.HeaderCell>
            <Table.HeaderCell>操作</Table.HeaderCell>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {items.map((item) => (
            <Table.Row key={item.id}>
              <Table.Cell>
                <input
                  type='checkbox'
                  aria-label={`选择消息 ${item.id}`}
                  checked={selected.includes(item.id)}
                  onChange={() =>
                    setSelected((current) =>
                      current.includes(item.id)
                        ? current.filter((id) => id !== item.id)
                        : [...current, item.id]
                    )
                  }
                />
              </Table.Cell>
              <Table.Cell>{item.id}</Table.Cell>
              <Table.Cell>{item.title || '无标题'}</Table.Cell>
              <Table.Cell>{item.source_name || item.source_id || '—'}</Table.Cell>
              <Table.Cell><Label color={statusColor(item.status)}>{item.status || 'unknown'}</Label></Table.Cell>
              <Table.Cell>{dateValue(item.created_at || item.timestamp)}</Table.Cell>
              <Table.Cell>
                <Button size='small' as={Link} to={`/gateway/history/${item.id}`}>详情</Button>
                <Button
                  size='small'
                  primary
                  onClick={() => runAction(
                    `/api/v1/admin/notifications/${item.id}/retry`,
                    '已提交原任务重试',
                    undefined,
                    item.status === 'delivery_unknown'
                      ? '发送结果未知，重试可能造成重复通知。仍要继续吗？'
                      : null
                  )}
                >
                  Retry
                </Button>
                <Button
                  size='small'
                  onClick={() => runAction(`/api/v1/admin/notifications/${item.id}/cancel`, '已取消待发送任务')}
                >
                  取消
                </Button>
                <Button
                  size='small'
                  color='yellow'
                  onClick={() => runAction(
                    `/api/v1/admin/notifications/${item.id}/resend`,
                    '已创建新的发送任务',
                    undefined,
                    'Resend 会创建新的逻辑发送记录，原消息可能已被接收。仍要继续吗？'
                  )}
                >
                  Resend
                </Button>
              </Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
      </Table>
      {total > PAGE_SIZE && (
        <Pagination
          activePage={page}
          totalPages={Math.ceil(total / PAGE_SIZE)}
          onPageChange={(event, { activePage }) => load(Number(activePage))}
        />
      )}
      <Message info size='small'>Retry 会重试原投递任务；Resend 会创建一条新的逻辑发送记录。</Message>
    </Segment>
  );
};

export default History;
