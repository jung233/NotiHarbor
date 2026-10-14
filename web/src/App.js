import React, { lazy, Suspense, useContext, useEffect } from 'react';
import { Route, Routes } from 'react-router-dom';
import Loading from './components/Loading';
import User from './pages/User';
import { PrivateRoute } from './components/PrivateRoute';
import RegisterForm from './components/RegisterForm';
import LoginForm from './components/LoginForm';
import NotFound from './pages/NotFound';
import Setting from './pages/Setting';
import EditUser from './pages/User/EditUser';
import AddUser from './pages/User/AddUser';
import { API, showError, showNotice } from './helpers';
import PasswordResetForm from './components/PasswordResetForm';
import GitHubOAuth from './components/GitHubOAuth';
import PasswordResetConfirm from './components/PasswordResetConfirm';
import { UserContext } from './context/User';
import { StatusContext } from './context/Status';
import Message from './pages/Message';
import Channel from './pages/Channel';
import EditChannel from './pages/Channel/EditChannel';
import EditMessage from './pages/Message/EditMessage';
import Webhook from './pages/Webhook';
import EditWebhook from './pages/Webhook/EditWebhook';
import AdminRoute from './components/AdminRoute';
import { restoreUserSession } from './helpers/session';
import GatewayDashboard from './pages/Gateway/Dashboard';
import GatewayHistory from './pages/Gateway/History';
import GatewayNotificationDetail from './pages/Gateway/NotificationDetail';
import GatewayTester from './pages/Gateway/Tester';
import GatewaySources from './pages/Gateway/Sources';
import GatewayTargets from './pages/Gateway/Targets';
import GatewayLogs from './pages/Gateway/Logs';

const Home = lazy(() => import('./pages/Home'));
const About = lazy(() => import('./pages/About'));

function App() {
  const [, userDispatch] = useContext(UserContext);
  const [, statusDispatch] = useContext(StatusContext);

  const loadStatus = async () => {
    try {
      const res = await API.get('/api/status');
      const { success, data } = res.data;
      if (success) {
        localStorage.setItem('status', JSON.stringify(data));
        statusDispatch({ type: 'set', payload: data });
        localStorage.setItem('footer_html', data.footer_html || '');
        localStorage.setItem('home_page_link', data.home_page_link || '');
        if (
          data.version !== process.env.REACT_APP_VERSION &&
          data.version !== 'v0.0.0' &&
          process.env.REACT_APP_VERSION !== ''
        ) {
          showNotice(
            `新版本可用：${data.version}，请使用快捷键 Shift + F5 刷新页面`
          );
        }
      } else {
        showError('无法正常连接至服务器！');
      }
    } catch (error) {
      showError(error);
    }
  };

  useEffect(() => {
    const handleSessionExpired = () => {
      localStorage.removeItem('user');
      userDispatch({ type: 'logout' });
    };
    window.addEventListener('message-pusher-session-expired', handleSessionExpired);
    restoreUserSession(API, userDispatch);
    loadStatus().then();
    return () => {
      window.removeEventListener(
        'message-pusher-session-expired',
        handleSessionExpired
      );
    };
    // Session restoration and status loading should run once when App mounts.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <Routes>
      <Route
        path='/'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <Home />
          </Suspense>
        }
      />
      <Route
        path='/user'
        element={
          <PrivateRoute>
            <User />
          </PrivateRoute>
        }
      />
      <Route
        path='/user/edit/:id'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <EditUser />
          </Suspense>
        }
      />
      <Route
        path='/user/edit'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <EditUser />
          </Suspense>
        }
      />
      <Route
        path='/user/add'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <AddUser />
          </Suspense>
        }
      />
      <Route
        path='/user/reset'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <PasswordResetConfirm />
          </Suspense>
        }
      />
      <Route
        path='/channel'
        element={
          <PrivateRoute>
            <Channel />
          </PrivateRoute>
        }
      />
      <Route
        path='/channel/edit/:id'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <EditChannel />
          </Suspense>
        }
      />
      <Route
        path='/channel/add'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <EditChannel />
          </Suspense>
        }
      />
      <Route
        path='/webhook'
        element={
          <PrivateRoute>
            <Webhook />
          </PrivateRoute>
        }
      />
      <Route
        path='/webhook/edit/:id'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <EditWebhook />
          </Suspense>
        }
      />
      <Route
        path='/webhook/add'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <EditWebhook />
          </Suspense>
        }
      />
      <Route
        path='/message'
        element={
          <PrivateRoute>
            <Message />
          </PrivateRoute>
        }
      />
      <Route
        path='/gateway'
        element={<AdminRoute><GatewayDashboard /></AdminRoute>}
      />
      <Route
        path='/gateway/history'
        element={<AdminRoute><GatewayHistory /></AdminRoute>}
      />
      <Route
        path='/gateway/history/:id'
        element={<AdminRoute><GatewayNotificationDetail /></AdminRoute>}
      />
      <Route
        path='/gateway/logs'
        element={<AdminRoute><GatewayLogs /></AdminRoute>}
      />
      <Route
        path='/gateway/test'
        element={<AdminRoute><GatewayTester /></AdminRoute>}
      />
      <Route
        path='/gateway/sources'
        element={<AdminRoute><GatewaySources /></AdminRoute>}
      />
      <Route
        path='/gateway/targets'
        element={<AdminRoute><GatewayTargets /></AdminRoute>}
      />
      <Route
        path='/editor'
        element={
          <PrivateRoute>
            <EditMessage />
          </PrivateRoute>
        }
      />
      <Route
        path='/editor/:id'
        element={
          <PrivateRoute>
            <EditMessage />
          </PrivateRoute>
        }
      />
      <Route
        path='/login'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <LoginForm />
          </Suspense>
        }
      />
      <Route
        path='/register'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <RegisterForm />
          </Suspense>
        }
      />
      <Route
        path='/reset'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <PasswordResetForm />
          </Suspense>
        }
      />
      <Route
        path='/oauth/github'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <GitHubOAuth />
          </Suspense>
        }
      />
      <Route
        path='/setting'
        element={
          <PrivateRoute>
            <Suspense fallback={<Loading></Loading>}>
              <Setting />
            </Suspense>
          </PrivateRoute>
        }
      />
      <Route
        path='/about'
        element={
          <Suspense fallback={<Loading></Loading>}>
            <About />
          </Suspense>
        }
      />
      <Route path='*' element={NotFound} />
    </Routes>
  );
}

export default App;
