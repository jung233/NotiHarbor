import React, { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Card,
  Header,
  Label,
  Message,
  Segment,
  Table,
} from 'semantic-ui-react';
import GatewayNav from '../../components/GatewayNav';
import { API, showError } from '../../helpers';

const metricValue = (data, names) => {
  for (const name of names) {
    const value = data?.today?.[name] ?? data?.stats?.[name] ?? data?.[name] ?? data?.metrics?.[name];
    if (value !== undefined && value !== null) return value;
  }
  return 0;
};

const displayTime = (value) => {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
};

const Dashboard = () => {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);

  const load = async () => {
    setLoading(true);
    try {
      const response = await API.get('/api/v1/admin/dashboard');
      if (response.data?.success) {
        setData(response.data.data || {});
      } else {
        showError(response.data?.message || '读取网关统计失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  const channels = data?.by_channel || data?.stats?.by_channel || data?.channel_stats || data?.channels || [];
  const errors = data?.recent_errors || data?.stats?.recent_errors || data?.errors || [];
  const trend = data?.trend || data?.last_24_hours || data?.stats?.last_24_hours || data?.hourly_trend || [];
  const cards = [
    ['今日收到', metricValue(data, ['received', 'today_received', 'received_today', 'notifications'])],
    ['今日受理', metricValue(data, ['accepted', 'today_accepted', 'accepted_today'])],
    ['今日失败', metricValue(data, ['failed', 'today_failed', 'failed_today'])],
    ['正在重试', metricValue(data, ['retrying', 'retrying_count'])],
    ['队列等待', metricValue(data, ['queue_backlog', 'queue_waiting', 'queue_waiting_count', 'queued'])],
  ];
  const maxTrend = Math.max(
    1,
    ...trend.map((item) => Number(item.received ?? item.count ?? 0))
  );

  return (
    <Segment loading={loading}>
      <Header as='h2'>消息网关</Header>
      <GatewayNav />
      <Card.Group itemsPerRow={5} stackable>
        {cards.map(([label, value]) => (
          <Card key={label} color={label === '今日失败' && value > 0 ? 'red' : undefined}>
            <Card.Content>
              <Card.Meta>{label}</Card.Meta>
              <Card.Header>{value}</Card.Header>
            </Card.Content>
          </Card>
        ))}
      </Card.Group>

      <Header as='h3' dividing>
        最近 24 小时消息趋势
      </Header>
      {trend.length ? (
        <div className='gateway-trend' aria-label='最近 24 小时消息趋势'>
          {trend.map((item, index) => {
            const received = Number(item.received ?? item.count ?? 0);
            const accepted = Number(item.accepted ?? 0);
            const failed = Number(item.failed ?? 0);
            return (
              <div className='gateway-trend-item' key={item.hour || item.time || index}>
                <div className='gateway-trend-bars'>
                  <span style={{ height: `${Math.max(3, (received / maxTrend) * 100)}%` }} />
                  <span className='accepted' style={{ height: `${Math.max(3, (accepted / maxTrend) * 100)}%` }} />
                  <span className='failed' style={{ height: `${Math.max(3, (failed / maxTrend) * 100)}%` }} />
                </div>
                <small>{String(item.hour || item.time || '').slice(-5)}</small>
              </div>
            );
          })}
        </div>
      ) : (
        <Message info>后端尚未返回趋势数据。</Message>
      )}

      <Header as='h3' dividing>
        通道发送情况
      </Header>
      <Table stackable>
        <Table.Header>
          <Table.Row>
            <Table.HeaderCell>通道</Table.HeaderCell>
            <Table.HeaderCell>成功率</Table.HeaderCell>
            <Table.HeaderCell>平均耗时</Table.HeaderCell>
            <Table.HeaderCell>成功 / 失败</Table.HeaderCell>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {channels.map((channel) => (
            <Table.Row key={channel.channel_id || channel.id || channel.name}>
              <Table.Cell>{channel.channel_name || channel.name || channel.channel_id}</Table.Cell>
              <Table.Cell>
                {channel.success_rate === undefined
                  ? '—'
                  : `${Number(channel.success_rate) <= 1 ? Number(channel.success_rate) * 100 : channel.success_rate}%`}
              </Table.Cell>
              <Table.Cell>{channel.average_duration_ms ?? channel.avg_duration_ms ?? '—'} ms</Table.Cell>
              <Table.Cell>{channel.accepted ?? channel.succeeded ?? 0} / {channel.failed ?? 0}</Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
        {!channels.length && <Table.Footer><Table.Row><Table.HeaderCell colSpan='4'>暂无通道统计</Table.HeaderCell></Table.Row></Table.Footer>}
      </Table>

      <Header as='h3' dividing>
        最近错误
      </Header>
      <Table stackable>
        <Table.Header>
          <Table.Row>
            <Table.HeaderCell>时间</Table.HeaderCell>
            <Table.HeaderCell>消息</Table.HeaderCell>
            <Table.HeaderCell>错误</Table.HeaderCell>
            <Table.HeaderCell>通道</Table.HeaderCell>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {errors.map((error, index) => (
            <Table.Row key={error.id || error.notification_id || index}>
              <Table.Cell>{displayTime(error.created_at || error.updated_at || error.timestamp)}</Table.Cell>
              <Table.Cell>
                {error.notification_id ? (
                  <Link to={`/gateway/history/${error.notification_id}`}>
                    {error.title || error.notification_id}
                  </Link>
                ) : error.delivery_id ? (
                  <Link to={`/gateway/logs?delivery_id=${encodeURIComponent(error.delivery_id)}`}>
                    {error.title || error.delivery_id}
                  </Link>
                ) : error.title || '—'}
              </Table.Cell>
              <Table.Cell><Label color='red'>{error.error_code || error.error_category || error.error_message || error.error || '发送失败'}</Label></Table.Cell>
              <Table.Cell>{error.channel_name || error.channel_id || '—'}</Table.Cell>
            </Table.Row>
          ))}
        </Table.Body>
        {!errors.length && <Table.Footer><Table.Row><Table.HeaderCell colSpan='4'>最近没有错误记录</Table.HeaderCell></Table.Row></Table.Footer>}
      </Table>
    </Segment>
  );
};

export default Dashboard;
