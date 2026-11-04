import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, within, waitFor } from '@testing-library/react';
import { HttpUtil, Msg } from '@/utils';
import ExternalOutbounds from '@/pages/xray/outbounds/ExternalOutbounds';
import { makeTestQueryClient, renderWithProviders } from './test-utils';
import { externalClientsKey } from '@/api/external-outbounds';

const clients = [
  {
    protocol: 'naive',
    name: 'NaiveProxy',
    installed: false,
    version: '',
    release: '',
    latestVersion: 'v154.0.8037.49-5',
    updateAvailable: false,
    supported: true,
    state: 'idle',
    error: '',
  },
  {
    protocol: 'mieru',
    name: 'mieru',
    installed: true,
    version: '3.38.1',
    release: 'v3.38.1',
    latestVersion: 'v3.39.0',
    updateAvailable: true,
    supported: true,
    state: 'idle',
    error: '',
  },
];

describe('external client software management', () => {
  beforeEach(() => {
    vi.spyOn(HttpUtil, 'get').mockImplementation(
      async (path) => new Msg(true, '', path.endsWith('/clients') ? clients : []),
    );
  });

  it('shows actual versions and installs only after confirmation', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', null));
    renderWithProviders(<ExternalOutbounds />);
    const region = within(await screen.findByRole('region', { name: 'Client software' }));
    expect(await region.findByText('v3.38.1')).toBeTruthy();
    expect(region.getByText('v3.39.0')).toBeTruthy();
    fireEvent.click(region.getByRole('button', { name: 'Install NaiveProxy' }));
    expect(post).not.toHaveBeenCalled();
    await act(async () => fireEvent.click(await screen.findByRole('button', { name: 'Confirm' })));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/panel/api/external-outbounds/clients/install',
        { protocol: 'naive' },
        expect.anything(),
      ),
    );
  });

  it('displays installation failures and prevents concurrent operations', async () => {
    vi.spyOn(HttpUtil, 'get').mockImplementation(
      async (path) =>
        new Msg(
          true,
          '',
          path.endsWith('/clients')
            ? [
                { ...clients[0], state: 'error', error: 'client archive SHA-256 mismatch' },
                { ...clients[1], state: 'downloading' },
              ]
            : [],
        ),
    );
    renderWithProviders(<ExternalOutbounds />);
    const region = within(await screen.findByRole('region', { name: 'Client software' }));
    expect(await region.findByText('client archive SHA-256 mismatch')).toBeTruthy();
    expect(
      (region.getByRole('button', { name: 'Install NaiveProxy' }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it('updates an installed client and keeps its old version visible until completion', async () => {
    let rows = clients.map((row) => ({ ...row }));
    vi.spyOn(HttpUtil, 'get').mockImplementation(
      async (path) => new Msg(true, '', path.endsWith('/clients') ? rows : []),
    );
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async () => {
      rows = rows.map((row) => (row.protocol === 'mieru' ? { ...row, state: 'downloading' } : row));
      return new Msg(true, '', null);
    });
    const cache = makeTestQueryClient();
    renderWithProviders(<ExternalOutbounds />, { queryClient: cache });
    const region = within(await screen.findByRole('region', { name: 'Client software' }));
    await region.findByText('v3.38.1');
    fireEvent.click(region.getByRole('button', { name: 'Update mieru' }));
    await act(async () => fireEvent.click(await screen.findByRole('button', { name: 'Confirm' })));
    await region.findByText('Downloading');
    expect(region.getByText('v3.38.1')).toBeTruthy();
    expect(post).toHaveBeenCalledWith(
      '/panel/api/external-outbounds/clients/install',
      { protocol: 'mieru' },
      expect.anything(),
    );
    rows = rows.map((row) =>
      row.protocol === 'mieru'
        ? { ...row, state: 'idle', version: '3.39.0', release: 'v3.39.0', updateAvailable: false }
        : row,
    );
    await act(async () => {
      await cache.invalidateQueries({ queryKey: externalClientsKey });
    });
    await waitFor(() => expect(region.queryByText('v3.38.1')).toBeNull());
    expect(
      (region.getByRole('button', { name: 'Update mieru' }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });
});
