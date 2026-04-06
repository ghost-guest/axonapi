import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { z } from 'zod';
import { zodResolver } from '@hookform/resolvers/zod';
import { IconCheck, IconDeviceFloppy, IconRefresh, IconSelector } from '@tabler/icons-react';
import { toast } from 'sonner';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Form, FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Switch } from '@/components/ui/switch';
import { PermissionGuard } from '@/components/permission-guard';
import { graphqlRequest } from '@/gql/graphql';
import { apiRequest } from '@/lib/api-client';
import { cn } from '@/lib/utils';

type PublicBenefitHubConfig = {
  providers: unknown[];
  upstreams: unknown[];
  outbound: {
    enabled: boolean;
    public_base_url?: string;
    public_api_key?: string;
    default_route_mode?: string;
    session_affinity_enabled?: boolean;
    session_affinity_ttl_seconds?: number;
    default_claude_fallback?: string[];
    default_codex_fallback?: string[];
    default_opencode_fallback?: string[];
    default_gemini_fallback?: string[];
    default_generic_fallback?: string[];
  };
};

type ChannelConnection = {
  edges: Array<{
    node: {
      id: string;
      name: string;
      supportedModels: string[];
      status: string;
    };
  }>;
};

const queryKey = ['public-benefit-outbound'];
const channelModelsQueryKey = ['public-benefit-outbound', 'channel-models'];

const ALL_CHANNEL_MODELS_QUERY = `
  query PublicBenefitOutboundChannelModels {
    channels {
      edges {
        node {
          id
          name
          status
          supportedModels
        }
      }
    }
  }
`;

const schema = z.object({
  enabled: z.boolean(),
  public_base_url: z.string().optional(),
  public_api_key: z.string().optional(),
  default_route_mode: z.string().optional(),
  session_affinity_enabled: z.boolean(),
  session_affinity_ttl_seconds: z.string().min(1),
  default_claude_fallback: z.array(z.string()).default([]),
  default_codex_fallback: z.array(z.string()).default([]),
  default_opencode_fallback: z.array(z.string()).default([]),
  default_gemini_fallback: z.array(z.string()).default([]),
  default_generic_fallback: z.array(z.string()).default([]),
});

type FormValues = z.infer<typeof schema>;

async function fetchConfig() {
  return apiRequest<PublicBenefitHubConfig>('/admin/public-benefit/config', { requireAuth: true });
}

async function fetchAvailableModels() {
  const data = await graphqlRequest<{ channels: ChannelConnection }>(ALL_CHANNEL_MODELS_QUERY);
  const models = new Set<string>();

  data.channels.edges.forEach(({ node }) => {
    if (node.status !== 'enabled') {
      return;
    }
    node.supportedModels.forEach((model) => {
      const normalized = model.trim();
      if (normalized) {
        models.add(normalized);
      }
    });
  });

  return Array.from(models).sort((a, b) => a.localeCompare(b));
}

function toFormValue(config?: PublicBenefitHubConfig['outbound']): FormValues {
  return {
    enabled: Boolean(config?.enabled),
    public_base_url: config?.public_base_url || '',
    public_api_key: config?.public_api_key || '',
    default_route_mode: config?.default_route_mode || 'adaptive',
    session_affinity_enabled: config?.session_affinity_enabled ?? true,
    session_affinity_ttl_seconds: String(config?.session_affinity_ttl_seconds || 1800),
    default_claude_fallback: config?.default_claude_fallback || [],
    default_codex_fallback: config?.default_codex_fallback || [],
    default_opencode_fallback: config?.default_opencode_fallback || [],
    default_gemini_fallback: config?.default_gemini_fallback || [],
    default_generic_fallback: config?.default_generic_fallback || [],
  };
}

