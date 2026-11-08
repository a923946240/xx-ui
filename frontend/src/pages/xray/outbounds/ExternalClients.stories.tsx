import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { HttpUtil, Msg } from '@/utils';
import type { ExternalClient } from '@/schemas/external-outbound';
import ExternalClients from './ExternalClients';

const fixtures: ExternalClient[] = [
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
  {
    protocol: 'trusttunnel',
    name: 'TrustTunnel',
    installed: true,
    version: '1.1.11',
    release: 'v1.1.11',
    latestVersion: 'v1.1.11',
    updateAvailable: false,
    supported: true,
    state: 'idle',
    error: '',
  },
];

const meta = {
  title: 'Xray/ExternalClients',
  component: ExternalClients,
  parameters: {
    layout: 'fullscreen',
    docs: {
      description: {
        component:
          'Local API fixtures demonstrate missing, upgradeable and current client installations. No software is downloaded.',
      },
    },
  },
  decorators: [
    function ClientProvider(Story) {
      const [cache] = useState(
        () => new QueryClient({ defaultOptions: { queries: { retry: false } } }),
      );
      return (
        <QueryClientProvider client={cache}>
          <Story />
        </QueryClientProvider>
      );
    },
  ],
  beforeEach: () => {
    let rows = fixtures.map((row) => ({ ...row }));
    const get = HttpUtil.get;
    const post = HttpUtil.post;
    const timers: number[] = [];
    HttpUtil.get = async <T,>() => new Msg<T>(true, '', rows as T);
    HttpUtil.post = async <T,>(url: string, body?: unknown) => {
      const protocol =
        typeof body === 'object' && body && 'protocol' in body ? String(body.protocol) : '';
      if (url.endsWith('/install')) {
        rows = rows.map((row) =>
          row.protocol === protocol ? { ...row, state: 'downloading' } : row,
        );
        timers.push(
          window.setTimeout(() => {
            rows = rows.map((row) =>
              row.protocol === protocol
                ? {
                    ...row,
                    installed: true,
                    release: row.latestVersion,
                    version: row.latestVersion.slice(1),
                    state: 'idle',
                    updateAvailable: false,
                  }
                : row,
            );
          }, 1500),
        );
      }
      return new Msg<T>(true, '', null);
    };
    return () => {
      HttpUtil.get = get;
      HttpUtil.post = post;
      timers.forEach(window.clearTimeout);
    };
  },
} satisfies Meta<typeof ExternalClients>;

export default meta;
type Story = StoryObj<typeof meta>;
export const Management: Story = {};
