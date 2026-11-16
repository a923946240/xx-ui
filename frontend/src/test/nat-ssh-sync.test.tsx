import { afterEach, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import NATSyncStatusButton from '@/pages/xray/outbounds/NATSyncStatusButton';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

afterEach(() => vi.restoreAllMocks());

it('tests the SSH relay before enabling automatic sync from the modal', async () => {
  const config = {
    enabled: false,
    relayHost: 'hk.example.test',
    relayPort: 22,
    relayUser: 'nat-sync',
    hostKeyFingerprint: 'SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA',
    outboundTags: ['taiwan'],
  };
  const view = {
    config,
    publicKey: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA',
  };
  vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, msg: '', obj: view });
  const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (path) => ({
    success: true,
    msg: '',
    obj: path.endsWith('/test')
      ? { candidateIp: '1.1.1.1', seenAt: 1800000000 }
      : { ...view, config: { ...config, enabled: true } },
  }));

  renderWithProviders(<NATSyncStatusButton outboundTags={['taiwan']} />);
  fireEvent.click(screen.getByRole('button', { name: 'NAT SSH Sync' }));
  fireEvent.click(await screen.findByRole('switch', { name: 'Automatic sync' }));
  const save = screen.getByRole<HTMLButtonElement>('button', { name: 'Save' });
  expect(save.disabled).toBe(true);

  fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));
  await screen.findByText(/1\.1\.1\.1/);
  expect(save.disabled).toBe(false);
  fireEvent.click(save);
  await waitFor(() => {
    expect(post).toHaveBeenCalledWith(
      '/panel/api/xray/nat-sync/config',
      { ...config, enabled: true },
      expect.objectContaining({ headers: { 'Content-Type': 'application/json' } }),
    );
  });
});
