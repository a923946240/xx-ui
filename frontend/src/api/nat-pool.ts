import { z } from 'zod';
import type { NodeRecord } from '@/api/queries/useNodesQuery';
import { NATPoolEntrySchema } from '@/generated/zod';

export const natPoolEntrySchema = NATPoolEntrySchema.extend({
  id: z.string().regex(/^[0-9a-f]{64}$/),
  candidateIp: z.ipv4(),
  seenAt: z.number().int().positive(),
});

export const natPoolSchema = z.array(natPoolEntrySchema);

export function isNATHTTPSNode(node: NodeRecord) {
  return (
    node.enable &&
    !node.transitive &&
    (!node.scheme || node.scheme === 'https') &&
    node.tlsVerifyMode !== 'skip'
  );
}
