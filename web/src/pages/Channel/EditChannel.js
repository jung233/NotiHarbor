import React, { useEffect, useState } from 'react';
import { Button, Form, Header, Message, Segment } from 'semantic-ui-react';
import { useParams } from 'react-router-dom';
import { API, generateToken, showError, showSuccess } from '../../helpers';
import { CHANNEL_OPTIONS } from '../../constants';
import axios from 'axios';
import { Link } from 'react-router-dom';

const EditChannel = () => {
  const params = useParams();
  const channelId = params.id;
  const isEditing = channelId !== undefined;
  const [loading, setLoading] = useState(isEditing);
  const originInputs = {
    type: 'none',
    name: '',
    description: '',
    secret: '',
    app_id: '',
    account_id: '',
    url: '',
    other: '',
    corp_id: '', // only for corp_app
    agent_id: '', // only for corp_app
    token: '',
  };

  const [inputs, setInputs] = useState(originInputs);
  const smtpDefaults = {
    tls_mode: '',
    timeout_seconds: 30,
    html: true,
    from_name: '',
    from_address: '',
  };
  const [smtpConfig, setSmtpConfig] = useState(smtpDefaults);
  const [smtpConfigError, setSmtpConfigError] = useState(false);
  const [qqConfig, setQqConfig] = useState({
    bot_name: '',
    default_target: '',
    target_type: 'user',
    message_type: 'text',
    delivery_mode: 'active',
    timeout_seconds: 10,
    rate_limit_per_second: 5,
    bot_rate_limit_per_minute: 30,
    target_rate_limit_per_minute: 20,
    target_rate_limit_per_day: 1000,
    retry_max_attempts: 5,
    retry_backoff_seconds: [30, 120, 300, 900],
    markdown_template_id: '',
  });
  const { type, name, description, secret, app_id, account_id, url, other } =
    inputs;

  const handleInputChange = (e, { name, value }) => {
    setInputs((inputs) => ({ ...inputs, [name]: value }));
  };

  const loadChannel = async () => {
    let res = await API.get(`/api/channel/${channelId}`);
    const { success, message, data } = res.data;
    if (success) {
      if (data.type === 'corp_app') {
        const [corp_id, agent_id] = data.app_id.split('|');
        data.corp_id = corp_id;
        data.agent_id = agent_id;
      }
      if (data.type === 'qqbot_v2') {
        try {
          const savedConfig = typeof data.other === 'string'
            ? JSON.parse(data.other || '{}')
            : data.other || {};
          setQqConfig((current) => ({
            ...current,
            ...savedConfig,
            default_target: savedConfig.default_target || data.account_id || '',
          }));
        } catch (error) {
          showError('QQ Bot 配置格式无效，保存前请检查配置');
        }
      }
      if (data.type === 'email') {
        try {
          const savedConfig = JSON.parse(data.other || '{}');
          if (!savedConfig || Array.isArray(savedConfig) || typeof savedConfig !== 'object') {
            throw new Error('invalid SMTP configuration');
          }
          setSmtpConfig({
            ...smtpDefaults,
            ...savedConfig,
            timeout_seconds: savedConfig.timeout_seconds || 30,
            html: savedConfig.html !== false,
          });
        } catch (error) {
          setSmtpConfigError(true);
          showError('SMTP 配置格式无效，请修复后再保存');
        }
      }
      setInputs(data);
    } else {
      showError(message);
    }
    setLoading(false);
  };
  useEffect(() => {
    if (isEditing) {
      loadChannel().then();
    }
    // Load the selected channel once when the editor mounts.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const submit = async () => {
    if (!name) return;
    let res = undefined;
    let localInputs = { ...inputs };
    if (inputs.type === 'email') {
      if (smtpConfigError) {
        showError('SMTP 配置格式无效，请先通过 API 修复原有配置');
        return;
      }
      localInputs.url = String(inputs.url || '').trim();
      localInputs.app_id = String(inputs.app_id || '').trim();
      localInputs.account_id = String(inputs.account_id || '').trim();
      const customSMTP = localInputs.url || localInputs.app_id || localInputs.account_id || localInputs.secret;
      if (customSMTP && (!localInputs.url || !localInputs.app_id || (!isEditing && !localInputs.secret))) {
        showError('独立 SMTP 通道请填写服务器、账号和密码／授权码');
        return;
      }
      if (customSMTP && smtpConfig.tls_mode === 'none') {
        showError('SMTP 账号认证需要使用 TLS 或 STARTTLS');
        return;
      }
      if (localInputs.url && /[\s/:\\@[\]]/.test(localInputs.url)) {
        showError('SMTP 服务器请填写主机名，不要包含协议、端口或路径');
        return;
      }
      if (localInputs.account_id && (!/^\d+$/.test(localInputs.account_id) || Number(localInputs.account_id) < 1 || Number(localInputs.account_id) > 65535)) {
        showError('SMTP 端口需为 1–65535 的整数');
        return;
      }
      if (!Number.isInteger(smtpConfig.timeout_seconds) || smtpConfig.timeout_seconds < 1 || smtpConfig.timeout_seconds > 120) {
        showError('SMTP 超时需为 1–120 秒的整数');
        return;
      }
      localInputs.other = JSON.stringify(smtpConfig);
    }
    if (inputs.type === 'qqbot_v2') {
      if (qqConfig.delivery_mode === 'wakeup' && qqConfig.target_type !== 'user') {
        showError('互动召回仅支持 C2C 私聊目标');
        return;
      }
      const backoff = Array.isArray(qqConfig.retry_backoff_seconds)
        ? qqConfig.retry_backoff_seconds
        : [];
      if (
        qqConfig.retry_max_attempts < 1 ||
        qqConfig.retry_max_attempts > 20 ||
        qqConfig.timeout_seconds < 1 ||
        qqConfig.timeout_seconds > 60 ||
        qqConfig.rate_limit_per_second < 1 ||
        qqConfig.rate_limit_per_second > 5 ||
        qqConfig.bot_rate_limit_per_minute < 1 ||
        qqConfig.bot_rate_limit_per_minute > 30 ||
        qqConfig.target_rate_limit_per_minute < 1 ||
        qqConfig.target_rate_limit_per_minute > 20 ||
        qqConfig.target_rate_limit_per_day < 1 ||
        qqConfig.target_rate_limit_per_day > 1000 ||
        backoff.length < 1 ||
        backoff.length > 6 ||
        backoff.some(
          (seconds) =>
            !Number.isInteger(seconds) || seconds < 1 || seconds > 86400
        )
      ) {
        showError('超时需为 1–60 秒，Bot 每秒上限为 1–5、每分钟上限为 1–30，单目标每分钟上限为 1–20、每天上限为 1–1000；重试次数为 1–20，间隔需为 1–6 个 1–86400 秒的值');
        return;
      }
      const defaultTarget = String(qqConfig.default_target || '').trim();
      localInputs.account_id = defaultTarget;
      localInputs.url = String(inputs.url || 'https://api.bot.qq.com').trim();
      localInputs.other = JSON.stringify({ ...qqConfig, default_target: defaultTarget });
    }
    switch (inputs.type) {
      case 'corp_app':
        localInputs.app_id = `${inputs.corp_id}|${inputs.agent_id}`;
        break;
      case 'bark':
        if (localInputs.url === '') {
          localInputs.url = 'https://api.day.app';
        }
        break;
      case 'one_bot':
        if (localInputs.url.endsWith('/')) {
          localInputs.url = localInputs.url.slice(0, -1);
        }
        break;
      case 'group':
        let channels = localInputs.app_id.split('|');
        let targets = localInputs.account_id.split('|');
        if (localInputs.account_id === '') {
          for (let i = 0; i < channels.length - 1; i++) {
            localInputs.account_id += '|';
          }
        } else if (channels.length !== targets.length) {
          showError(
            '群组通道的子通道数量与目标数量不匹配，对于不需要指定的目标请直接留空'
          );
          return;
        }
        break;
      case 'custom':
        // if (!localInputs.url.startsWith('https://')) {
        //   showError('自定义通道的 URL 必须以 https:// 开头！');
        //   return;
        // }
        try {
          JSON.parse(localInputs.other);
        } catch (e) {
          showError('JSON 格式错误：' + e.message);
          return;
        }
        break;
    }
    if (isEditing) {
      res = await API.put(`/api/channel/`, {
        ...localInputs,
        id: parseInt(channelId),
      });
    } else {
      res = await API.post(`/api/channel`, localInputs);
    }
    const { success, message } = res.data;
    if (success) {
      if (isEditing) {
        showSuccess('通道信息更新成功！');
      } else {
        showSuccess('通道创建成功！');
        setInputs(originInputs);
        setSmtpConfig(smtpDefaults);
      }
    } else {
      showError(message);
    }
  };

  const getTelegramChatId = async () => {
    if (inputs.telegram_bot_token === '') {
      showError('请先输入 Telegram 机器人令牌！');
      return;
    }
    let res = await axios.get(
      `https://api.telegram.org/bot${inputs.secret}/getUpdates`
    );
    const { ok } = res.data;
    if (ok) {
      let result = res.data.result;
      if (result.length === 0) {
        showError(`请先向你的机器人发送一条任意消息！`);
      } else {
        let id = result[0]?.message?.chat?.id;
        id = id.toString();
        setInputs((inputs) => ({ ...inputs, account_id: id }));
        showSuccess('会话 ID 获取成功！');
      }
    } else {
      showError(`发生错误：${res.description}`);
    }
  };

  const renderChannelForm = () => {
    switch (type) {
      case 'email':
        return (
          <>
            <Message info>
              通过 SMTP 发送消息邮件。填写下方信息可使用独立 SMTP 服务器；连接信息全部留空则使用系统 SMTP。
              默认发送到个人设置中绑定的邮箱，也可在推送请求中指定收件人（受发信权限控制）。
            </Message>
            <Form.Group widths='equal'>
              <Form.Input label='SMTP 服务器' name='url' value={url} onChange={handleInputChange} placeholder='例如 smtp.example.com，不含协议和端口' />
              <Form.Input label='SMTP 端口' name='account_id' value={account_id} onChange={handleInputChange} type='number' min='1' max='65535' placeholder='留空使用系统端口，通常为 587 或 465' />
            </Form.Group>
            <Form.Group widths='equal'>
              <Form.Input label='SMTP 账号' name='app_id' value={app_id} onChange={handleInputChange} autoComplete='off' placeholder='通常为发件邮箱地址' />
              <Form.Input label='SMTP 密码／授权码' name='secret' value={secret} onChange={handleInputChange} type='password' autoComplete='new-password' placeholder={isEditing ? '留空保留已保存凭证' : '输入邮箱密码或应用授权码'} />
            </Form.Group>
            <Form.Group widths='equal'>
              <Form.Select label='连接安全' value={smtpConfig.tls_mode} options={[
                { key: 'auto', value: '', text: '自动（465 使用 TLS，其他端口使用 STARTTLS）' },
                { key: 'tls', value: 'tls', text: 'TLS' },
                { key: 'starttls', value: 'starttls', text: 'STARTTLS' },
                { key: 'none', value: 'none', text: '明文（仅限无认证的系统 SMTP）' },
              ]} onChange={(event, { value }) => setSmtpConfig((current) => ({ ...current, tls_mode: value }))} />
              <Form.Input label='发送超时（秒）' type='number' min='1' max='120' value={smtpConfig.timeout_seconds} onChange={(event, { value }) => setSmtpConfig((current) => ({ ...current, timeout_seconds: Number(value) }))} />
            </Form.Group>
            <Form.Group widths='equal'>
              <Form.Input label='发件人名称（可选）' value={smtpConfig.from_name} onChange={(event, { value }) => setSmtpConfig((current) => ({ ...current, from_name: value }))} />
              <Form.Input label='发件邮箱（可选）' value={smtpConfig.from_address} placeholder='默认使用 SMTP 账号' onChange={(event, { value }) => setSmtpConfig((current) => ({ ...current, from_address: value }))} />
            </Form.Group>
            <Form.Checkbox label='同时发送 Markdown 渲染后的 HTML 正文' checked={smtpConfig.html} onChange={(event, { checked }) => setSmtpConfig((current) => ({ ...current, html: checked }))} />
          </>
        );
      case 'test':
        return (
          <>
            <Message>
              通过微信测试号进行推送，点击前往配置：
              <a
                target='_blank' rel='noreferrer'
                href='https://mp.weixin.qq.com/debug/cgi-bin/sandboxinfo?action=showinfo&t=sandbox/index'
              >
                微信公众平台接口测试帐号
              </a>
              。
              <br />
              需要新增测试模板，模板标题推荐填写为「消息推送」，模板内容填写为：
              <br />
              标题：{' {{'}title.DATA{'}}'}
              <br />
              描述：{' {{'}description.DATA{'}}'}
              <br />
              内容：{' {{'}content.DATA{'}}'}
            </Message>
            <Form.Group widths={3}>
              <Form.Input
                label='测试号 ID'
                name='app_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.app_id}
                placeholder='测试号信息 -> appID'
              />
              <Form.Input
                label='测试号密钥'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='测试号信息 -> appsecret'
              />
              <Form.Input
                label='测试模板 ID'
                name='other'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.other}
                placeholder='模板消息接口 -> 模板 ID'
              />
            </Form.Group>
            <Form.Group widths={3}>
              <Form.Input
                label='用户 Open ID'
                name='account_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.account_id}
                placeholder='扫描测试号二维码 -> 用户列表 -> 微信号'
              />
            </Form.Group>
          </>
        );
      case 'tencent_alarm':
        return (
          <>
            <Message>
              通过腾讯云自定义消息告警进行推送，
              <a
                target='_blank' rel='noreferrer'
                href='https://github.com/songquanpeng/message-pusher/issues/87#issuecomment-1547971847'
              >
                配置教程
              </a>
              。
            </Message>
            <Form.Group widths={3}>
              <Form.Input
                label='SecretId'
                name='app_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.app_id}
                placeholder='子账号的 SecretId'
              />
              <Form.Input
                label='SecretKey'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='子账号的 SecretKey'
              />
              <Form.Input
                label='消息策略 ID'
                name='account_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.account_id}
                placeholder='例如：cm-6gl3pq19'
              />
            </Form.Group>
            <Form.Group widths={3}>
              <Form.Input
                label='区域'
                name='other'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.other}
                placeholder='例如：ap-shanghai'
              />
            </Form.Group>
          </>
        );
      case 'corp_app':
        return (
          <>
            <Message>
              通过企业微信应用号进行推送，点击前往配置：
              <a
                target='_blank' rel='noreferrer'
                href='https://work.weixin.qq.com/wework_admin/frame#apps'
              >
                企业微信应用管理
              </a>
              。
              <br />
              <br />
              注意，企业微信要求配置可信 IP，步骤：应用管理 -> 自建 -> 创建应用
              -> 应用设置页面下拉中找到「企业可信 IP」，点击配置 -> 设置可信域名
              -> 在「可调用
              JS-SDK、跳转小程序的可信域名」下面填写一个域名，然后点击「申请校验域名」，根据提示完成校验
              -> 之后填写服务器 IP 地址（此 IP
              地址是消息推送服务所部署在的服务器的 IP
              地址，未必是上面校验域名中记录的 IP 地址）。
            </Message>
            <Form.Group widths={3}>
              <Form.Input
                label='企业 ID'
                name='corp_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.corp_id}
                placeholder='我的企业 -> 企业信息 -> 企业 ID'
              />
              <Form.Input
                label='应用 AgentId'
                name='agent_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.agent_id}
                placeholder='应用管理 -> 自建 -> 创建应用 -> AgentId'
              />
              <Form.Input
                label='应用 Secret'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='应用管理 -> 自建 -> 创建应用 -> Secret'
              />
            </Form.Group>
            <Form.Group widths={3}>
              <Form.Input
                label='用户账号'
                name='account_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.account_id}
                placeholder='通讯录 -> 点击姓名 -> 账号'
              />
              <Form.Select
                label='微信企业号客户端类型'
                name='other'
                options={[
                  {
                    key: 'plugin',
                    text: '微信中的企业微信插件',
                    value: 'plugin',
                  },
                  { key: 'app', text: '企业微信 APP', value: 'app' },
                ]}
                value={inputs.other}
                onChange={handleInputChange}
              />
            </Form.Group>
          </>
        );
      case 'corp':
        return (
          <>
            <Message>
              通过企业微信群机器人进行推送，配置流程：选择一个群聊 -> 设置 ->
              群机器人 -> 添加 -> 新建 -> 输入名字，点击添加 -> 点击复制 Webhook
              地址
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='Webhook 地址'
                name='url'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.url}
                placeholder='在此填写企业微信提供的 Webhook 地址'
              />
            </Form.Group>
          </>
        );
      case 'lark':
        return (
          <>
            <Message>
              通过飞书群机器人进行推送，飞书桌面客户端的配置流程：选择一个群聊
              -> 设置 -> 群机器人 -> 添加机器人 -> 自定义机器人 -> 添加（
              <strong>注意选中「签名校验」</strong>）。具体参见：
              <a
                target='_blank' rel='noreferrer'
                href='https://open.feishu.cn/document/ukTMukTMukTM/ucTM5YjL3ETO24yNxkjN'
              >
                飞书开放文档
              </a>
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='Webhook 地址'
                name='url'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.url}
                placeholder='在此填写飞书提供的 Webhook 地址'
              />
              <Form.Input
                label='签名校验密钥'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='在此填写飞书提供的签名校验密钥'
              />
            </Form.Group>
          </>
        );
      case 'ding':
        return (
          <>
            <Message>
              通过钉钉群机器人进行推送，钉钉桌面客户端的配置流程：选择一个群聊
              -> 群设置 -> 智能群助手 -> 添加机器人（点击右侧齿轮图标） ->
              自定义 -> 添加（
              <strong>注意选中「加密」</strong>）。具体参见：
              <a
                target='_blank' rel='noreferrer'
                href='https://open.dingtalk.com/document/robots/custom-robot-access'
              >
                钉钉开放文档
              </a>
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='Webhook 地址'
                name='url'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.url}
                placeholder='在此填写钉钉提供的 Webhook 地址'
              />
              <Form.Input
                label='签名校验密钥'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='在此填写钉钉提供的签名校验密钥'
              />
            </Form.Group>
          </>
        );
      case 'bark':
        return (
          <>
            <Message>
              通过 Bark 进行推送，下载 Bark 后按提示注册设备，之后会看到一个
              URL，例如 <code>https://api.day.app/wrsVSDRANDOM/Body Text</code>
              ，其中 <code>wrsVSDRANDOM</code> 就是你的推送 key。
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='服务器地址'
                name='url'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.url}
                placeholder='在此填写 Bark 服务器地址，不填则使用默认值'
              />
              <Form.Input
                label='推送 key'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='在此填写 Bark 推送 key'
              />
            </Form.Group>
          </>
        );
      case 'client':
        return (
          <>
            <Message>
              通过 WebSocket
              客户端进行推送，可以使用官方客户端实现，或者根据协议自行实现。官方客户端
              <a
                target='_blank' rel='noreferrer'
                href='https://github.com/songquanpeng/personal-assistant'
              >
                详见此处
              </a>
              。
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='客户端连接密钥'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='在此设置客户端连接密钥'
              />
            </Form.Group>
          </>
        );
      case 'telegram':
        return (
          <>
            <Message>
              通过 Telegram 机器人进行消息推送。首先向
              <a href='https://t.me/botfather' target='_blank' rel='noreferrer'>
                {' '}
                Bot Father{' '}
              </a>
              申请创建一个新的机器人，之后在下方输入获取到的令牌，然后点击你的机器人，随便发送一条消息，之后点击下方的「获取会话
              ID」按钮，系统将自动为你填写会话
              ID，最后点击保存按钮保存设置即可。
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='Telegram 机器人令牌'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='在此设置 Telegram 机器人令牌'
              />
              <Form.Input
                label='Telegram 会话 ID'
                name='account_id'
                type='text'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.account_id}
                placeholder='在此设置 Telegram 会话 ID'
              />
            </Form.Group>
            <Button onClick={getTelegramChatId} loading={loading}>
              获取会话 ID
            </Button>
          </>
        );
      case 'discord':
        return (
          <>
            <Message>
              通过 Discord 群机器人进行推送，配置流程：选择一个 channel -> 设置
              -> 整合 -> 创建 Webhook -> 点击复制 Webhook URL
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='Webhook 地址'
                name='url'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.url}
                placeholder='在此填写 Discord 提供的 Webhook 地址'
              />
            </Form.Group>
          </>
        );
      case 'one_bot':
        return (
          <>
            <Message>
              通过 OneBot 协议进行推送，可以使用{' '}
              <a href='https://github.com/Mrs4s/go-cqhttp' target='_blank' rel='noreferrer'>
                cqhttp
              </a>{' '}
              等实现。 利用 OneBot 协议可以实现推送 QQ 消息。
              <br />
              注意，如果推送目标是群号则前面必须加上群号前缀，例如
              group_123456789。
            </Message>
            <Form.Group widths={3}>
              <Form.Input
                label='服务器地址'
                name='url'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.url}
                placeholder='在此填写服务器地址'
              />
              <Form.Input
                label='推送 key'
                name='secret'
                type='password'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='在此填写服务器的 access token'
              />
              <Form.Input
                label='默认推送目标'
                name='account_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.account_id}
                placeholder='在此填写默认推送目标，例如 QQ 号'
              />
            </Form.Group>
          </>
        );
      case 'qqbot_v2':
        return (
          <>
            <Message info>
              使用 QQ 官方 Bot API v2。App Secret 只会在创建或更换时提交，页面不会回显凭证。普通主动消息是默认模式，不依赖回调事件，但机器人所有者必须允许主动发送，否则平台会拒绝。被动回复需要目标最近的交互事件：官方概述列出 C2C 60 分钟内最多 4 次，但 msg_id 字段文档仍写 5 分钟；本系统暂按 5 分钟保守校验。群聊为 5 分钟内最多回复 5 次。互动召回仅支持 C2C 私聊。
              <p>Token 状态和最近错误诊断当前不可用，管理页不会展示凭证。</p>
            </Message>
            <Form.Group widths='equal'>
              <Form.Input
                label='AppID'
                name='app_id'
                value={inputs.app_id}
                onChange={handleInputChange}
                autoComplete='off'
              />
              <Form.Input
                label='Client Secret'
                name='secret'
                type='password'
                value={inputs.secret}
                onChange={handleInputChange}
                autoComplete='new-password'
                placeholder={isEditing ? '留空保留已保存凭证' : '输入 QQ Bot Client Secret'}
              />
              <Form.Input
                label='机器人名称'
                value={qqConfig.bot_name}
                onChange={(event, { value }) => setQqConfig({ ...qqConfig, bot_name: value })}
                placeholder='用于区分多个 QQ Bot 实例'
              />
              <Form.Input
                label='QQ Bot API 基础地址'
                name='url'
                value={inputs.url}
                onChange={handleInputChange}
                placeholder='留空使用 https://api.bot.qq.com'
              />
            </Form.Group>
            <Form.Group widths='equal'>
              <Form.Input
                label='默认目标 OpenID'
                value={qqConfig.default_target}
                onChange={(event, { value }) => setQqConfig({ ...qqConfig, default_target: value })}
                placeholder='留空时在测试器或请求中选择目标'
              />
              <Form.Select
                label='默认目标类型'
                options={[
                  { key: 'user', value: 'user', text: '私聊用户' },
                  { key: 'group', value: 'group', text: '群聊' },
                ]}
                value={qqConfig.target_type}
                onChange={(event, { value }) => setQqConfig((current) => ({
                  ...current,
                  target_type: value,
                  delivery_mode: value === 'group' && current.delivery_mode === 'wakeup'
                    ? 'active'
                    : current.delivery_mode,
                }))}
              />
              <Form.Select
                label='消息格式'
                options={[
                  { key: 'text', value: 'text', text: '文本' },
                  { key: 'markdown', value: 'markdown', text: 'Markdown' },
                ]}
                value={qqConfig.message_type}
                onChange={(event, { value }) => setQqConfig({ ...qqConfig, message_type: value })}
              />
              <Form.Input
                label='Markdown 模板 ID'
                value={qqConfig.markdown_template_id}
                onChange={(event, { value }) => setQqConfig({ ...qqConfig, markdown_template_id: value })}
              />
            </Form.Group>
            <Form.Group widths='equal'>
              <Form.Select
                label='发送类型'
                options={[
                  { key: 'active', value: 'active', text: '普通主动消息（默认，无需回调）' },
                  { key: 'passive', value: 'passive', text: '被动回复（需近期交互事件）' },
                  { key: 'wakeup', value: 'wakeup', text: '互动召回（仅 C2C 私聊）', disabled: qqConfig.target_type !== 'user' },
                ]}
                value={qqConfig.delivery_mode}
                onChange={(event, { value }) => setQqConfig((current) => ({ ...current, delivery_mode: value }))}
              />
              <Form.Input
                type='number'
                min='1'
                max='60'
                label='请求超时（秒）'
                value={qqConfig.timeout_seconds}
                onChange={(event, { value }) => setQqConfig({ ...qqConfig, timeout_seconds: Number(value) })}
              />
              <Form.Input
                type='number'
                min='1'
                max='5'
                label='Bot 每秒请求上限'
                value={qqConfig.rate_limit_per_second}
                onChange={(event, { value }) => setQqConfig((current) => ({ ...current, rate_limit_per_second: Number(value) }))}
              />
              <Form.Input
                type='number'
                min='1'
                max='30'
                label='Bot 每分钟请求上限'
                value={qqConfig.bot_rate_limit_per_minute}
                onChange={(event, { value }) => setQqConfig((current) => ({ ...current, bot_rate_limit_per_minute: Number(value) }))}
              />
              <Form.Input
                type='number'
                min='1'
                max='20'
                label='单目标每分钟上限'
                value={qqConfig.target_rate_limit_per_minute}
                onChange={(event, { value }) => setQqConfig((current) => ({ ...current, target_rate_limit_per_minute: Number(value) }))}
              />
              <Form.Input
                type='number'
                min='1'
                max='1000'
                label='单目标每天上限'
                value={qqConfig.target_rate_limit_per_day}
                onChange={(event, { value }) => setQqConfig((current) => ({ ...current, target_rate_limit_per_day: Number(value) }))}
              />
            </Form.Group>
            <Form.Group widths='equal'>
              <Form.Input
                type='number'
                min='1'
                max='20'
                label='最大尝试次数（1–20）'
                value={qqConfig.retry_max_attempts}
                onChange={(event, { value }) => setQqConfig({ ...qqConfig, retry_max_attempts: Number(value) })}
              />
              <Form.Input
                label='重试间隔（秒，逗号分隔，最多 6 个）'
                value={Array.isArray(qqConfig.retry_backoff_seconds) ? qqConfig.retry_backoff_seconds.join(',') : ''}
                onChange={(event, { value }) => setQqConfig({
                  ...qqConfig,
                  retry_backoff_seconds: value === ''
                    ? []
                    : value.split(',').map((part) => Number(part.trim())),
                })}
              />
            </Form.Group>
            <Button as={Link} to='/gateway/targets'>管理 OpenID 目标</Button>
            <Button as={Link} to='/gateway/test'>打开消息测试器</Button>
          </>
        );
      case 'group':
        return (
          <>
            <Message>
              对渠道进行分组，然后在推送时选择分组进行推送，可以实现一次性推送到多个渠道的功能。
              <br />
              <br />
              推送目标如若不填，则使用子渠道的默认推送目标。如果填写，请务必全部按顺序填写，对于不需要指定的直接留空即可，例如{' '}
              <code>123456789||@wechat</code>，两个连续的分隔符表示跳过该渠道。
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='渠道列表'
                name='app_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.app_id}
                placeholder='在此填写渠道列表，使用 | 分割，例如 bark|telegram|wechat'
              />
              <Form.Input
                label='默认推送目标'
                name='account_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.account_id}
                placeholder='在此填写默认推送目标，使用 | 分割，例如 123456789|@wechat|@wechat'
              />
            </Form.Group>
          </>
        );
      case 'lark_app':
        return (
          <>
            <Message>
              通过飞书自建应用进行推送，点击前往配置：
              <a target='_blank' rel='noreferrer' href='https://open.feishu.cn/app'>
                飞书开放平台
              </a>
              。
              <br />
              需要为应用添加机器人能力：应用能力->添加应用能力—>机器人。
              <br />
              需要为应用添加消息发送权限：开发配置->权限管理->权限配置->搜索「获取与发送单聊、群组消息」->开通权限。
              <br />
              注意，添加完成权限后需要发布版本提交审核才能见效。
              <br />
              注意，推送目标的格式为：
              <strong>
                <code>类型:ID</code>
              </strong>
              ，详见飞书
              <a
                href='https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/create#bc6d1214'
                target='_blank' rel='noreferrer'
              >
                开发文档
              </a>
              中查询参数一节。
            </Message>
            <Form.Group widths={3}>
              <Form.Input
                label='App ID'
                name='app_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.app_id}
                placeholder='应用凭证 -> App ID'
              />
              <Form.Input
                label='App Secret'
                name='secret'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.secret}
                placeholder='应用凭证 -> App Secret'
              />
              <Form.Input
                label='默认推送目标'
                name='account_id'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.account_id}
                placeholder='格式必须为：<类型>:<ID>，例如 open_id:123456'
              />
            </Form.Group>
          </>
        );
      case 'custom':
        return (
          <>
            <Message>
              自定义推送，目前仅支持 POST 请求，请求体为 JSON 格式。
              <br />
              支持以下模板变量：<code>$title</code>，<code>$description</code>，
              <code>$content</code>，<code>$url</code>，<code>$to</code>。
              <br />
              <a
                href='https://iamazing.cn/page/message-pusher-common-custom-templates'
                target='_blank' rel='noreferrer'
              >
                这个页面
              </a>
              给出了常见的第三方平台的配置实例，你可以参考这些示例进行配置。
              <br />
              注意，为了防止攻击者利用本功能访问内部网络，也为了你的信息安全，请求地址必须使用
              HTTPS 协议。
            </Message>
            <Form.Group widths={2}>
              <Form.Input
                label='请求地址'
                name='url'
                onChange={handleInputChange}
                autoComplete='new-password'
                value={inputs.url}
                placeholder='在此填写完整的请求地址，必须使用 HTTPS 协议'
              />
            </Form.Group>
            <Form.Group widths='equal'>
              <Form.TextArea
                label='请求体'
                placeholder='在此输入请求体，支持模板变量，必须为合法的 JSON 格式'
                value={inputs.other}
                name='other'
                onChange={handleInputChange}
                style={{
                  minHeight: 200,
                  fontFamily: 'JetBrains Mono, Consolas',
                }}
              />
            </Form.Group>
          </>
        );
      case 'none':
        return (
          <>
            <Message>
              仅保存消息，不做推送，可以在 Web
              端查看，需要用户具有消息持久化的权限。
            </Message>
          </>
        );
      default:
        return (
          <>
            <Message>未知通道类型！</Message>
          </>
        );
    }
  };

  return (
    <>
      <Segment loading={loading}>
        <Header as='h3'>{isEditing ? '更新通道配置' : '新建消息通道'}</Header>
        <Form autoComplete='new-password'>
          <Form.Field>
            <Form.Input
              label='名称'
              name='name'
              placeholder={
                '请输入通道名称，请仅使用英文字母和下划线，该名称必须唯一'
              }
              onChange={handleInputChange}
              value={name}
              autoComplete='new-password'
              required
            />
          </Form.Field>
          <Form.Field>
            <Form.Input
              label='备注'
              name='description'
              type={'text'}
              placeholder={'请输入备注信息'}
              onChange={handleInputChange}
              value={description}
              autoComplete='new-password'
            />
          </Form.Field>
          <Form.Select
            label='通道类型'
            name='type'
            options={CHANNEL_OPTIONS}
            value={type}
            onChange={handleInputChange}
          />
          <Form.Input
            label='鉴权令牌'
            name='token'
            onChange={handleInputChange}
            autoComplete='new-password'
            value={inputs.token}
            placeholder='通道维度鉴权令牌，设置后使用该通道推送需要鉴权（使用全局鉴权令牌也可以）'
            action={{
              content: '随机生成',
              onClick: () => {
                setInputs((inputs) => ({
                  ...inputs,
                  token: generateToken(16),
                }));
              },
            }}
          />
          {renderChannelForm()}
          <Button onClick={submit}>
            提交
          </Button>
        </Form>
      </Segment>
    </>
  );
};

export default EditChannel;
