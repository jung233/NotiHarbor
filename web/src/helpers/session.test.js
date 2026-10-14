import { expireSessionOnUnauthorized, restoreUserSession } from './session';
import { initialState, reducer } from '../context/User/reducer';

describe('restoreUserSession', () => {
  const storageStub = () => ({
    setItem: jest.fn(),
    removeItem: jest.fn(),
  });

  it('clears an expired cached session with one self request and unlocks signed-out routes', async () => {
    const api = {
      get: jest.fn().mockRejectedValue({ response: { status: 401 } }),
    };
    const storage = storageStub();
    const dispatch = jest.fn();

    const restored = await restoreUserSession(api, dispatch, storage);

    expect(restored).toBe(false);
    expect(api.get).toHaveBeenCalledTimes(1);
    expect(api.get).toHaveBeenCalledWith('/api/user/self', {
      suppressUnauthorizedRedirect: true,
      suppressErrorToast: true,
    });
    expect(storage.removeItem).toHaveBeenCalledWith('user');
    expect(storage.setItem).not.toHaveBeenCalled();
    expect(dispatch.mock.calls).toEqual([
      [{ type: 'logout' }],
      [{ type: 'session-ready' }],
    ]);

    const expiredState = reducer(
      reducer({ ...initialState, user: { id: 7, role: 10 } }, { type: 'logout' }),
      { type: 'session-ready' }
    );
    expect(expiredState).toEqual({ user: undefined, sessionReady: true });
  });

  it('restores a verified session and persists only the server response', async () => {
    const user = { id: 12, username: 'verified-user', role: 10 };
    const api = {
      get: jest.fn().mockResolvedValue({ data: { success: true, data: user } }),
    };
    const storage = storageStub();
    const dispatch = jest.fn();

    const restored = await restoreUserSession(api, dispatch, storage);

    expect(restored).toBe(true);
    expect(api.get).toHaveBeenCalledTimes(1);
    expect(storage.setItem).toHaveBeenCalledWith('user', JSON.stringify(user));
    expect(storage.removeItem).not.toHaveBeenCalled();
    expect(dispatch.mock.calls).toEqual([
      [{ type: 'login', payload: user }],
      [{ type: 'session-ready' }],
    ]);
  });

  it('clears cached identity when the self endpoint returns an unsuccessful envelope', async () => {
    const api = {
      get: jest.fn().mockResolvedValue({ data: { success: false, data: null } }),
    };
    const storage = storageStub();
    const dispatch = jest.fn();

    await restoreUserSession(api, dispatch, storage);

    expect(storage.removeItem).toHaveBeenCalledWith('user');
    expect(dispatch).toHaveBeenCalledWith({ type: 'logout' });
    expect(dispatch).toHaveBeenCalledWith({ type: 'session-ready' });
  });
});

describe('expireSessionOnUnauthorized', () => {
  it('clears a protected session and notifies the app once for route handling', () => {
    const storage = { removeItem: jest.fn() };
    let routeState = { ...initialState, user: { id: 8, role: 10 }, sessionReady: true };
    const notify = jest.fn(() => {
      routeState = reducer(routeState, { type: 'logout' });
    });
    const error = {
      config: { url: '/api/v1/admin/dashboard' },
      response: { status: 401 },
    };

    expect(expireSessionOnUnauthorized(error, storage, notify)).toBe(true);

    expect(storage.removeItem).toHaveBeenCalledTimes(1);
    expect(storage.removeItem).toHaveBeenCalledWith('user');
    expect(notify).toHaveBeenCalledTimes(1);
    expect(routeState).toEqual({ user: undefined, sessionReady: true });
  });

  it.each([
    ['/api/user/login', false],
    ['/api/user/register', false],
    ['/api/oauth/github', false],
    ['/api/user/self', true],
  ])('does not expire on authentication or intentionally suppressed requests (%s)', (url, suppressed) => {
    const storage = { removeItem: jest.fn() };
    const notify = jest.fn();
    const error = {
      config: { url, suppressUnauthorizedRedirect: suppressed },
      response: { status: 401 },
    };

    expect(expireSessionOnUnauthorized(error, storage, notify)).toBe(false);
    expect(storage.removeItem).not.toHaveBeenCalled();
    expect(notify).not.toHaveBeenCalled();
  });
});
