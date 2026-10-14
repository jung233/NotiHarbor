const SELF_ENDPOINT = '/api/user/self';
const RESTORE_REQUEST_OPTIONS = {
  suppressUnauthorizedRedirect: true,
  suppressErrorToast: true,
};

export function expireSessionOnUnauthorized(
  error,
  storage = localStorage,
  notifySessionExpired = () => window.dispatchEvent(new Event('message-pusher-session-expired'))
) {
  const requestUrl = error?.config?.url || '';
  const isAuthenticationRequest =
    requestUrl.includes('/api/user/login') ||
    requestUrl.includes('/api/user/register') ||
    requestUrl.includes('/api/oauth/');

  if (
    error?.response?.status !== 401 ||
    isAuthenticationRequest ||
    error.config?.suppressUnauthorizedRedirect
  ) {
    return false;
  }

  storage.removeItem('user');
  notifySessionExpired();
  return true;
}

export async function restoreUserSession(api, userDispatch, storage = localStorage) {
  let restored = false;

  try {
    const response = await api.get(SELF_ENDPOINT, RESTORE_REQUEST_OPTIONS);
    const user = response.data?.success ? response.data.data : null;
    if (user) {
      storage.setItem('user', JSON.stringify(user));
      userDispatch({ type: 'login', payload: user });
      restored = true;
    }
  } catch (error) {
    // A missing or expired session is a normal signed-out state.
  } finally {
    if (!restored) {
      storage.removeItem('user');
      userDispatch({ type: 'logout' });
    }
    userDispatch({ type: 'session-ready' });
  }

  return restored;
}
