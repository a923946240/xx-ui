import { afterEach, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import NATSyncStatusButton from '@/pages/xray/outbounds/NATSyncStatusButton';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

const config = {
  enabled: false,
  relayNodeId: 0,
  reporterNodeId: 0,
  outboundTags: ['taiwan'],
};
const nodes = [
  { id: 2, name: 'Hong Kong', enable: true, scheme: 'https' },
  { id: 3, name: 'Taiwan', enable: true, scheme: 'https', tlsVerifyMode: 'pin' },
  { id: 4, name: 'Disabled node', enable: false, scheme: 'https' },
  { id: 5, name: 'HTTP node', enable: true, scheme: 'http' },
  { id: 6, name: 'Unverified node', enable: true, scheme: 'https', tlsVerifyMode: 'skip' },
  { id: 7, name: 'Sub-node', enable: true, scheme: 'https', transitive: true },
];

function mockView(saved = config, availableNodes = nodes) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (path) => ({
    success: true,
    msg: '',
    obj: path.endsWith('/nodes/list') ? availableNodes : { config: saved },
  }));
}

function renderButton() {
  return renderWithProviders(
    <MemoryRouter>
      <NATSyncStatusButton outboundTags={['taiwan']} />
    </MemoryRouter>,
  );
}

function chooseNode(control: HTMLElement, label: string) {
  const selector = control.closest('.ant-select');
  if (!selector) throw new Error('Node selector is missing');
  fireEvent.mouseDown(selector.querySelector('.ant-select-selector') ?? selector);
  const listId = control.getAttribute('aria-controls');
  const dropdown = listId && document.getElementById(listId)?.closest('.ant-select-dropdown');
  if (!dropdown) throw new Error('Node dropdown is missing');
  const options = Array.from(dropdown.querySelectorAll('.ant-select-item-option'));
  const labels = options.map((item) => item.textContent?.trim());
  const option = options.find((item) => item.textContent?.trim() === label);
  if (!option) throw new Error('Node option is missing: ' + label);
  fireEvent.click(option);
  return labels;
}

afterEach(() => vi.restoreAllMocks());

it('tests the selected node pair and displays its observed IP before enabling sync', async () => {
  mockView();
  const selected = { ...config, relayNodeId: 2, reporterNodeId: 3, enabled: true };
  const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (path) => ({
    success: true,
    msg: '',
    obj: path.endsWith('/test')
      ? { candidateIp: '1.1.1.1', seenAt: 1800000000 }
      : { config: selected },
  }));

  renderButton();
  fireEvent.click(screen.getByRole('button', { name: 'NAT Sync' }));
  const relay = await screen.findByRole<HTMLInputElement>('combobox', { name: 'Receiving node' });
  const reporter = screen.getByRole('combobox', { name: 'Reporting node' });
  await waitFor(() => expect(relay.disabled).toBe(false));
  expect(chooseNode(relay, 'Hong Kong')).toEqual(['Hong Kong', 'Taiwan']);
  chooseNode(reporter, 'Taiwan');
  fireEvent.click(screen.getByRole('switch', { name: 'Automatic sync' }));
  const save = screen.getByRole<HTMLButtonElement>('button', { name: 'Save' });
  expect(save.disabled).toBe(true);

  fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
  await screen.findByText(/1\.1\.1\.1/);
  expect(post).toHaveBeenCalledWith(
    '/panel/api/xray/nat-sync/test',
    selected,
    expect.objectContaining({ headers: { 'Content-Type': 'application/json' } }),
  );
  expect(save.disabled).toBe(false);
  fireEvent.click(save);
  await waitFor(() => {
    expect(post).toHaveBeenCalledWith(
      '/panel/api/xray/nat-sync/config',
      selected,
      expect.objectContaining({ headers: { 'Content-Type': 'application/json' } }),
    );
  });
});

it('can disable sync after its receiving node is removed', async () => {
  const saved = { ...config, relayNodeId: 2, reporterNodeId: 3, enabled: true };
  mockView(saved, [nodes[1]]);
  const stopped = { ...saved, enabled: false };
  const post = vi
    .spyOn(HttpUtil, 'post')
    .mockResolvedValue({ success: true, msg: '', obj: { config: stopped } });

  renderButton();
  fireEvent.click(screen.getByRole('button', { name: 'NAT Sync' }));
  fireEvent.click(await screen.findByRole('switch', { name: 'Automatic sync' }));
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/panel/api/xray/nat-sync/config',
      stopped,
      expect.anything(),
    ),
  );
});
