import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { z } from 'zod';
import { zodResolver } from '@hookform/resolvers/zod';
import { IconPlus, IconRefresh, IconDeviceFloppy, IconEdit, IconHeartbeat } from '@tabler/icons-react';
import { toast } from 'sonner';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { PermissionGuard } from '@/components/permission-guard';
import { apiRequest } from '@/lib/api-client';

type PublicBenefitRouteMode = 'priority' | 'round_robin' | 'adaptive' | 'failover';

type PublicBenefitUpstreamSite = {
  id: string;
  name: string;
  base_url: string;
  api_key: string;
  enabled: boolean;
  auto_discover_models: boolean;
  discover_models_cron?: string;
  preferred_model_family?: string;
  route_mode: PublicBenefitRouteMode;
  weight: number;
  health_check_path?: string;
  health_check_model?: string;
  failure_threshold: number;
  recover_threshold: number;
  supports_claude: boolean;
  supports_codex: boolean;
  supports_opencode: boolean;
  supports_gemini: boolean;
  model_allowlist?: string[];
  model_blocklist?: string[];
  remark?: string;
};

type PublicBenefitUpstreamRuntime = {
  upstream_id: string;
  health_status?: string;
  available_models?: string[];
  total_requests: number;
  total_tokens: number;
  last_error?: string;
};

type PublicBenefitHubConfig = {
  providers: unknown[];
  upstreams: PublicBenefitUpstreamSite[];
  outbound: Record<string, unknown>;
};

type PublicBenefitRuntimeState = {
  providers: unknown[];
  upstreams: PublicBenefitUpstreamRuntime[];
  daily_usage: unknown[];
};

const queryKey = ['public-benefit-upstreams'];

const formSchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  base_url: z.string().min(1),
  api_key: z.string().min(1),
  enabled: z.boolean(),
  auto_discover_models: z.boolean(),
  discover_models_cron: z.string().optional(),
  preferred_model_family: z.string().optional(),
  route_mode: z.enum(['priority', 'round_robin', 'adaptive', 'failover']),
  weight: z.string().min(1),
  health_check_path: z.string().optional(),
  health_check_model: z.string().optional(),
  failure_threshold: z.string().min(1),
  recover_threshold: z.string().min(1),
  supports_claude: z.boolean(),
  supports_codex: z.boolean(),
  supports_opencode: z.boolean(),
  supports_gemini: z.boolean(),
  model_allowlist: z.string().optional(),
  model_blocklist: z.string().optional(),
  remark: z.string().optional(),
});

type FormValues = z.infer<typeof formSchema>;

async function fetchConfig() {
  return apiRequest<PublicBenefitHubConfig>('/admin/public-benefit/config', { requireAuth: true });
}

async function fetchRuntime() {
  return apiRequest<PublicBenefitRuntimeState>('/admin/public-benefit/runtime', { requireAuth: true });
}

function defaultValues(): FormValues {
  return {
    id: '',
    name: '',
    base_url: '',
    api_key: '',
    enabled: true,
    auto_discover_models: true,
    discover_models_cron: '*/30 * * * *',
    preferred_model_family: '',
    route_mode: 'adaptive',
    weight: '1',
    health_check_path: '/v1/models',
    health_check_model: '',
    failure_threshold: '3',
    recover_threshold: '1',
    supports_claude: false,
    supports_codex: true,
    supports_opencode: false,
    supports_gemini: false,
    model_allowlist: '',
    model_blocklist: '',
    remark: '',
  };
}

function toFormValue(item: PublicBenefitUpstreamSite): FormValues {
  return {
    ...item,
    discover_models_cron: item.discover_models_cron || '',
    preferred_model_family: item.preferred_model_family || '',
    weight: String(item.weight),
    health_check_path: item.health_check_path || '',
    health_check_model: item.health_check_model || '',
    failure_threshold: String(item.failure_threshold),
    recover_threshold: String(item.recover_threshold),
    model_allowlist: (item.model_allowlist || []).join('\n'),
    model_blocklist: (item.model_blocklist || []).join('\n'),
    remark: item.remark || '',
  };
}

