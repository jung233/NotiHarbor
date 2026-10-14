import React, { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
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
import { API, copy, showError, showSuccess } from '../../helpers';

const emptySource = {
  name: '',
  description: '',
  rate_limit_per_minute: 0,
  allowed_channel_ids: [],
};

const Sources = () => {
  const [sources, setSources] = useState([]);
  const [channels, setChannels] = useState([]);
  const [inputs, setInputs] = useState(emptySource);
  const [loading, setLoading] = useState(false);
  const [keyModalOpen, setKeyModalOpen] = useState(false);
  const [newKey, setNewKey] = useState('');
  const [newKeySource, setNewKeySource] = useState('');
  const [keySource, setKeySource] = useState(null);
  const [apiKeys, setApiKeys] = useState([]);
  const [rateDrafts, setRateDrafts] = useState({});
  const [sourceDrafts, setSourceDrafts] = useState({});

  const load = async () => {
    setLoading(true);
    try {
      const [sourceResponse, channelResponse] = await Promise.all([
        API.get('/api/v1/admin/sources'),
        API.get('/api/v1/admin/channels'),
      ]);
      if (sourceResponse.data?.success) {
        const result = sourceResponse.data.data || {};
        setSources(Array.isArray(result) ? result : result.items || []);
      } else {
        showError(sourceResponse.data?.message || '读取应用列表失败');
      }
      if (channelResponse.data?.success) {
        const result = channelResponse.data.data || {};
        setChannels(Array.isArray(result) ? result : result.items || []);
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // Initial page load only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openKey = (data, sourceName) => {
    const keyPayload = data?.api_key || data?.key || data?.secret || '';
    const returnedKey = typeof keyPayload === 'string'
      ? keyPayload
      : keyPayload.secret || keyPayload.key || '';
    setNewKey(returnedKey);
    setNewKeySource(sourceName);
  };

  const loadApiKeys = async (source) => {
    setKeySource(source);
    setNewKey('');
    setApiKeys([]);
    setKeyModalOpen(true);
    try {
      const response = await API.get(
        `/api/v1/admin/sources/${encodeURIComponent(source.id)}/api-keys`
      );
      if (response.data?.success) {
        const data = response.data.data || {};
        setApiKeys(Array.isArray(data) ? data : data.items || []);
      } else {
        showError(response.data?.message || '读取 API Key 列表失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    }
  };

  const createApiKey = async (source) => {
    setLoading(true);
    try {
      const response = await API.post(
        `/api/v1/admin/sources/${encodeURIComponent(source.id)}/api-keys`,
        { name: 'primary' }
      );
      if (response.data?.success) {
        const keyData = response.data.data || {};
        showSuccess('API Key 已创建，请立即保存');
        await loadApiKeys(source);
        openKey(keyData, source.name);
      } else {
        showError(response.data?.message || '创建 API Key 失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const rotateApiKey = async (source, apiKey) => {
    setLoading(true);
    try {
      const response = await API.post(
        `/api/v1/admin/sources/${encodeURIComponent(source.id)}/api-keys/${encodeURIComponent(apiKey.id)}/rotate`,
        {}
      );
      if (response.data?.success) {
        const keyData = response.data.data || {};
        showSuccess('API Key 已轮换，请立即保存新密钥');
        await loadApiKeys(source);
        openKey(keyData, source.name);
      } else {
        showError(response.data?.message || '轮换 API Key 失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const deleteApiKey = async (source, apiKey) => {
    if (!window.confirm(`删除 API Key“${apiKey.name || apiKey.prefix || apiKey.id}”？`)) return;
    setLoading(true);
    try {
      const response = await API.delete(
        `/api/v1/admin/sources/${encodeURIComponent(source.id)}/api-keys/${encodeURIComponent(apiKey.id)}`
      );
      if (response.data?.success) {
        showSuccess('API Key 已删除');
        await loadApiKeys(source);
      } else {
        showError(response.data?.message || '删除 API Key 失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const createSource = async () => {
    if (!inputs.name.trim()) {
      showError('请填写应用名称');
      return;
    }
    setLoading(true);
    try {
      const response = await API.post('/api/v1/admin/sources', inputs);
      if (response.data?.success) {
        const data = response.data.data || {};
        showSuccess('上游应用已创建');
        setInputs(emptySource);
        await load();
        await createApiKey({ id: data.id || data.source_id, name: inputs.name });
      } else {
        showError(response.data?.message || '创建应用失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const updateSource = async (source, changes, successMessage) => {
    setLoading(true);
    try {
      const response = await API.patch(
        `/api/v1/admin/sources/${encodeURIComponent(source.id)}`,
        changes
      );
      if (response.data?.success) {
        showSuccess(successMessage);
        await load();
      } else {
        showError(response.data?.message || '更新应用失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const deleteSource = async (source) => {
    if (!window.confirm(`删除应用“${source.name}”？`)) return;
    setLoading(true);
    try {
      const response = await API.delete(
        `/api/v1/admin/sources/${encodeURIComponent(source.id)}`
      );
      if (response.data?.success) {
        showSuccess('应用已删除');
        await load();
      } else {
        showError(response.data?.message || '删除应用失败');
      }
    } catch (error) {
      // The shared API interceptor reports transport errors.
    } finally {
      setLoading(false);
    }
  };

  const channelOptions = channels.map((channel) => ({
    key: channel.id,
    value: String(channel.id),
    text: channel.name || channel.channel_name || String(channel.id),
  }));

  return (
    <Segment loading={loading}>
      <Header as='h2'>上游应用与 API Key</Header>
      <GatewayNav />
      <Message info>
        每个应用拥有独立 API Key、允许使用的通道范围和请求限额。密钥只在创建或轮换时显示一次；列表不会返回完整密钥。
      </Message>
      <Header as='h3' dividing>创建应用</Header>
      <Form onSubmit={(event) => { event.preventDefault(); createSource(); }}>
        <Form.Group widths='equal'>
          <Form.Input
            required
            label='应用名称'
            placeholder='例如 NAS、Docker 或 GitHub Actions'
            value={inputs.name}
            onChange={(event, { value }) => setInputs({ ...inputs, name: value })}
          />
          <Form.Input
            label='备注'
            value={inputs.description}
            onChange={(event, { value }) => setInputs({ ...inputs, description: value })}
          />
          <Form.Input
            type='number'
            min='0'
            label='每分钟请求上限（0 表示不单独限制）'
            value={inputs.rate_limit_per_minute}
            onChange={(event, { value }) => setInputs({ ...inputs, rate_limit_per_minute: Number(value) })}
          />
        </Form.Group>
        <Form.Select
          multiple
          search
          label='允许使用的通道'
          placeholder='留空时按后端策略处理'
          options={channelOptions}
          value={inputs.allowed_channel_ids}
          onChange={(event, { value }) => setInputs({ ...inputs, allowed_channel_ids: value || [] })}
        />
        <Button primary type='submit'>创建应用并生成 API Key</Button>
      </Form>

      <Header as='h3' dividing>已配置应用</Header>
      {!sources.length && <Message info>尚未创建上游应用。</Message>}
      <Table stackable>
        <Table.Header>
          <Table.Row>
            <Table.HeaderCell>应用</Table.HeaderCell>
            <Table.HeaderCell>状态</Table.HeaderCell>
            <Table.HeaderCell>允许的通道</Table.HeaderCell>
            <Table.HeaderCell>请求上限</Table.HeaderCell>
            <Table.HeaderCell>最近更新</Table.HeaderCell>
            <Table.HeaderCell>操作</Table.HeaderCell>
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {sources.map((source) => {
            const isEnabled = source.status === undefined
              ? source.enabled !== false
              : source.status === 'active' || source.status === 1 || source.status === true;
            return (
              <Table.Row key={source.id}>
                <Table.Cell>
                  <Form.Input
                    aria-label={`${source.name} 应用名称`}
                    value={sourceDrafts[source.id]?.name ?? source.name ?? ''}
                    onChange={(event, { value }) => setSourceDrafts((current) => ({
                      ...current,
                      [source.id]: {
                        name: value,
                        description: current[source.id]?.description ?? source.description ?? '',
                      },
                    }))}
                  />
                  <Form.Input
                    aria-label={`${source.name} 应用备注`}
                    value={sourceDrafts[source.id]?.description ?? source.description ?? ''}
                    onChange={(event, { value }) => setSourceDrafts((current) => ({
                      ...current,
                      [source.id]: {
                        name: current[source.id]?.name ?? source.name ?? '',
                        description: value,
                      },
                    }))}
                  />
                  <Button
                    size='small'
                    onClick={() => {
                      const draft = sourceDrafts[source.id] || source;
                      if (String(draft.name || '').trim()) {
                        updateSource(source, {
                          name: String(draft.name).trim(),
                          description: draft.description || '',
                        }, '应用信息已更新');
                      } else {
                        showError('应用名称不能为空');
                      }
                    }}
                  >保存</Button>
                </Table.Cell>
                <Table.Cell><Label color={isEnabled ? 'green' : 'grey'}>{isEnabled ? '启用' : '停用'}</Label></Table.Cell>
                <Table.Cell>
                  <Form.Select
                    multiple
                    options={channelOptions}
                    value={(source.allowed_channel_ids || []).map(String)}
                    onChange={(event, { value }) => updateSource(source, { allowed_channel_ids: value || [] }, '通道权限已更新')}
                  />
                </Table.Cell>
                <Table.Cell>
                  <Form.Input
                    type='number'
                    min='0'
                    aria-label={`${source.name} 每分钟请求上限`}
                    value={rateDrafts[source.id] ?? source.rate_limit_per_minute ?? 0}
                    onChange={(event, { value }) => setRateDrafts((current) => ({ ...current, [source.id]: value }))}
                  />
                  <Button
                    size='small'
                    onClick={() => {
                      const rateLimit = Number(rateDrafts[source.id] ?? source.rate_limit_per_minute ?? 0);
                      if (Number.isFinite(rateLimit) && rateLimit >= 0) {
                        updateSource(source, { rate_limit_per_minute: rateLimit }, '请求上限已更新');
                      }
                    }}
                  >保存</Button>
                </Table.Cell>
                <Table.Cell>{source.updated_at ? new Date(source.updated_at).toLocaleString() : '—'}</Table.Cell>
                <Table.Cell>
                  <Button
                    size='small'
                    onClick={() => updateSource(source, { status: isEnabled ? 'disabled' : 'active' }, isEnabled ? '应用已停用' : '应用已启用')}
                  >
                    {isEnabled ? '停用' : '启用'}
                  </Button>
                  <Button size='small' primary onClick={() => loadApiKeys(source)}>API Keys</Button>
                  <Button size='small' as={Link} to={`/gateway/targets?source_id=${encodeURIComponent(source.id)}`}>目标</Button>
                  <Button size='small' negative onClick={() => deleteSource(source)}>删除</Button>
                </Table.Cell>
              </Table.Row>
            );
          })}
        </Table.Body>
      </Table>

      <Modal open={keyModalOpen} onClose={() => { setKeyModalOpen(false); setNewKey(''); }} size='small'>
        <Modal.Header>管理 API Keys · {keySource?.name || newKeySource}</Modal.Header>
        <Modal.Content>
          {newKey ? (
            <>
              <Message warning>此密钥只显示这一次。请安全保存；关闭窗口后无法再次读取。</Message>
              <Form.TextArea readOnly value={newKey} />
            </>
          ) : null}
          <Header as='h4' dividing>密钥记录</Header>
          <Table compact>
            <Table.Header><Table.Row><Table.HeaderCell>名称 / 前缀</Table.HeaderCell><Table.HeaderCell>状态</Table.HeaderCell><Table.HeaderCell>最近使用</Table.HeaderCell><Table.HeaderCell>操作</Table.HeaderCell></Table.Row></Table.Header>
            <Table.Body>
              {apiKeys.map((apiKey) => (
                <Table.Row key={apiKey.id}>
                  <Table.Cell>{apiKey.name || apiKey.prefix || apiKey.id}</Table.Cell>
                  <Table.Cell>{apiKey.status || 'active'}</Table.Cell>
                  <Table.Cell>{apiKey.last_used_at ? new Date(apiKey.last_used_at).toLocaleString() : '—'}</Table.Cell>
                  <Table.Cell>
                    <Button size='small' onClick={() => rotateApiKey(keySource, apiKey)}>轮换</Button>
                    <Button size='small' negative onClick={() => deleteApiKey(keySource, apiKey)}>删除</Button>
                  </Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
        </Modal.Content>
        <Modal.Actions>
          {keySource && <Button onClick={() => createApiKey(keySource)}>新增 Key</Button>}
          {newKey && <Button onClick={async () => (await copy(newKey)) && showSuccess('已复制 API Key')}>复制</Button>}
          <Button primary onClick={() => { setKeyModalOpen(false); setNewKey(''); }}>完成</Button>
        </Modal.Actions>
      </Modal>
    </Segment>
  );
};

export default Sources;
