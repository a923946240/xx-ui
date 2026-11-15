import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useState } from 'react';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';

import { HttpUtil, Msg } from '@/utils';
import ExternalOutbounds from '@/pages/xray/outbounds/ExternalOutbounds';
import { renderWithProviders } from './test-utils';
import { makeTestQueryClient } from './test-utils';
import { externalOutboundsKey } from '@/api/external-outbounds';
import { act } from '@testing-library/react';

const initialRow = {
  id: 1,
  tag: 'test-exit',
  remark: 'Original exit',
  protocol: 'naive',
  host: 'proxy.example',
  port: 443,
  username: 'alice',
  serverName: '',
  transport: 'tcp',
  enable: true,
  mieruMultiplexing: '',
  mieruHandshakeMode: '',
  mieruMtu: 0,
  passwordSet: true,
  state: 'missing-binary',
  logs: '',
};

describe('external final exits', () => {
  let row = { ...initialRow };
  let sent: unknown;

  beforeEach(() => {
    row = { ...initialRow };
    sent = undefined;
    vi.spyOn(HttpUtil, 'get').mockImplementation(
      async (path) => new Msg(true, '', path.endsWith('/clients') ? [] : [row]),
    );
    vi.spyOn(HttpUtil, 'post').mockImplementation(async (path, input) => {
      if (path.endsWith('/save')) {
        sent = input;
        row = { ...row, ...(input as object) };
        return new Msg(true, '', row);
      }
      return new Msg(false, 'outbound is referenced; update routing before deleting it');
    });
  });

  it('edits without exposing or replacing the retained password', async () => {
    renderWithProviders(<ExternalOutbounds />);
    await screen.findByText('Original exit');
    fireEvent.click(screen.getByRole('button', { name: 'Edit test-exit' }));
    const dialog = within(screen.getByRole('region', { name: 'Exit configuration' }));
    const password = dialog.getByLabelText('Password') as HTMLInputElement;
    expect(password.value).toBe('');
    fireEvent.change(dialog.getByLabelText('Name'), { target: { value: 'Updated exit' } });
    fireEvent.click(dialog.getByRole('button', { name: 'Save exit' }));
    await screen.findByText('Updated exit');
    expect(sent).toMatchObject({ id: 1, tag: 'test-exit', password: '', remark: 'Updated exit' });
  });

  it('keeps the inline editor and configuration when a save fails', async () => {
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'invalid server host'));
    renderWithProviders(<ExternalOutbounds />);
    await screen.findByText('Original exit');
    fireEvent.click(screen.getByRole('button', { name: 'Edit test-exit' }));
    fireEvent.click(
      within(screen.getByRole('region', { name: 'Exit configuration' })).getByRole('button', {
        name: 'Save exit',
      }),
    );
    await screen.findByText('invalid server host');
    expect(screen.getByRole('region', { name: 'Exit configuration' })).toBeTruthy();
    expect(screen.getByText('Original exit')).toBeTruthy();
  });

  it('does not overwrite pending Xray template edits', async () => {
    renderWithProviders(<ExternalOutbounds disabled />);
    await screen.findByText('Original exit');
    await waitFor(() =>
      expect((screen.getByRole('button', { name: 'Add exit' }) as HTMLButtonElement).disabled).toBe(
        true,
      ),
    );
    expect(
      (screen.getByRole('button', { name: 'Edit test-exit' }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it('retains typed edits when background exit status refreshes', async () => {
    const queryClient = makeTestQueryClient();
    renderWithProviders(<ExternalOutbounds />, { queryClient });
    await screen.findByText('Original exit');
    fireEvent.click(screen.getByRole('button', { name: 'Edit test-exit' }));
    const editor = within(screen.getByRole('region', { name: 'Exit configuration' }));
    fireEvent.change(editor.getByLabelText('Name'), { target: { value: 'Unsaved draft' } });
    await act(async () => {
      queryClient.setQueryData(externalOutboundsKey, [{ ...initialRow, state: 'running' }]);
    });
    expect((editor.getByLabelText('Name') as HTMLInputElement).value).toBe('Unsaved draft');
    expect(
      (screen.getByRole('switch', { name: 'Enabled test-exit' }) as HTMLButtonElement).disabled,
    ).toBe(true);
    fireEvent.click(editor.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByLabelText('Name')).toBeNull();
    expect(sent).toBeUndefined();
  });

  it('creates a new exit in a dialog with a required password', async () => {
    renderWithProviders(<ExternalOutbounds />);
    await screen.findByText('Original exit');
    fireEvent.click(screen.getByRole('button', { name: 'Add exit' }));
    const dialog = within(screen.getByRole('dialog'));
    expect((dialog.getByLabelText('Password') as HTMLInputElement).value).toBe('');
    fireEvent.click(dialog.getByRole('button', { name: 'Save exit' }));
    await waitFor(() =>
      expect(dialog.getAllByText('This field is required.').length).toBeGreaterThan(0),
    );
    expect(sent).toBeUndefined();
  });

  it('blocks Enter submission if native template edits become pending during editing', async () => {
    function PendingTemplateFixture() {
      const [pending, setPending] = useState(false);
      return (
        <>
          <button onClick={() => setPending(true)}>Change native template</button>
          <ExternalOutbounds disabled={pending} />
        </>
      );
    }
    renderWithProviders(<PendingTemplateFixture />);
    await screen.findByText('Original exit');
    fireEvent.click(screen.getByRole('button', { name: 'Edit test-exit' }));
    fireEvent.click(screen.getByRole('button', { name: 'Change native template' }));
    const editor = screen.getByRole('region', { name: 'Exit configuration' });
    await act(async () => {
      fireEvent.submit(editor.querySelector('form')!);
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(sent).toBeUndefined();
  });
});