function fromFormValue(value: FormValues): PublicBenefitUpstreamSite {
  const splitLines = (raw?: string) =>
    (raw || '')
      .split('\n')
      .map((item) => item.trim())
      .filter(Boolean);

  return {
    id: value.id.trim(),
    name: value.name.trim(),
    base_url: value.base_url.trim(),
    api_key: value.api_key.trim(),
    enabled: value.enabled,
    auto_discover_models: value.auto_discover_models,
    discover_models_cron: value.discover_models_cron?.trim() || '',
    preferred_model_family: value.preferred_model_family?.trim() || '',
    route_mode: value.route_mode,
    weight: Math.max(1, Number(value.weight || '1')),
    health_check_path: value.health_check_path?.trim() || '',
    health_check_model: value.health_check_model?.trim() || '',
    failure_threshold: Math.max(1, Number(value.failure_threshold || '3')),
    recover_threshold: Math.max(1, Number(value.recover_threshold || '1')),
    supports_claude: value.supports_claude,
    supports_codex: value.supports_codex,
    supports_opencode: value.supports_opencode,
    supports_gemini: value.supports_gemini,
    model_allowlist: splitLines(value.model_allowlist),
    model_blocklist: splitLines(value.model_blocklist),
    remark: value.remark?.trim() || '',
  };
}