function ModelMultiSelect({
  title,
  description,
  value,
  onChange,
  options,
}: {
  title: string;
  description?: string;
  value: string[];
  onChange: (next: string[]) => void;
  options: string[];
}) {
  return (
    <FormItem>
      <FormLabel>{title}</FormLabel>
      <Popover>
        <PopoverTrigger asChild>
          <Button variant='outline' className='w-full justify-between'>
            {value.length > 0 ? `已选择 ${value.length} 个模型` : '选择渠道支持的模型'}
            <IconSelector className='ml-2 h-4 w-4 shrink-0 opacity-50' />
          </Button>
        </PopoverTrigger>
        <PopoverContent className='w-[420px] p-0' align='start'>
          <Command>
            <CommandInput placeholder='搜索模型...' />
            <CommandList>
              <CommandEmpty>没有可选模型</CommandEmpty>
              <CommandGroup className='max-h-64 overflow-auto'>
                {options.map((model) => {
                  const selected = value.includes(model);
                  return (
                    <CommandItem
                      key={model}
                      value={model}
                      onSelect={() => {
                        onChange(selected ? value.filter((item) => item !== model) : [...value, model]);
                      }}
                    >
                      <IconCheck className={cn('mr-2 h-4 w-4', selected ? 'opacity-100' : 'opacity-0')} />
                      <span className='font-mono text-sm'>{model}</span>
                    </CommandItem>
                  );
                })}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {description ? <FormDescription>{description}</FormDescription> : null}
      {value.length > 0 ? (
        <div className='mt-2 flex flex-wrap gap-2'>
          {value.map((model) => (
            <Badge key={model} variant='secondary' className='cursor-pointer font-mono text-xs' onClick={() => onChange(value.filter((item) => item !== model))}>
              {model}
              <span className='ml-1'>×</span>
            </Badge>
          ))}
        </div>
      ) : null}
      <FormMessage />
    </FormItem>
  );
}

