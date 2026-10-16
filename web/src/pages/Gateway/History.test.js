import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import History from './History';
import { API } from '../../helpers';

jest.mock('../../helpers', () => ({
  API: { get: jest.fn(), post: jest.fn() },
  showError: jest.fn(), showSuccess: jest.fn(),
}));

let container;
let root;
beforeEach(() => {
  global.IS_REACT_ACT_ENVIRONMENT = true;
  jest.useFakeTimers();
  Object.defineProperty(document, 'hidden', { configurable: true, value: false });
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
  API.get.mockReset();
});
afterEach(() => {
  if (root) act(() => root.unmount());
  container.remove();
  jest.useRealTimers();
});
const result = (items) => ({ data: { success: true, data: { items, total: items.length, page: 1 } } });
const render = async () => {
  // ReactDOM rendering needs act; this test does not use Testing Library.
  // eslint-disable-next-line testing-library/no-unnecessary-act
  await act(async () => root.render(<MemoryRouter initialEntries={['/message']}><History /></MemoryRouter>));
};

it('shows webhook and historical messages together and only enables sending actions for gateway records', async () => {
  API.get.mockResolvedValue(result([
    { id: 'webhook-id', title: 'Webhook 通知', status: 'accepted' },
    { id: 'legacy-archive:7', title: '历史通知', status: 'succeeded' },
  ]));
  await render();
  expect(container.textContent).toContain('Webhook 通知');
  expect(container.textContent).toContain('历史通知');
  expect(container.querySelector('a[href="/message/detail/webhook-id"]')).not.toBeNull();
  const archivedRow = [...container.querySelectorAll('tbody tr')].find((row) => row.textContent.includes('历史通知'));
  expect(archivedRow.querySelector('input').disabled).toBe(true);
  expect(archivedRow.textContent).not.toContain('再次发送');
  expect(container.querySelector('a[href="/message/delivery/logs"]')).not.toBeNull();
  expect(API.get).toHaveBeenCalledWith('/api/v1/admin/notifications', expect.any(Object));
});

it('refreshes webhook submissions without a legacy SSE stream and stops polling when unmounted', async () => {
  API.get.mockResolvedValueOnce(result([])).mockResolvedValue(result([
    { id: 'new-id', title: '新 Webhook', status: 'accepted' },
  ]));
  await render();
  await act(async () => jest.advanceTimersByTime(10000));
  expect(container.textContent).toContain('新 Webhook');
  expect(API.get).toHaveBeenCalledTimes(2);
  Object.defineProperty(document, 'hidden', { configurable: true, value: true });
  await act(async () => jest.advanceTimersByTime(10000));
  expect(API.get).toHaveBeenCalledTimes(2);
  act(() => root.unmount());
  root = null;
  await act(async () => jest.advanceTimersByTime(10000));
  expect(API.get).toHaveBeenCalledTimes(2);
});
