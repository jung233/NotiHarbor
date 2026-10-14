import React from 'react';
import { Link, useLocation } from 'react-router-dom';
import { Menu } from 'semantic-ui-react';

const links = [
  { to: '/gateway', name: '概览' },
  { to: '/gateway/history', name: '消息历史' },
  { to: '/gateway/logs', name: '发送日志' },
  { to: '/gateway/test', name: '消息测试' },
  { to: '/gateway/sources', name: '应用与 API Key' },
  { to: '/gateway/targets', name: 'QQ Bot 目标' },
];

const GatewayNav = () => {
  const location = useLocation();
  return (
    <Menu pointing secondary stackable>
      {links.map((link) => (
        <Menu.Item
          key={link.to}
          as={Link}
          to={link.to}
          active={
            link.to === '/gateway'
              ? location.pathname === link.to
              : location.pathname.startsWith(link.to)
          }
        >
          {link.name}
        </Menu.Item>
      ))}
    </Menu>
  );
};

export default GatewayNav;
