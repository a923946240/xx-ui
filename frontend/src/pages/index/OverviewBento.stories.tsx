import type { Meta, StoryObj } from '@storybook/react-vite';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { expect, within } from 'storybook/test';

import { Status } from '@/models/status';
import { externalOutboundsKey } from '@/api/external-outbounds';
import OverviewBento from './OverviewBento';
import type { OverviewHistory } from './useOverviewHistory';
import './IndexPage.css';
import '@/styles/charcoal-theme.css';

const client = new QueryClient({ defaultOptions: { queries: { enabled: false } } });
client.setQueryData(externalOutboundsKey, []);
const history: OverviewHistory = {
  labels: ['0', '1', '2'],
  series: {
    cpu: [0, 0, 0],
    mem: [0, 0, 0],
    swap: [0, 0, 0],
    diskUsage: [0, 0, 0],
    netUp: [0, 0, 0],
    netDown: [0, 0, 0],
    tcpCount: [0, 0, 0],
    udpCount: [0, 0, 0],
  },
};
const meta = {
  title: 'Monitoring/Charcoal overview',
  component: OverviewBento,
  tags: ['autodocs'],
  decorators: [
    (Story) => (
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <div className="index-page" style={{ maxWidth: 1350 }}>
            <Story />
          </div>
        </MemoryRouter>
      </QueryClientProvider>
    ),
  ],
  args: {
    status: new Status({ unavailableMetrics: ['cpu', 'swap', 'uptime'] }),
    history,
    isMobile: false,
    showIp: false,
    onToggleIp: () => undefined,
  },
} satisfies Meta<typeof OverviewBento>;
export default meta;
type Story = StoryObj<typeof meta>;

export const EmptyAndUnavailable: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('No external tunnel exits configured.')).toBeVisible();
    await expect(canvas.getByRole('heading', { name: 'Total traffic' })).toBeVisible();
    await expect(canvas.getAllByText('—').length).toBeGreaterThan(0);
  },
};