export default function PublicBenefitOutboundManagement() {
  const queryClient = useQueryClient();
  const configQuery = useQuery({ queryKey: [...queryKey, 'config'], queryFn: fetchConfig });
  const channelModelsQuery = useQuery({ queryKey: channelModelsQueryKey, queryFn: fetchAvailableModels });

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    values: toFormValue(configQuery.data?.outbound),
  });

  const saveMutation = useMutation({
    mutationFn: async (value: FormValues) => {
      const current = await fetchConfig();
      return apiRequest('/admin/public-benefit/config', {
        method: 'PUT',
        requireAuth: true,
        body: {
          ...current,
          outbound: {
            enabled: value.enabled,
            public_base_url: value.public_base_url?.trim() || '',
            public_api_key: value.public_api_key?.trim() || '',
            default_route_mode: value.default_route_mode?.trim() || 'adaptive',
            session_affinity_enabled: value.session_affinity_enabled,
            session_affinity_ttl_seconds: Math.max(60, Number(value.session_affinity_ttl_seconds || '1800')),
            default_claude_fallback: value.default_claude_fallback,
            default_codex_fallback: value.default_codex_fallback,
            default_opencode_fallback: value.default_opencode_fallback,
            default_gemini_fallback: value.default_gemini_fallback,
            default_generic_fallback: value.default_generic_fallback,
          },
        },
      });
    },
    onSuccess: async () => {
      toast.success('聚合出口配置已保存');
      await queryClient.invalidateQueries({ queryKey: [...queryKey, 'config'] });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : '保存失败'),
  });

  const syncMutation = useMutation({
    mutationFn: () => apiRequest('/admin/public-benefit/sync', { method: 'POST', requireAuth: true }),
    onSuccess: () => toast.success('已触发聚合同步'),
  });

  const onSubmit = form.handleSubmit(async (value) => {
    await saveMutation.mutateAsync(value);
  });

  return (
    <>
      <Header fixed>
        <div className='flex flex-1 items-center justify-between gap-3'>
          <div>
            <h2 className='text-xl font-bold tracking-tight'>聚合出口</h2>
            <p className='text-sm text-muted-foreground'>配置统一对外 `baseURL + key`、协议感知模型 fallback 和会话无感切换参数。</p>
          </div>
          <div className='flex gap-2'>
            <PermissionGuard requiredScope='write_settings'>
              <Button variant='outline' onClick={() => syncMutation.mutate()}>
                <IconRefresh className='mr-2 h-4 w-4' />
                同步
              </Button>
            </PermissionGuard>
            <PermissionGuard requiredScope='write_settings'>
              <Button onClick={onSubmit} disabled={saveMutation.isPending}>
                <IconDeviceFloppy className='mr-2 h-4 w-4' />
                保存
              </Button>
            </PermissionGuard>
          </div>
        </div>
      </Header>

      <Form {...form}>
        <Main fixed>
          <div className='grid gap-4 lg:grid-cols-3'>
            <Card className='lg:col-span-2'>
              <CardHeader>
                <CardTitle>统一出口设置</CardTitle>
                <CardDescription>这里定义聚合后对外暴露的统一入口、访问密钥和基础路由策略。</CardDescription>
              </CardHeader>
              <CardContent>
                <div className='grid gap-4'>
                  <div className='grid gap-4 md:grid-cols-2'>
                    <FormField control={form.control} name='enabled' render={({ field }) => <FormItem className='flex items-center justify-between rounded-lg border p-3'><FormLabel>启用统一出口</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} /></FormControl></FormItem>} />
                    <FormField control={form.control} name='session_affinity_enabled' render={({ field }) => <FormItem className='flex items-center justify-between rounded-lg border p-3'><FormLabel>启用会话粘性</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} /></FormControl></FormItem>} />
                  </div>

                  <FormField control={form.control} name='public_base_url' render={({ field }) => <FormItem><FormLabel>统一访问地址</FormLabel><FormControl><Input {...field} placeholder='https://gateway.example/v1' /></FormControl><FormDescription>这是给 Claude、Codex 等终端使用的统一入口地址。</FormDescription><FormMessage /></FormItem>} />
                  <FormField control={form.control} name='public_api_key' render={({ field }) => <FormItem><FormLabel>统一访问密钥</FormLabel><FormControl><Input {...field} /></FormControl><FormDescription>这里只是聚合出口自己的访问密钥，不是供应商站点里的 API Key。</FormDescription><FormMessage /></FormItem>} />

                  <div className='grid gap-4 md:grid-cols-2'>
                    <FormField control={form.control} name='default_route_mode' render={({ field }) => <FormItem><FormLabel>默认路由模式</FormLabel><FormControl><Input {...field} placeholder='adaptive' /></FormControl><FormMessage /></FormItem>} />
                    <FormField control={form.control} name='session_affinity_ttl_seconds' render={({ field }) => <FormItem><FormLabel>会话粘性 TTL（秒）</FormLabel><FormControl><Input type='number' {...field} /></FormControl><FormMessage /></FormItem>} />
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>说明</CardTitle>
                <CardDescription>聚合出口将复用 AxonHub 现有的 retry / failover / load balance 主链。</CardDescription>
              </CardHeader>
              <CardContent className='space-y-3 text-sm text-muted-foreground'>
                <p>`Claude` 请求优先走 `Claude` 模型族。</p>
                <p>`Codex` 请求优先走 `Codex` 模型族。</p>
                <p>当模型不可用时，按 fallback 序列自动轮换。</p>
                <p>会话粘性会优先保持同会话命中同一健康上游，实现更接近 ccNexus 的无感切换体验。</p>
              </CardContent>
            </Card>
          </div>

          <div className='mt-4 grid gap-4 lg:grid-cols-2'>
            <Card>
              <CardHeader><CardTitle>Claude / Anthropic fallback</CardTitle></CardHeader>
              <CardContent>
                <FormField
                  control={form.control}
                  name='default_claude_fallback'
                  render={({ field }) => (
                    <ModelMultiSelect
                      title='回退模型'
                      description='只能选择当前已配置渠道实际支持的模型，避免手填不可用模型。'
                      value={field.value}
                      onChange={field.onChange}
                      options={channelModelsQuery.data || []}
                    />
                  )}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader><CardTitle>Codex fallback</CardTitle></CardHeader>
              <CardContent>
                <FormField
                  control={form.control}
                  name='default_codex_fallback'
                  render={({ field }) => (
                    <ModelMultiSelect
                      title='回退模型'
                      description='Codex 请求只会在这些已存在模型中自动轮换。'
                      value={field.value}
                      onChange={field.onChange}
                      options={channelModelsQuery.data || []}
                    />
                  )}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader><CardTitle>Gemini / OpenCode fallback</CardTitle></CardHeader>
              <CardContent className='space-y-4'>
                <FormField
                  control={form.control}
                  name='default_gemini_fallback'
                  render={({ field }) => (
                    <ModelMultiSelect title='Gemini' value={field.value} onChange={field.onChange} options={channelModelsQuery.data || []} />
                  )}
                />
                <FormField
                  control={form.control}
                  name='default_opencode_fallback'
                  render={({ field }) => (
                    <ModelMultiSelect title='OpenCode' value={field.value} onChange={field.onChange} options={channelModelsQuery.data || []} />
                  )}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader><CardTitle>通用 fallback</CardTitle></CardHeader>
              <CardContent>
                <FormField
                  control={form.control}
                  name='default_generic_fallback'
                  render={({ field }) => (
                    <ModelMultiSelect title='回退模型' value={field.value} onChange={field.onChange} options={channelModelsQuery.data || []} />
                  )}
                />
              </CardContent>
            </Card>
          </div>
        </Main>
      </Form>
    </>
  );
}
