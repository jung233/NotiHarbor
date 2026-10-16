import React, { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Button, Form, Header, Label, Message, Pagination, Segment, Table } from 'semantic-ui-react';
import MessageNav from '../../components/MessageNav';
import { API, showError } from '../../helpers';

const PAGE_SIZE = 50;

const timeValue = (value) => {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
};

const attemptKindLabel = (kind) => ({
  send: '发送',
  preflight: '发送前校验',
}[kind] || kind || '—');

const Logs = () => {
  const [searchParams] = useSearchParams();
  const [filters, setFilters] = useState({
    notification_id: searchParams.get('notification_id') || '',
    delivery_id: searchParams.get('delivery_id') || '',
    source_id: searchParams.get('source_id') || '',
    channel_id: searchParams.get('channel_id') || '',
  });
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);

  const load = async (nextPage = 1, appliedFilters = filters) => {
    setLoading(true);
    try {
      const params = Object.fromEntries(
        Object.entries({ ...appliedFilters, page: nextPage, page_size: PAGE_SIZE }).filter(
          ([key, value]) => key === 'page' || key === 'page_size' || value !== ''
        )
      );
      const response = await API.get('/api/v1/admin/logs', { params });
      if (response.data?.success) {
        const data = response.data.data || {};
        setItems(data.items || []);
        setTotal(Number(data.total || 0));
        setPage(Number(data.page || nextPage));
      } else {
        showError(response.data?.message || '读取发送日志失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load(1);
    // Initial load uses filters from the URL.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <Segment loading={loading}>
      <Header as='h2'>投递日志</Header>
      <MessageNav />
      <Message info size='small'>
        每行代表一次投递尝试。点击消息 ID 可返回消息详情，查看内容及完整发送过程。
      </Message>
      <Form onSubmit={(event) => { event.preventDefault(); load(1); }}>
        <Form.Group widths='equal'>
          {Object.keys(filters).map((field) => (
            <Form.Input
              key={field}
              label={{ notification_id: '消息 ID', delivery_id: '投递 ID', source_id: '来源 ID', channel_id: '通道 ID' }[field]}
              value={filters[field]}
              onChange={(event, { value }) => setFilters({ ...filters, [field]: value })}
            />
          ))}
          <Form.Button primary content='筛选' />
        </Form.Group>
      </Form>
      {!items.length && <Message info>没有符合条件的发送日志。</Message>}
      <Table stackable>
        <Table.Header>
          <Table.Row>
            <Table.HeaderCell>时间</Table.HeaderCell>
            <Table.HeaderCell>级别</Table.HeaderCell>
            <Table.HeaderCell>消息</Table.HeaderCell>
            <Table.HeaderCell>消息 / 投递 / 尝试</Table.HeaderCell>
            <Table.HeaderCell>来源 / 通道</Table.HeaderCell>
            <Table.HeaderCell>错误</Table.HeaderCell>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {items.map((item) => (
            <Table.Row key={item.id}>
              <Table.Cell>{timeValue(item.created_at || item.timestamp)}</Table.Cell>
              <Table.Cell><Label color={item.level === 'error' || item.level === 'fatal' ? 'red' : 'grey'}>{item.level || item.type || 'info'}</Label></Table.Cell>
              <Table.Cell>{item.message || item.event || '—'}</Table.Cell>
              <Table.Cell>
                {item.notification_id ? <Link to={`/message/detail/${item.notification_id}`}>{item.notification_id}</Link> : '—'}
                <br />{item.delivery_id || '—'}
                <br />尝试 {item.attempt_number || '—'} · {attemptKindLabel(item.attempt_kind)}
                <br />{item.attempt_id || '—'}
              </Table.Cell>
              <Table.Cell>{item.source_id || '—'} / {item.channel_name || item.channel_id || '—'}</Table.Cell>
              <Table.Cell>{item.error_code || item.error_category || item.error_message || '—'}</Table.Cell>
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
      <Button onClick={() => load(page)}>刷新</Button>
    </Segment>
  );
};

export default Logs;