export default function PublicBenefitUpstreamsManagement() {
  const queryClient = useQueryClient();
  const configQuery = useQuery({ queryKey: [...queryKey, 'config'], queryFn: fetchConfig });
  const runtimeQuery = useQuery({ queryKey: [...queryKey, 'runtime'], queryFn: fetchRuntime, refetchInterval: 30_000 });
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingID, setEditingID] = useState<string | null>(null);

  const form = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: defaultValues(),
  });

  const upstreams = configQuery.data?.upstreams || [];
  const runtimeByID = useMemo(
    () => new Map((runtimeQuery.data?.upstreams || []).map((item) => [item.upstream_id, item])),
    [runtimeQuery.data?.upstreams]
  );

  const saveMutation = useMutation({
    mutationFn: async (nextUpstreams: PublicBenefitUpstreamSite[]) => {
      const current = await fetchConfig();
      return apiRequest('/admin/public-benefit/config', {
        method: 'PUT',
        requireAuth: true,
        body: { ...current, upstreams: nextUpstreams },
      });
    },
    onSuccess: async () => {
      toast.success('公益上游配置已保存');
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: [...queryKey, 'config'] }),
        queryClient.invalidateQueries({ queryKey: [...queryKey, 'runtime'] }),
      ]);
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : '保存失败'),
  });

  const refreshMutation = useMutation({
    mutationFn: (upstreamID: string) => apiRequest(`/admin/public-benefit/upstreams/${upstreamID}/refresh`, { method: 'POST', requireAuth: true }),
    onSuccess: async () => {
      toast.success('已刷新上游状态');
      await queryClient.invalidateQueries({ queryKey: [...queryKey, 'runtime'] });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : '刷新失败'),
  });

  const syncMutation = useMutation({
    mutationFn: () => apiRequest('/admin/public-benefit/sync', { method: 'POST', requireAuth: true }),
    onSuccess: async () => {
      toast.success('已触发全量同步');
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: [...queryKey, 'config'] }),
        queryClient.invalidateQueries({ queryKey: [...queryKey, 'runtime'] }),
      ]);
    },
  });

  const handleEdit = (item: PublicBenefitUpstreamSite) => {
    setEditingID(item.id);
    form.reset(toFormValue(item));
    setDialogOpen(true);
  };

  const handleDelete = async (id: string) => {
    await saveMutation.mutateAsync(upstreams.filter((item) => item.id !== id));
  };

  const onSubmit = form.handleSubmit(async (value) => {
    const next = fromFormValue(value);
    const nextUpstreams = editingID ? upstreams.map((item) => (item.id === editingID ? next : item)) : [...upstreams, next];
    await saveMutation.mutateAsync(nextUpstreams);
    setDialogOpen(false);
    setEditingID(null);
    form.reset(defaultValues());
  });

  return (
    <>
      <Header fixed>
        <div className='flex flex-1 items-center justify-between gap-3'>
          <div>
            <h2 className='text-xl font-bold tracking-tight'>公益上游</h2>
            <p className='text-sm text-muted-foreground'>管理聚合的公益中转站上游、模型同步、健康检查和路由能力。</p>
          </div>
          <div className='flex gap-2'>
            <PermissionGuard requiredScope='write_settings'>
              <Button variant='outline' onClick={() => syncMutation.mutate()}>
                <IconRefresh className='mr-2 h-4 w-4' />
                全量同步
              </Button>
            </PermissionGuard>
            <PermissionGuard requiredScope='write_settings'>
              <Button onClick={() => setDialogOpen(true)}>
                <IconPlus className='mr-2 h-4 w-4' />
                添加上游
              </Button>
            </PermissionGuard>
          </div>
        </div>
      </Header>

      <Main fixed>
        <div className='grid gap-4 lg:grid-cols-4'>
          <Card>
            <CardHeader><CardTitle>上游总数</CardTitle><CardDescription>当前配置的公益站上游</CardDescription></CardHeader>
            <CardContent className='text-3xl font-semibold'>{upstreams.length}</CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle>启用上游</CardTitle><CardDescription>当前可参与调度的上游</CardDescription></CardHeader>
            <CardContent className='text-3xl font-semibold'>{upstreams.filter((item) => item.enabled).length}</CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle>Claude 能力</CardTitle><CardDescription>支持 Claude 模型族的上游</CardDescription></CardHeader>
            <CardContent className='text-3xl font-semibold'>{upstreams.filter((item) => item.supports_claude).length}</CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle>Codex 能力</CardTitle><CardDescription>支持 Codex 模型族的上游</CardDescription></CardHeader>
            <CardContent className='text-3xl font-semibold'>{upstreams.filter((item) => item.supports_codex).length}</CardContent>
          </Card>
        </div>

        <div className='mt-4 rounded-2xl border bg-background'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>路由模式</TableHead>
                <TableHead>权重</TableHead>
                <TableHead>能力</TableHead>
                <TableHead>健康</TableHead>
                <TableHead>模型数</TableHead>
                <TableHead className='text-right'>操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {upstreams.map((item) => {
                const runtime = runtimeByID.get(item.id);
                const abilities = [item.supports_claude && 'Claude', item.supports_codex && 'Codex', item.supports_gemini && 'Gemini', item.supports_opencode && 'OpenCode'].filter(Boolean);
                return (
                  <TableRow key={item.id}>
                    <TableCell>
                      <div className='font-medium'>{item.name}</div>
                      <div className='text-xs text-muted-foreground'>{item.base_url}</div>
                    </TableCell>
                    <TableCell>{item.route_mode}</TableCell>
                    <TableCell>{item.weight}</TableCell>
                    <TableCell>{abilities.length > 0 ? abilities.join(', ') : '-'}</TableCell>
                    <TableCell>
                      <Badge variant={runtime?.health_status === 'healthy' ? 'default' : runtime?.health_status === 'error' ? 'destructive' : 'secondary'}>
                        {runtime?.health_status || 'unknown'}
                      </Badge>
                    </TableCell>
                    <TableCell>{runtime?.available_models?.length || 0}</TableCell>
                    <TableCell className='text-right'>
                      <div className='flex justify-end gap-2'>
                        <PermissionGuard requiredScope='write_settings'>
                          <Button variant='outline' size='sm' onClick={() => refreshMutation.mutate(item.id)}>
                            <IconHeartbeat className='mr-1 h-4 w-4' />
                            刷新
                          </Button>
                        </PermissionGuard>
                        <PermissionGuard requiredScope='write_settings'>
                          <Button variant='outline' size='sm' onClick={() => handleEdit(item)}>
                            <IconEdit className='mr-1 h-4 w-4' />
                            编辑
                          </Button>
                        </PermissionGuard>
                        <PermissionGuard requiredScope='write_settings'>
                          <Button variant='destructive' size='sm' onClick={() => handleDelete(item.id)}>
                            删除
                          </Button>
                        </PermissionGuard>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
              {upstreams.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className='h-28 text-center text-muted-foreground'>
                    暂无公益上游配置
                  </TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </div>
      </Main>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className='max-h-[90vh] overflow-auto sm:max-w-3xl'>
          <DialogHeader>
            <DialogTitle>{editingID ? '编辑上游' : '添加上游'}</DialogTitle>
            <DialogDescription>配置公益中转站的 `baseURL + apiKey`、健康检查和模型能力，用于统一聚合出口调度。</DialogDescription>
          </DialogHeader>
          <Form {...form}>
            <form onSubmit={onSubmit} className='grid gap-4'>
              <div className='grid gap-4 md:grid-cols-2'>
                <FormField control={form.control} name='id' render={({ field }) => <FormItem><FormLabel>ID</FormLabel><FormControl><Input {...field} disabled={Boolean(editingID)} /></FormControl><FormMessage /></FormItem>} />
                <FormField control={form.control} name='name' render={({ field }) => <FormItem><FormLabel>名称</FormLabel><FormControl><Input {...field} /></FormControl><FormMessage /></FormItem>} />
              </div>
              <FormField control={form.control} name='base_url' render={({ field }) => <FormItem><FormLabel>Base URL</FormLabel><FormControl><Input {...field} /></FormControl><FormMessage /></FormItem>} />
              <FormField control={form.control} name='api_key' render={({ field }) => <FormItem><FormLabel>API Key</FormLabel><FormControl><Input {...field} /></FormControl><FormMessage /></FormItem>} />

              <div className='grid gap-4 md:grid-cols-2'>
                <FormField control={form.control} name='route_mode' render={({ field }) => <FormItem><FormLabel>路由模式</FormLabel><FormControl><Input {...field} /></FormControl><FormMessage /></FormItem>} />
                <FormField control={form.control} name='weight' render={({ field }) => <FormItem><FormLabel>权重</FormLabel><FormControl><Input type='number' {...field} /></FormControl><FormMessage /></FormItem>} />
                <FormField control={form.control} name='discover_models_cron' render={({ field }) => <FormItem><FormLabel>模型同步 Cron</FormLabel><FormControl><Input {...field} /></FormControl><FormMessage /></FormItem>} />
                <FormField control={form.control} name='preferred_model_family' render={({ field }) => <FormItem><FormLabel>偏好模型族</FormLabel><FormControl><Input {...field} placeholder='claude / codex / gemini' /></FormControl><FormMessage /></FormItem>} />
                <FormField control={form.control} name='health_check_path' render={({ field }) => <FormItem><FormLabel>健康检查路径</FormLabel><FormControl><Input {...field} /></FormControl><FormMessage /></FormItem>} />
                <FormField control={form.control} name='health_check_model' render={({ field }) => <FormItem><FormLabel>健康检查模型</FormLabel><FormControl><Input {...field} /></FormControl><FormMessage /></FormItem>} />
              </div>

              <div className='grid gap-4 md:grid-cols-2'>
                <FormField control={form.control} name='failure_threshold' render={({ field }) => <FormItem><FormLabel>失败阈值</FormLabel><FormControl><Input type='number' {...field} /></FormControl><FormMessage /></FormItem>} />
                <FormField control={form.control} name='recover_threshold' render={({ field }) => <FormItem><FormLabel>恢复阈值</FormLabel><FormControl><Input type='number' {...field} /></FormControl><FormMessage /></FormItem>} />
              </div>

              <div className='grid gap-4 md:grid-cols-2'>
                <FormField control={form.control} name='enabled' render={({ field }) => <FormItem className='flex items-center justify-between rounded-lg border p-3'><FormLabel>启用上游</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} /></FormControl></FormItem>} />
                <FormField control={form.control} name='auto_discover_models' render={({ field }) => <FormItem className='flex items-center justify-between rounded-lg border p-3'><FormLabel>自动发现模型</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} /></FormControl></FormItem>} />
              </div>

              <div className='grid gap-4 md:grid-cols-2'>
                <FormField control={form.control} name='supports_claude' render={({ field }) => <FormItem className='flex items-center justify-between rounded-lg border p-3'><FormLabel>支持 Claude</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} /></FormControl></FormItem>} />
                <FormField control={form.control} name='supports_codex' render={({ field }) => <FormItem className='flex items-center justify-between rounded-lg border p-3'><FormLabel>支持 Codex</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} /></FormControl></FormItem>} />
                <FormField control={form.control} name='supports_gemini' render={({ field }) => <FormItem className='flex items-center justify-between rounded-lg border p-3'><FormLabel>支持 Gemini</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} /></FormControl></FormItem>} />
                <FormField control={form.control} name='supports_opencode' render={({ field }) => <FormItem className='flex items-center justify-between rounded-lg border p-3'><FormLabel>支持 OpenCode</FormLabel><FormControl><Switch checked={field.value} onCheckedChange={field.onChange} /></FormControl></FormItem>} />
              </div>

              <FormField control={form.control} name='model_allowlist' render={({ field }) => <FormItem><FormLabel>模型白名单</FormLabel><FormControl><Textarea {...field} rows={4} placeholder={'claude-sonnet-4\\ngpt-5.4-codex'} /></FormControl><FormMessage /></FormItem>} />
              <FormField control={form.control} name='model_blocklist' render={({ field }) => <FormItem><FormLabel>模型黑名单</FormLabel><FormControl><Textarea {...field} rows={4} /></FormControl><FormMessage /></FormItem>} />
              <FormField control={form.control} name='remark' render={({ field }) => <FormItem><FormLabel>备注</FormLabel><FormControl><Textarea {...field} rows={3} /></FormControl><FormMessage /></FormItem>} />

              <DialogFooter>
                <Button type='button' variant='outline' onClick={() => setDialogOpen(false)}>取消</Button>
                <Button type='submit' disabled={saveMutation.isPending}>
                  <IconDeviceFloppy className='mr-2 h-4 w-4' />
                  保存
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>
    </>
  );
}
