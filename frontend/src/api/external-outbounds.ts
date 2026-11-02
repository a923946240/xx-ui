import { HttpUtil } from '@/utils';
import {
  ExternalOutboundInputSchema,
  ExternalOutboundListSchema,
  ExternalOutboundViewSchema,
  ExternalOutboundProbeSchema,
} from '@/schemas/external-outbound';
import type { ExternalOutboundInput } from '@/schemas/external-outbound';

const root = '/panel/api/external-outbounds';
export const externalOutboundsKey = ['external-outbounds'] as const;

export async function listExternalOutbounds() {
  const msg = await HttpUtil.get(`${root}/list`, undefined, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'Could not load external exits');
  return ExternalOutboundListSchema.parse(msg.obj);
}

export async function saveExternalOutbound(value: ExternalOutboundInput) {
  const msg = await HttpUtil.post(`${root}/save`, ExternalOutboundInputSchema.parse(value), {
    silent: true,
    headers: { 'Content-Type': 'application/json' },
  });
  if (!msg.success) throw new Error(msg.msg || 'Could not save external exit');
  return ExternalOutboundViewSchema.parse(msg.obj);
}

export async function deleteExternalOutbound(id: number) {
  const msg = await HttpUtil.post(`${root}/del/${id}`, undefined, { silent: true });
  if (!msg.success) throw new Error(msg.msg || 'Could not delete external exit');
}

export async function probeExternalOutbound(id: number) {
  const msg = await HttpUtil.post(`${root}/probe/${id}`, undefined, {
    silent: true,
    timeout: 20000,
  });
  if (!msg.success) throw new Error(msg.msg || 'Tunnel probe failed');
  return ExternalOutboundProbeSchema.parse(msg.obj);
}
