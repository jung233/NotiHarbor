import React, { useEffect, useState } from 'react';
import { Header, Segment } from 'semantic-ui-react';
import { API, sanitizeHTML, showError } from '../../helpers';
import { marked } from 'marked';

const About = () => {
  const [about, setAbout] = useState('');
  const [aboutLoaded, setAboutLoaded] = useState(false);

  const displayAbout = async () => {
    setAbout(localStorage.getItem('about') || '');
    const res = await API.get('/api/about');
    const { success, message, data } = res.data;
    if (success) {
      let HTMLAbout = marked.parse(data);
      setAbout(HTMLAbout);
      localStorage.setItem('about', HTMLAbout);
    } else {
      showError(message);
      setAbout('加载关于内容失败...');
    }
    setAboutLoaded(true);
  };

  useEffect(() => {
    displayAbout().then();
  }, []);

  return (
    <>
      <Segment>
        <Header as='h2'>NotiHarbor 讯港</Header>
        <p><strong>A Reliable, Self-Hosted Notification Gateway</strong></p>
        <p>可靠、自托管的统一消息通知网关。</p>
        <p>
          <a href='https://github.com/jung233/NotiHarbor' target='_blank' rel='noreferrer'>项目仓库</a>
          {' · '}
          <a href='https://github.com/jung233/NotiHarbor/issues' target='_blank' rel='noreferrer'>问题反馈</a>
          {' · '}
          <a href='https://github.com/jung233/NotiHarbor/tree/main/docs' target='_blank' rel='noreferrer'>文档</a>
        </p>
      </Segment>
      <Segment>
        {aboutLoaded && about === '' ? (
          <>
            <Header as='h3'>关于 NotiHarbor</Header>
            <p>NotiHarbor 基于 JustSong 的 Message Pusher 开源项目构建，源代码遵循 MIT 协议。</p>
            <p>可在设置页面设置关于内容，支持 HTML & Markdown。</p>
          </>
        ) : (
          <>
            <div dangerouslySetInnerHTML={{ __html: sanitizeHTML(about) }}></div>
          </>
        )}
      </Segment>
    </>
  );
};


export default About;
