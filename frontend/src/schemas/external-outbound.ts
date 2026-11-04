import { z } from 'zod';

const required = 'pages.xray.external.required';

const ExternalOutboundFieldsSchema = z.object({
  id: z.number().int().min(0),
  tag: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/, 'pages.xray.external.invalidTag'),
  remark: z.string().max(256),
  protocol: z.enum(['naive', 'mieru', 'trusttunnel']),
  host: z.string().min(1, required),
  port: z.number().int().min(1).max(65535),
  username: z.string().min(1, required).max(255),
  password: z.string().max(1024),
  serverName: z.string(),
  transport: z.enum(['tcp', 'quic']),
  enable: z.boolean(),
});

export const ExternalOutboundInputSchema = ExternalOutboundFieldsSchema.superRefine(
  (value, ctx) => {
    if (value.id === 0 && !value.password) {
      ctx.addIssue({ code: 'custom', path: ['password'], message: required });
    }
    if (value.protocol === 'mieru' && value.transport !== 'tcp') {
      ctx.addIssue({
        code: 'custom',
        path: ['transport'],
        message: 'pages.xray.external.mieruTcp',
      });
    }
  },
);

export const ExternalOutboundViewSchema = ExternalOutboundFieldsSchema.omit({
  password: true,
}).extend({
  passwordSet: z.boolean(),
  state: z.string(),
  logs: z.string(),
});

export const ExternalOutboundListSchema = z.array(ExternalOutboundViewSchema);
export const ExternalOutboundProbeSchema = z.object({
  success: z.boolean(),
  delay: z.number(),
  httpStatus: z.number(),
});

export type ExternalOutboundInput = z.infer<typeof ExternalOutboundInputSchema>;
export type ExternalOutboundView = z.infer<typeof ExternalOutboundViewSchema>;

export const ExternalClientSchema = z.object({
  protocol: z.enum(['naive', 'mieru', 'trusttunnel']),
  name: z.string(),
  installed: z.boolean(),
  version: z.string(),
  release: z.string(),
  latestVersion: z.string(),
  updateAvailable: z.boolean(),
  supported: z.boolean(),
  state: z.string(),
  error: z.string(),
});
export const ExternalClientListSchema = z.array(ExternalClientSchema);
export type ExternalClient = z.infer<typeof ExternalClientSchema>;
