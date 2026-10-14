import React, { useEffect, useState } from 'react';

import { Container, Segment } from 'semantic-ui-react';
import { sanitizeHTML } from '../helpers';

const Footer = () => {
  const [Footer, setFooter] = useState('');
  useEffect(() => {
    let savedFooter = localStorage.getItem('footer_html');
    if (!savedFooter) savedFooter = '';
    setFooter(savedFooter);
  }, []);

  return (
    <Segment vertical>
      <Container textAlign='center'>
        {Footer === '' ? (
          <div className='custom-footer'>
            <div>
              <a href='https://github.com/jung233/NotiHarbor' target='_blank' rel='noreferrer'>
                NotiHarbor 讯港 {process.env.REACT_APP_VERSION}
              </a>
              {' · '}
              <a href='https://github.com/jung233/NotiHarbor/issues' target='_blank' rel='noreferrer'>
                问题反馈
              </a>
              {' · '}
              <a href='https://github.com/jung233/NotiHarbor/tree/main/docs' target='_blank' rel='noreferrer'>
                文档
              </a>
            </div>
            <div>A Reliable, Self-Hosted Notification Gateway / 可靠、自托管的统一消息通知网关</div>
            <div>
              NotiHarbor 基于 JustSong 的{' '}
              <a href='https://github.com/songquanpeng/message-pusher' target='_blank' rel='noreferrer'>
                Message Pusher
              </a>{' '}
              开源项目构建，源代码遵循{' '}
              <a href='https://opensource.org/licenses/mit-license.php' target='_blank' rel='noreferrer'>
                MIT 协议
              </a>
              。感谢{' '}
              <a href='https://github.com/songquanpeng' target='_blank' rel='noreferrer'>
                JustSong
              </a>{' '}
              与原项目贡献者。
            </div>
          </div>
        ) : (
          <div
            className='custom-footer'
            dangerouslySetInnerHTML={{ __html: sanitizeHTML(Footer) }}
          ></div>
        )}
      </Container>
    </Segment>
  );
};

export default Footer;
