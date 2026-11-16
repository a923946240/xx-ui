import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { PropsWithChildren } from 'react';
import { expect, it, vi } from 'vitest';

import { useXraySetting } from '@/hooks/useXraySetting';
import { HttpUtil, Msg } from '@/utils';

it('saves an edited Xray template against its original revision after a background update', async () => {
  let templateRevision = '"before-sync"';
  const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (url) => {
    if (url === '/panel/api/xray/update') return new Msg(false, 'Template changed');
    return new Msg(true, '', JSON.stringify({ xraySetting: { outbounds: [] }, templateRevision }));
  });
  const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
  const client = new QueryClient();
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  try {
    const { result } = renderHook(useXraySetting, { wrapper });
    await waitFor(() => expect(result.current.xraySetting).not.toBe(''));
    act(() => result.current.setXraySetting('{"outbounds":[],"log":{"loglevel":"error"}}'));

    templateRevision = '"after-sync"';
    await act(() => result.current.fetchAll());
    await act(() => result.current.saveAll());

    expect(post).toHaveBeenCalledWith(
      '/panel/api/xray/update',
      expect.objectContaining({ xraySetting: '{"outbounds":[],"log":{"loglevel":"error"}}' }),
      { headers: { 'If-Match': '"before-sync"' } },
    );
    expect(result.current.xraySetting).toContain('"loglevel":"error"');
  } finally {
    post.mockRestore();
    get.mockRestore();
    client.clear();
  }
});
