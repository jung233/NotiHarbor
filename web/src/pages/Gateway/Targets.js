import React, { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Button,
  Form,
  Header,
  Label,
  Message,
  Modal,
  Segment,
  Table,
} from 'semantic-ui-react';
import GatewayNav from '../../components/GatewayNav';
import { API, showError, showSuccess } from '../../helpers';

const blankTarget = {
  channel_id: '',
  target_type: 'user',
  external_id: '',
  name: '',
};

const maskOpenId = (value) => {
  const text = String(value || '');
  if (text.length < 8) return '••••';
  return `${text.slice(0, 3)}••••${text.slice(-3)}`;
};

const Targets = () => {
  const [searchParams] = useSearchParams();
  const [sources, setSources] = useState([]);
  const [channels, setChannels] = useState([]);
  const [sourceId, setSourceId] = useState(searchParams.get('source_id') || '');
  const [targets, setTargets] = useState([]);
  const [inputs, setInputs] = useState(blankTarget);
  const [editingTarget, setEditingTarget] = useState(null);
  const [loading, setLoading] = useState(false);

  const loadOptions = async () => {
    try {
      const [sourceResponse, channelResponse] = await Promise.all([
        API.get('/api/v1/admin/sources'),
        API.get('/api/v1/admin/channels'),
      ]);
      if (sourceResponse.data?.success) {
        const data = sourceResponse.data.data || {};
        const items = Array.isArray(data) ? data : data.items || [];
        setSources(items);
        if (!sourceId && items.length) setSourceId(String(items[0].id));
      }
      if (channelResponse.data?.success) {
        const data = channelResponse.data.data || {};
        const items = Array.isArray(data) ? data : data.items || [];
        setChannels(items);
        if (!inputs.channel_id && items.length) {
          setInputs((current) => ({ ...current, channel_id: String(items[0].id) }));
        }
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    }
  };

  const loadTargets = async (selectedSourceId = sourceId) => {
    if (!selectedSourceId) {
      setTargets([]);
      return;
    }
    setLoading(true);
    try {
      const response = await API.get(
        `/api/v1/admin/sources/${encodeURIComponent(selectedSourceId)}/targets`
      );
      if (response.data?.success) {
        const data = response.data.data || {};
        setTargets(Array.isArray(data) ? data : data.items || []);
      } else {
        showError(response.data?.message || '读取 QQ Bot 目标失败');
      }
    } catch (error) {
      setTargets([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadOptions();
    // Initial option load only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    loadTargets(sourceId);
    // Refresh targets when the selected source changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sourceId]);

  const createTarget = async () => {
    if (!sourceId || !inputs.channel_id || !inputs.external_id.trim()) {
      showError('请选择应用和通道，并填写 OpenID');
      return;
    }
    setLoading(true);
    try {
      const response = await API.post(
        `/api/v1/admin/sources/${encodeURIComponent(sourceId)}/targets`,
        { ...inputs, source: 'manual' }
      );
      if (response.data?.success) {
        showSuccess('目标已添加');
        setInputs({ ...blankTarget, channel_id: inputs.channel_id });
        await loadTargets(sourceId);
      } else {
        showError(response.data?.message || '添加目标失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const updateTarget = async (target, changes, successMessage) => {
    setLoading(true);
    try {
      const response = await API.patch(
        `/api/v1/admin/sources/${encodeURIComponent(sourceId)}/targets/${encodeURIComponent(target.id)}`,
        changes
      );
      if (response.data?.success) {
        showSuccess(successMessage);
        await loadTargets(sourceId);
      } else {
        showError(response.data?.message || '更新目标失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const saveTargetDetails = async () => {
    if (!editingTarget?.name?.trim()) {
      showError('请填写目标名称或备注');
      return;
    }
    setLoading(true);
    try {
      const response = await API.patch(
        `/api/v1/admin/sources/${encodeURIComponent(sourceId)}/targets/${encodeURIComponent(editingTarget.id)}`,
        { name: editingTarget.name.trim(), target_type: editingTarget.target_type }
      );
      if (response.data?.success) {
        showSuccess('目标信息已更新');
        setEditingTarget(null);
        await loadTargets(sourceId);
      } else {
        showError(response.data?.message || '更新目标失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const deleteTarget = async (target) => {
    if (!window.confirm(`删除目标“${target.name || target.masked_openid || maskOpenId(target.external_id)}”？`)) return;
    setLoading(true);
    try {
      const response = await API.delete(
        `/api/v1/admin/sources/${encodeURIComponent(sourceId)}/targets/${encodeURIComponent(target.id)}`
      );
      if (response.data?.success) {
        showSuccess('目标已删除');
        await loadTargets(sourceId);
      } else {
        showError(response.data?.message || '删除目标失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const sourceOptions = sources.map((source) => ({
    key: source.id,
    value: String(source.id),
    text: source.name,
  }));
  const channelOptions = channels
    .filter((channel) => channel.type === 'qqbot_v2')
    .map((channel) => ({ key: channel.id, value: String(channel.id), text: channel.name }));

  return (
    <Segment loading={loading}>
      <Header as='h2'>QQ Bot 发送目标</Header>
      <GatewayNav />
      <Message info>
        目标记录按上游应用隔离。优先使用机器人收到官方事件后自动采集的 OpenID；普通 QQ 号不是 OpenID。
      </Message>
      <Form>
        <Form.Select
          search
          label='所属应用'
          options={sourceOptions}
          value={sourceId}
          onChange={(event, { value }) => setSourceId(value)}
        />
      </Form>
      <Header as='h3' dividing>手动添加目标</Header>
      {!channelOptions.length && <Message warning>尚未配置 `qqbot_v2` 通道。</Message>}
      <Form onSubmit={(event) => { event.preventDefault(); createTarget(); }}>
        <Form.Group widths='equal'>
          <Form.Select
            required
            label='QQ Bot 通道'
            options={channelOptions}
            value={inputs.channel_id}
            onChange={(event, { value }) => setInputs({ ...inputs, channel_id: value })}
          />
          <Form.Select
            label='目标类型'
            options={[
              { key: 'user', value: 'user', text: '私聊用户' },
              { key: 'group', value: 'group', text: '群聊' },
            ]}
            value={inputs.target_type}
            onChange={(event, { value }) => setInputs({ ...inputs, target_type: value })}
          />
          <Form.Input
            required
            label='官方 OpenID'
            value={inputs.external_id}
            onChange={(event, { value }) => setInputs({ ...inputs, external_id: value })}
          />
          <Form.Input
            label='名称或备注'
            value={inputs.name}
            onChange={(event, { value }) => setInputs({ ...inputs, name: value })}
          />
        </Form.Group>
        <Button primary type='submit'>添加目标</Button>
      </Form>

      <Header as='h3' dividing>已发现与配置的目标</Header>
      {!targets.length && <Message info>此应用尚无目标。用户或群组与机器人交互后，可由官方事件自动发现目标。</Message>}
      <Table stackable>
        <Table.Header>
          <Table.Row>
            <Table.HeaderCell>OpenID 摘要</Table.HeaderCell>
            <Table.HeaderCell>类型 / 通道</Table.HeaderCell>
            <Table.HeaderCell>来源</Table.HeaderCell>
            <Table.HeaderCell>最近交互</Table.HeaderCell>
            <Table.HeaderCell>最近发送</Table.HeaderCell>
            <Table.HeaderCell>状态</Table.HeaderCell>
            <Table.HeaderCell>操作</Table.HeaderCell>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {targets.map((target) => {
            const enabled = target.status === undefined
              ? target.enabled !== false
              : target.status === 'active' || target.status === 1 || target.status === true;
            return (
              <Table.Row key={target.id}>
                <Table.Cell>{target.masked_openid || maskOpenId(target.external_id)}<br /><small>{target.name || ''}</small></Table.Cell>
                <Table.Cell>{target.target_type || '—'} · {target.channel_name || target.channel_id || '—'}</Table.Cell>
                <Table.Cell>{target.source || 'unknown'}</Table.Cell>
                <Table.Cell>{target.last_interaction_at ? new Date(target.last_interaction_at).toLocaleString() : '—'}</Table.Cell>
                <Table.Cell>{target.last_send_status || '—'}</Table.Cell>
                <Table.Cell>
                  {target.is_default && <Label color='blue'>默认</Label>}
                  <Label color={enabled ? 'green' : 'grey'}>{enabled ? '启用' : '停用'}</Label>
                </Table.Cell>
                <Table.Cell>
                  {!target.is_default && <Button size='small' onClick={() => updateTarget(target, { is_default: true }, '已设为默认目标')}>设为默认</Button>}
                  <Button size='small' onClick={() => setEditingTarget({ ...target, name: target.name || '' })}>编辑</Button>
                  <Button size='small' onClick={() => updateTarget(target, { status: enabled ? 'disabled' : 'active' }, enabled ? '目标已停用' : '目标已启用')}>{enabled ? '停用' : '启用'}</Button>
                  <Button size='small' negative onClick={() => deleteTarget(target)}>删除</Button>
                </Table.Cell>
              </Table.Row>
            );
          })}
        </Table.Body>
      </Table>
      <Modal open={Boolean(editingTarget)} onClose={() => setEditingTarget(null)} size='small'>
        <Modal.Header>编辑目标</Modal.Header>
        <Modal.Content>
          <Form onSubmit={(event) => { event.preventDefault(); saveTargetDetails(); }}>
            <Form.Select
              label='目标类型'
              options={[
                { key: 'user', value: 'user', text: '私聊用户' },
                { key: 'group', value: 'group', text: '群聊' },
              ]}
              value={editingTarget?.target_type || 'user'}
              onChange={(event, { value }) => setEditingTarget((current) => ({ ...current, target_type: value }))}
            />
            <Form.Input
              required
              label='名称或备注'
              value={editingTarget?.name || ''}
              onChange={(event, { value }) => setEditingTarget((current) => ({ ...current, name: value }))}
            />
            <p>OpenID 以脱敏形式管理，编辑此处不会更改 OpenID。</p>
          </Form>
        </Modal.Content>
        <Modal.Actions>
          <Button onClick={() => setEditingTarget(null)}>取消</Button>
          <Button primary onClick={saveTargetDetails}>保存</Button>
        </Modal.Actions>
      </Modal>
    </Segment>
  );
};

export default Targets;
