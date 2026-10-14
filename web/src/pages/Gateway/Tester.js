import React, { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Button,
  Form,
  Header,
  Message,
  Segment,
} from 'semantic-ui-react';
import GatewayNav from '../../components/GatewayNav';
import { API, showError, showSuccess } from '../../helpers';

const CHANNEL_TYPES = [
  { key: 'text', value: 'text', text: '文本' },
  { key: 'markdown', value: 'markdown', text: 'Markdown' },
];

const readChannelConfig = (channel) => {
  if (!channel?.other) return {};
  if (typeof channel.other !== 'string') return channel.other;
  try {
    return JSON.parse(channel.other);
  } catch (error) {
    return {};
  }
};

const Tester = () => {
  const [channels, setChannels] = useState([]);
  const [targets, setTargets] = useState([]);
  const [sources, setSources] = useState([]);
  const [sourceId, setSourceId] = useState('');
  const [channelId, setChannelId] = useState('');
  const [targetId, setTargetId] = useState('');
  const [inputs, setInputs] = useState({
    title: '',
    content: '',
    message_type: 'text',
    delivery_mode: 'active',
  });
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState(null);

  const loadChannels = async () => {
    try {
      const [response, sourceResponse] = await Promise.all([
        API.get('/api/v1/admin/channels'),
        API.get('/api/v1/admin/sources'),
      ]);
      if (response.data?.success) {
        const items = response.data.data?.items || response.data.data || [];
        const activeChannels = items.filter((channel) => channel.enabled !== false && channel.status !== 2);
        setChannels(activeChannels);
        if (!channelId && activeChannels.length) {
          setChannelId(String(activeChannels[0].id));
        }
      } else {
        showError(response.data?.message || '读取通道列表失败');
      }
      if (sourceResponse.data?.success) {
        const result = sourceResponse.data.data || {};
        const items = Array.isArray(result) ? result : result.items || [];
        setSources(items);
        if (!sourceId && items.length) setSourceId(String(items[0].id));
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    }
  };

  const loadTargets = async (selectedSourceId) => {
    setTargets([]);
    setTargetId('');
    if (!selectedSourceId) {
      return;
    }
    try {
      const response = await API.get(
        `/api/v1/admin/sources/${encodeURIComponent(selectedSourceId)}/targets`
      );
      if (response.data?.success) {
        const items = response.data.data?.items || response.data.data || [];
        setTargets(items);
        const channelTargets = items.filter(
          (target) => !channelId || !target.channel_id || String(target.channel_id) === String(channelId)
        );
        const defaultTarget = channelTargets.find((target) => target.is_default || target.default);
        setTargetId(String(defaultTarget?.id || defaultTarget?.target_id || channelTargets[0]?.id || channelTargets[0]?.target_id || ''));
      } else {
        setTargets([]);
        showError(response.data?.message || '读取通道目标失败');
      }
    } catch (error) {
      setTargets([]);
    }
  };

  useEffect(() => {
    loadChannels();
    // Load the initial choices once; later target fetches follow sourceId.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    loadTargets(sourceId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sourceId]);

  const submit = async () => {
    if (!sourceId || !channelId || !inputs.content.trim()) {
      showError('请选择所属应用与通道，并填写消息正文');
      return;
    }
    setLoading(true);
    setResult(null);
    try {
      const response = await API.post(
        `/api/v1/admin/channels/${encodeURIComponent(channelId)}/test`,
          {
          source_id: sourceId,
          target_id: targetId || undefined,
          title: inputs.title,
          content: inputs.content,
          message_type: inputs.message_type,
          ...(isQQBot ? { delivery_mode: inputs.delivery_mode } : {}),
        }
      );
      setResult(response.data?.data || response.data);
      if (response.data?.success) {
        showSuccess('测试请求已提交');
      } else {
        showError(response.data?.message || '测试发送失败');
      }
    } catch (error) {
      setResult(error.response?.data?.data || error.response?.data || { message: error.message });
    } finally {
      setLoading(false);
    }
  };

  const channelOptions = channels.map((channel) => ({
    key: channel.id,
    value: String(channel.id),
    text: `${channel.name || channel.channel_name || channel.id} (${channel.type || 'channel'})`,
  }));
  const targetOptions = targets
    .filter((target) => !target.channel_id || String(target.channel_id) === String(channelId))
    .map((target) => ({
      key: target.id || target.target_id,
      value: String(target.id || target.target_id),
      text: `${target.name || target.display_name || target.masked_openid || target.target_id || target.id}${target.is_default || target.default ? ' · 默认' : ''}`,
    }));
  const selectedChannel = channels.find((channel) => String(channel.id) === String(channelId));
  const selectedChannelConfig = readChannelConfig(selectedChannel);
  const isQQBot = selectedChannel?.type === 'qqbot_v2';
  const selectedTarget = targets.find(
    (target) => String(target.id || target.target_id) === String(targetId)
  );
  const targetType = selectedTarget?.target_type || selectedChannelConfig.target_type || 'user';
  const deliveryModeOptions = [
    { key: 'active', value: 'active', text: '普通主动消息（默认，无需回调）' },
    { key: 'passive', value: 'passive', text: '被动回复（需近期交互事件）' },
    { key: 'wakeup', value: 'wakeup', text: '互动召回（仅 C2C 私聊）', disabled: targetType !== 'user' },
  ];

  return (
    <Segment loading={loading}>
      <Header as='h2'>消息测试器</Header>
      <GatewayNav />
      <Message warning>
        {isQQBot
          ? '测试会实际发送 QQ 消息，后端会将记录标记为测试消息。普通主动消息为默认模式，无需回调事件，但机器人所有者必须允许主动发送。被动回复依赖目标近期的交互事件：官方概述列出 C2C 60 分钟内最多 4 次，但 msg_id 字段文档仍写 5 分钟；本系统暂按 5 分钟保守校验。群聊为 5 分钟内最多 5 次。互动召回仅支持 C2C 私聊。'
          : '测试会实际调用所选通道并可能向目标发送消息，后端会将记录标记为测试消息。'}
      </Message>
      {!sources.length && <Message info>尚无上游应用。请先在「应用与 API Key」中创建应用，再选择它运行测试。</Message>}
      {!channels.length && <Message info>没有可用通道。请先在通道管理中配置通道。</Message>}
      <Form onSubmit={(event) => { event.preventDefault(); submit(); }}>
        <Form.Group widths='equal'>
          <Form.Select
            required
            search
            label='所属应用'
            placeholder='选择上游应用'
            options={sources.map((source) => ({ key: source.id, value: String(source.id), text: source.name }))}
            value={sourceId}
            onChange={(event, { value }) => setSourceId(value)}
          />
          <Form.Select
            required
            search
            label='通道'
            placeholder='选择通道'
            options={channelOptions}
            value={channelId}
            onChange={(event, { value }) => {
              setChannelId(value);
              const channelTargets = targets.filter(
                (target) => !target.channel_id || String(target.channel_id) === String(value)
              );
              const defaultTarget = channelTargets.find((target) => target.is_default || target.default);
              setTargetId(String(defaultTarget?.id || defaultTarget?.target_id || channelTargets[0]?.id || channelTargets[0]?.target_id || ''));
              const nextChannel = channels.find((channel) => String(channel.id) === String(value));
              const nextConfig = readChannelConfig(nextChannel);
              const nextTargetType = defaultTarget?.target_type || nextConfig.target_type || 'user';
              if (inputs.delivery_mode === 'wakeup' && (nextChannel?.type !== 'qqbot_v2' || nextTargetType !== 'user')) {
                setInputs((current) => ({ ...current, delivery_mode: 'active' }));
              }
            }}
          />
          <Form.Select
            search
            clearable
            label='目标'
            placeholder='选择目标，留空使用默认目标'
            options={targetOptions}
            value={targetId}
            onChange={(event, { value }) => {
              setTargetId(value || '');
              const nextTarget = targets.find(
                (target) => String(target.id || target.target_id) === String(value || '')
              );
              const nextTargetType = nextTarget?.target_type || selectedChannelConfig.target_type || 'user';
              if (inputs.delivery_mode === 'wakeup' && nextTargetType !== 'user') {
                setInputs((current) => ({ ...current, delivery_mode: 'active' }));
              }
            }}
            noResultsMessage='没有可用目标'
          />
        </Form.Group>
        <Form.Group widths='equal'>
          <Form.Input
            label='标题'
            value={inputs.title}
            onChange={(event, { value }) => setInputs({ ...inputs, title: value })}
          />
          <Form.Select
            label='消息格式'
            options={CHANNEL_TYPES}
            value={inputs.message_type}
            onChange={(event, { value }) => setInputs({ ...inputs, message_type: value })}
          />
          {isQQBot && (
            <Form.Select
              label='QQ 发送模式'
              options={deliveryModeOptions}
              value={inputs.delivery_mode}
              onChange={(event, { value }) => {
                if (value === 'wakeup' && targetType !== 'user') {
                  showError('互动召回仅支持 C2C 私聊目标');
                  return;
                }
                setInputs((current) => ({ ...current, delivery_mode: value }));
              }}
            />
          )}
        </Form.Group>
        <Form.TextArea
          required
          label='正文'
          value={inputs.content}
          onChange={(event, { value }) => setInputs({ ...inputs, content: value })}
          style={{ minHeight: 180 }}
        />
        <Button primary type='submit' disabled={!sourceId || !channelId} loading={loading}>
          发送测试消息
        </Button>
      </Form>
      {result && (
        <Segment>
          <Header as='h3'>发送结果</Header>
          {(result.attempt?.duration_ms ?? result.duration_ms) !== undefined && <p>耗时：{result.attempt?.duration_ms ?? result.duration_ms} ms</p>}
          {(result.attempt?.provider_error_code || result.error_code) && <p>错误码：{result.attempt?.provider_error_code || result.error_code}</p>}
          {(result.attempt?.error_message || result.send_error) && <p>错误详情：{result.attempt?.error_message || result.send_error}</p>}
          {(result.attempt?.retry_count ?? result.retry_count) !== undefined && <p>重试次数：{result.attempt?.retry_count ?? result.retry_count}</p>}
          {(result.notification_id || result.attempt?.notification_id) && (
            <p><Link to={`/gateway/history/${result.notification_id || result.attempt.notification_id}`}>查看对应消息详情</Link></p>
          )}
          {(result.attempt?.delivery_id || result.delivery_id) && (
            <p><Link to={`/gateway/logs?delivery_id=${encodeURIComponent(result.attempt?.delivery_id || result.delivery_id)}`}>查看对应发送日志</Link></p>
          )}
          <pre className='gateway-message-content'>{JSON.stringify(result, null, 2)}</pre>
        </Segment>
      )}
    </Segment>
  );
};

export default Tester;
