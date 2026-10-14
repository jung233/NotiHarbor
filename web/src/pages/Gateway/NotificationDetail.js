import React, { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  Button,
  Header,
  Label,
  Message,
  Segment,
  Table,
} from 'semantic-ui-react';
import GatewayNav from '../../components/GatewayNav';
import { API, showError, showSuccess } from '../../helpers';

const timeValue = (value) => {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
};

const maskTarget = (value) => {
  const text = String(value || '');
  if (text.length < 8) return text ? '••••' : '默认目标';
  return `${text.slice(0, 3)}••••${text.slice(-3)}`;
};

const readData = (response) => response?.data?.data || {};

const NotificationDetail = () => {
  const { id } = useParams();
  const [notification, setNotification] = useState(null);
  const [deliveries, setDeliveries] = useState([]);
  const [loading, setLoading] = useState(true);

  const load = async () => {
    setLoading(true);
    try {
      const notificationResponse = await API.get(
        `/api/v1/admin/notifications/${encodeURIComponent(id)}`
      );
      if (!notificationResponse.data?.success) {
        showError(notificationResponse.data?.message || '读取消息详情失败');
        return;
      }
      const detail = readData(notificationResponse);
      const deliveryList = detail.deliveries || [];
      const deliveryResults = await Promise.all(
        deliveryList.map(async (delivery) => {
          try {
            const attemptsResponse = await API.get(
              `/api/v1/admin/notifications/${encodeURIComponent(id)}/deliveries/${encodeURIComponent(delivery.id)}/attempts`
            );
            const attemptData = readData(attemptsResponse);
            return {
              ...delivery,
              attempts: attemptsResponse.data?.success
                ? Array.isArray(attemptData)
                  ? attemptData
                  : attemptData.items || []
                : [],
            };
          } catch (error) {
            return { ...delivery, attempts: [] };
          }
        })
      );
      setNotification(detail.notification || detail);
      setDeliveries(deliveryResults);
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // Reload when the selected notification changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  const runAction = async (action, message) => {
    if (action === 'resend' && !window.confirm('Resend 会创建新的逻辑发送记录，原消息可能已被接收。仍要继续吗？')) return;
    if (
      action === 'retry' &&
      notification?.status === 'delivery_unknown' &&
      !window.confirm('发送结果未知，重试可能造成重复通知。仍要继续吗？')
    ) return;
    setLoading(true);
    try {
      const response = await API.post(
        `/api/v1/admin/notifications/${encodeURIComponent(id)}/${action}`,
        {}
      );
      if (response.data?.success) {
        showSuccess(message);
        await load();
      } else {
        showError(response.data?.message || '操作失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const attemptsFor = (delivery) =>
    delivery.attempts || delivery.delivery_attempts || [];

  return (
    <Segment loading={loading}>
      <Header as='h2'>消息详情</Header>
      <GatewayNav />
      <p><Link to='/gateway/history'>返回消息历史</Link></p>
      {!notification ? (
        <Message info>正在读取消息记录。</Message>
      ) : (
        <>
          <div className='gateway-actions'>
            <Button primary onClick={() => runAction('retry', '已提交原任务重试')}>Retry 原任务</Button>
            <Button onClick={() => runAction('cancel', '已取消待发送任务')}>取消待发送</Button>
            <Button color='yellow' onClick={() => runAction('resend', '已创建新的逻辑发送任务')}>Resend 新发送</Button>
            <Button onClick={load}>刷新</Button>
          </div>
          <Table definition stackable>
            <Table.Body>
              <Table.Row><Table.Cell>ID</Table.Cell><Table.Cell>{notification.id}</Table.Cell></Table.Row>
              <Table.Row><Table.Cell>状态</Table.Cell><Table.Cell><Label>{notification.status || 'unknown'}</Label></Table.Cell></Table.Row>
              <Table.Row><Table.Cell>标题</Table.Cell><Table.Cell>{notification.title || '无标题'}</Table.Cell></Table.Row>
              <Table.Row><Table.Cell>正文</Table.Cell><Table.Cell><pre className='gateway-message-content'>{notification.content || '—'}</pre></Table.Cell></Table.Row>
              <Table.Row><Table.Cell>来源</Table.Cell><Table.Cell>{notification.source_name || notification.source_id || '—'}</Table.Cell></Table.Row>
              <Table.Row><Table.Cell>请求 ID</Table.Cell><Table.Cell>{notification.request_id || '—'}</Table.Cell></Table.Row>
              <Table.Row><Table.Cell>幂等键</Table.Cell><Table.Cell>{notification.idempotency_key || '—'}</Table.Cell></Table.Row>
              <Table.Row><Table.Cell>创建时间</Table.Cell><Table.Cell>{timeValue(notification.created_at)}</Table.Cell></Table.Row>
              <Table.Row><Table.Cell>更新时间</Table.Cell><Table.Cell>{timeValue(notification.updated_at)}</Table.Cell></Table.Row>
            </Table.Body>
          </Table>

          <Header as='h3' dividing>投递与发送尝试</Header>
          {!deliveries.length && <Message info>此消息目前没有投递记录。</Message>}
          {deliveries.map((delivery) => (
            <Segment key={delivery.id}>
              <Header as='h4'>
                {delivery.channel_name || delivery.channel_id || '通道'}
                {' '}
                <Label>{delivery.status || 'unknown'}</Label>
              </Header>
              <p>
                Delivery ID: {delivery.id} · 目标: {delivery.target_name || delivery.target?.name || maskTarget(delivery.target_id || delivery.target?.external_id)} ·
                {' '}尝试次数: {delivery.attempt_count ?? attemptsFor(delivery).length}
              </p>
              {delivery.next_retry_at && <p>下次重试：{timeValue(delivery.next_retry_at)}</p>}
              {delivery.last_error_code && <p>最近错误：{delivery.last_error_code} {delivery.last_error_message || ''}</p>}
              <Table stackable compact>
                <Table.Header>
                  <Table.Row>
                    <Table.HeaderCell>尝试</Table.HeaderCell>
                    <Table.HeaderCell>开始时间</Table.HeaderCell>
                    <Table.HeaderCell>耗时</Table.HeaderCell>
                    <Table.HeaderCell>结果</Table.HeaderCell>
                    <Table.HeaderCell>错误分类</Table.HeaderCell>
                    <Table.HeaderCell>HTTP / 平台错误码</Table.HeaderCell>
                    <Table.HeaderCell>平台消息 ID</Table.HeaderCell>
                    <Table.HeaderCell>错误详情</Table.HeaderCell>
                    <Table.HeaderCell>下次重试</Table.HeaderCell>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {attemptsFor(delivery).map((attempt) => (
                    <Table.Row key={attempt.id || attempt.attempt_number}>
                      <Table.Cell>{attempt.attempt_number ?? attempt.number ?? '—'}</Table.Cell>
                      <Table.Cell>{timeValue(attempt.started_at)}</Table.Cell>
                      <Table.Cell>{attempt.duration_ms ?? '—'} ms</Table.Cell>
                      <Table.Cell>{attempt.result || attempt.status || '—'}</Table.Cell>
                      <Table.Cell>{attempt.error_category || '—'}</Table.Cell>
                      <Table.Cell>{attempt.http_status ?? '—'} / {attempt.provider_error_code || attempt.error_code || '—'}</Table.Cell>
                      <Table.Cell>{attempt.provider_message_id || '—'}</Table.Cell>
                      <Table.Cell>{attempt.error_message || attempt.message || '—'}</Table.Cell>
                      <Table.Cell>{timeValue(attempt.next_retry_at)}</Table.Cell>
                    </Table.Row>
                  ))}
                </Table.Body>
              </Table>
              {attemptsFor(delivery).length === 0 && (
                <Message info size='small'>后端未返回投递尝试记录。</Message>
              )}
            </Segment>
          ))}
        </>
      )}
    </Segment>
  );
};

export default NotificationDetail;
