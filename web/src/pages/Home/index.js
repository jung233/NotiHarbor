import React, { useContext, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { Button, Card, Grid, Header, Segment } from 'semantic-ui-react';
import { API, safeHttpURL, showError, showNotice, timestamp2string } from '../../helpers';
import { StatusContext } from '../../context/Status';
import { UserContext } from '../../context/User';

const Home = () => {
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);
  const homePageLink = safeHttpURL(localStorage.getItem('home_page_link') || '');

  const displayNotice = async () => {
    const res = await API.get('/api/notice');
    const { success, message, data } = res.data;
    if (success) {
      let oldNotice = localStorage.getItem('notice');
      if (data !== oldNotice && data !== '') {
        showNotice(data);
        localStorage.setItem('notice', data);
      }
    } else {
      showError(message);
    }
  };

  const getStartTimeString = () => {
    const timestamp = statusState?.status?.start_time;
    return timestamp2string(timestamp);
  };

  useEffect(() => {
    if (userState.sessionReady && userState.user) {
      displayNotice().catch(showError);
    }
  }, [userState.sessionReady, userState.user]);

  if (!userState.user) {
    return (
      <Segment textAlign='center' style={{ marginTop: '2em', padding: '3em 1em' }}>
        <Header as='h1'>NotiHarbor 讯港</Header>
        <p>
          <strong>A Reliable, Self-Hosted Notification Gateway</strong>
          {' / 可靠、自托管的统一消息通知网关'}
        </p>
        <p>登录后管理消息、通道和通知接口。</p>
        <Button as={Link} to='/login' primary>
          登录
        </Button>
        <Button as={Link} to='/register'>
          注册
        </Button>
      </Segment>
    );
  }

  return (
    <>
      {homePageLink !== '' ? (
        <>
          <iframe
            title='自定义首页'
            src={homePageLink}
            style={{ width: '100%', height: '100vh', border: 'none' }}
          />
        </>
      ) : (
        <>
          <Segment>
            <Header as='h2'>NotiHarbor 讯港</Header>
            <p>
              <strong>A Reliable, Self-Hosted Notification Gateway</strong>
              {' / 可靠、自托管的统一消息通知网关'}
            </p>
          </Segment>
          <Segment>
            <Header as='h3'>系统状况</Header>
            <Grid columns={2} stackable>
              <Grid.Column>
                <Card fluid>
                  <Card.Content>
                    <Card.Header>系统信息</Card.Header>
                    <Card.Meta>系统信息总览</Card.Meta>
                    <Card.Description>
                      <p>名称：{statusState?.status?.system_name}</p>
                      <p>版本：{statusState?.status?.version}</p>
                      <p>
                        项目源码：
                        <a
                          href='https://github.com/jung233/NotiHarbor'
                          target='_blank' rel='noreferrer'
                        >
                          NotiHarbor / 讯港
                        </a>
                        {' · '}
                        <a href='https://github.com/jung233/NotiHarbor/issues' target='_blank' rel='noreferrer'>问题反馈</a>
                        {' · '}
                        <a href='https://github.com/jung233/NotiHarbor/tree/main/docs' target='_blank' rel='noreferrer'>文档</a>
                      </p>
                      <p>启动时间：{getStartTimeString()}</p>
                      <p>自从上次启动已发送消息数目：{statusState?.status?.message_count}</p>
                      <p>自从上次启动新注册用户数目：{statusState?.status?.user_count}</p>
                    </Card.Description>
                  </Card.Content>
                </Card>
              </Grid.Column>
              <Grid.Column>
                <Card fluid>
                  <Card.Content>
                    <Card.Header>系统配置</Card.Header>
                    <Card.Meta>系统配置总览</Card.Meta>
                    <Card.Description>
                      <p>
                        邮箱验证：
                        {statusState?.status?.email_verification === true
                          ? '已启用'
                          : '未启用'}
                      </p>
                      <p>
                        GitHub 身份验证：
                        {statusState?.status?.github_oauth === true
                          ? '已启用'
                          : '未启用'}
                      </p>
                      <p>
                        微信身份验证：
                        {statusState?.status?.wechat_login === true
                          ? '已启用'
                          : '未启用'}
                      </p>
                      <p>
                        Turnstile 用户校验：
                        {statusState?.status?.turnstile_check === true
                          ? '已启用'
                          : '未启用'}
                      </p>
                      <p>
                        全局消息持久化：
                        {statusState?.status?.message_persistence === true
                          ? '已启用'
                          : '未启用'}
                      </p>
                      <p>
                        全局消息渲染：
                        {statusState?.status?.message_render === true
                          ? '已启用'
                          : '未启用'}
                      </p>
                    </Card.Description>
                  </Card.Content>
                </Card>
              </Grid.Column>
            </Grid>
          </Segment>
        </>
      )}
    </>
  );
};

export default Home;
