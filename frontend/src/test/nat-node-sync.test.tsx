import { afterEach, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import NATSyncStatusButton, { NATSyncWizard } from '@/pages/xray/outbounds/NATSyncStatusButton';
import NATReportingModal from '@/pages/nodes/NATReportingModal';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

const config = {
  enabled: false,
  relayNodeId: 0,
  reporterNodeId: 0,
  outboundTags: ['taiwan'],
};
const nodes: NodeRecord[] = [
  { id: 2, name: 'Hong Kong', enable: true, scheme: 'https' },
  { id: 3, name: 'Taiwan', enable: true, scheme: 'https', tlsVerifyMode: 'pin' },
  { id: 4, name: 'Disabled node', enable: false, scheme: 'https' },
  { id: 5, name: 'HTTP node', enable: true, scheme: 'http' },
  { id: 6, name: 'Unverified node', enable: true, scheme: 'https', tlsVerifyMode: 'skip' },
  { id: 7, name: 'Sub-node', enable: true, scheme: 'https', transitive: true },
];
const pooled = {
  id: 'a'.repeat(64),
  name: 'Taiwan WAN',
  candidateIp: '1.1.1.1',
  seenAt: 1800000000,
};

function mockView(saved = config, availableNodes = nodes) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (path) => ({
    success: true,
    msg: '',
    obj: path.endsWith('/nodes/list')
      ? availableNodes
      : path.endsWith('/pool')
        ? [pooled]
        : { config: saved },
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

it('reads Hong Kong’s pool and enables sync without a Taiwan node on the front panel', async () => {
  mockView(
    config,
    nodes.filter((node) => node.id !== 3),
  );
  const selected = { ...config, relayNodeId: 2, poolEntryId: pooled.id, enabled: true };
  const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (path) => ({
    success: true,
    msg: '',
    obj: path.endsWith('/wizard')
      ? { state: 'ready', enabled: true, candidateIp: '1.1.1.1', seenAt: 1800000000, checks: [] }
      : undefined,
  }));

  renderButton();
  fireEvent.click(screen.getByRole('button', { name: 'NAT sync wizard' }));
  const relay = await screen.findByRole<HTMLInputElement>('combobox', {
    name: 'Address pool node',
  });
  await waitFor(() => expect(relay.disabled).toBe(false));
  expect(chooseNode(relay, 'Hong Kong')).toEqual(['Hong Kong']);
  const address = await screen.findByRole<HTMLInputElement>('combobox', {
    name: 'Reported address',
  });
  await waitFor(() => expect(address.disabled).toBe(false));
  chooseNode(address, 'Taiwan WAN · 1.1.1.1');
  fireEvent.click(screen.getByRole('button', { name: 'Check and enable' }));
  await screen.findByText('Automatic sync is enabled.');
  expect(post).toHaveBeenCalledWith(
    '/panel/api/xray/nat-sync/wizard',
    selected,
    expect.objectContaining({ headers: { 'Content-Type': 'application/json' } }),
  );
  expect(post).toHaveBeenCalledTimes(1);
});

it('starts reporting to the only eligible receiving node and shows the observed IP', async () => {
  vi.spyOn(HttpUtil, 'get').mockResolvedValue({
    success: true,
    msg: '',
    obj: { config: { relayNodeId: 0, name: '', enabled: false }, failed: false },
  });
  const requested = { relayNodeId: 2, name: 'Taiwan WAN', enabled: true };
  const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue({
    success: true,
    msg: '',
    obj: { config: requested, failed: false, lastReport: pooled },
  });
  renderWithProviders(
    <NATReportingModal nodes={[nodes[0]]} onAddNode={() => {}} onClose={() => {}} />,
  );
  fireEvent.change(await screen.findByRole('textbox', { name: 'Name in the pool' }), {
    target: { value: requested.name },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Start reporting' }));
  await screen.findByText('1.1.1.1', { exact: true });
  expect(post).toHaveBeenCalledWith('/panel/api/nodes/nat/reporting', requested, expect.anything());
});

it('prefills the outbound from its row and displays a failed dial separately from the IP report', async () => {
  const saved = { ...config, relayNodeId: 2, reporterNodeId: 0, outboundTags: [] };
  mockView(saved);
  const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue({
    success: true,
    msg: '',
    obj: {
      state: 'probe_error',
      enabled: false,
      candidateIp: '1.1.1.1',
      seenAt: 1800000000,
      checks: [{ tag: 'taiwan', port: 35818, currentAddress: '8.8.8.8', state: 'failed' }],
    },
  });
  renderWithProviders(
    <MemoryRouter>
      <NATSyncWizard outboundTags={['taiwan']} initialTag="taiwan" onClose={() => {}} />
    </MemoryRouter>,
  );
  const address = await screen.findByRole<HTMLInputElement>('combobox', {
    name: 'Reported address',
  });
  await waitFor(() => expect(address.disabled).toBe(false));
  chooseNode(address, 'Taiwan WAN · 1.1.1.1');
  const preview = screen.getByRole<HTMLButtonElement>('button', {
    name: 'Check and enable',
  });
  await waitFor(() => expect(preview.disabled).toBe(false));
  fireEvent.click(preview);
  expect(await screen.findByText(/1\.1\.1\.1:35818/)).toBeTruthy();
  expect(screen.getByText(/8\.8\.8\.8:35818/)).toBeTruthy();
  expect(screen.getByText('Received reported IP 1.1.1.1')).toBeTruthy();
  expect(screen.getByText(/reported IP failed the proxy test/)).toBeTruthy();
  expect(post).toHaveBeenCalledWith(
    '/panel/api/xray/nat-sync/wizard',
    { ...saved, enabled: true, poolEntryId: pooled.id, outboundTags: ['taiwan'] },
    expect.anything(),
  );
  expect(post).toHaveBeenCalledTimes(1);
});

it('can disable sync after its receiving node is removed', async () => {
  const saved = { ...config, relayNodeId: 2, reporterNodeId: 3, enabled: true };
  mockView(saved, [nodes[1]]);
  const stopped = { ...saved, enabled: false };
  const post = vi
    .spyOn(HttpUtil, 'post')
    .mockResolvedValue({ success: true, msg: '', obj: { config: stopped } });

  renderButton();
  fireEvent.click(screen.getByRole('button', { name: 'NAT sync wizard' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Disable automatic sync' }));
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/panel/api/xray/nat-sync/config',
      stopped,
      expect.anything(),
    ),
  );
});
