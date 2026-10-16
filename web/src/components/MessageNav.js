import React from 'react';
import { Link, useLocation } from 'react-router-dom';
import { Menu } from 'semantic-ui-react';

const MessageNav = () => {
  const { pathname } = useLocation();
  const logs = pathname === '/message/delivery/logs';
  return (
    <Menu pointing secondary stackable>
      <Menu.Item as={Link} to='/message' active={!logs}>全部消息</Menu.Item>
      <Menu.Item as={Link} to='/message/delivery/logs' active={logs}>投递日志</Menu.Item>
    </Menu>
  );
};

export default MessageNav;
