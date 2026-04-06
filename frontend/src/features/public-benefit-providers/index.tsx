import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useForm } from 'react-hook-form';
import { z } from 'zod';
import { zodResolver } from '@hookform/resolvers/zod';
import { IconPlus, IconRefresh, IconRosetteDiscountCheck, IconDeviceFloppy, IconEdit, IconPlugConnectedX } from '@tabler/icons-react';
import { toast } from 'sonner';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Form, FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { apiRequest } from '@/lib/api-client';
import { PermissionGuard } from '@/components/permission-guard';

type PublicBenefitProviderKind = 'new_api' | 'one_api' | 'one_hub' | 'done_hub' | 'anyrouter' | 'cubence' | 'nekocode' | 'sub2api' | 'yescode' | 'generic';
type PublicBenefitAuthType = 'api_key' | 'cookie' | 'token' | 'password' | 'mixed';
type ProviderTemplate = 'new_api' | 'anyrouter' | 'cubence' | 'nekocode' | 'sub2api' | 'yescode' | 'generic';

type PublicBenefitProviderAccount = {
  id: string;
  name: string;
  kind: PublicBenefitProviderKind;
  base_url: string;
  auth_type: PublicBenefitAuthType;
  username?: string;
  password?: string;
  cookie?: string;
  token?: string;
  api_key?: string;
  enabled: boolean;
  auto_check_in: boolean;
  check_in_cron?: string;
  balance_path?: string;
  check_in_path?: string;
  remark?: string;
  extra?: Record<string, unknown>;
};

type PublicBenefitProviderRuntime = {
  provider_id: string;
  last_balance_at?: string;
  last_check_in_at?: string;
  last_check_in_status?: string;
  balance: number;
  currency?: string;
  total_usage: number;
  account_name?: string;
  last_error?: string;
};

type PublicBenefitUpstreamSite = Record<string, unknown>;

type PublicBenefitHubConfig = {
  providers: PublicBenefitProviderAccount[];
  upstreams: PublicBenefitUpstreamSite[];
  outbound: Record<string, unknown>;
};

type PublicBenefitRuntimeState = {
  providers: PublicBenefitProviderRuntime[];
  upstreams: unknown[];
  daily_usage: unknown[];
};

type ParseProviderIdentityResponse = {
  user_id?: string;
  source?: string;
};

type ProviderTemplateConfig = {
  label: string;
  kind: PublicBenefitProviderKind;
  authType: PublicBenefitAuthType;
  balancePath: string;
  checkInPath: string;
  cookieLabel?: string;
  apiKeyLabel?: string;
  usernameLabel?: string;
  usernameDescription?: string;
  apiKeyDescription?: string;
  cookieDescription?: string;
  showCookie: boolean;
  showAPIKey: boolean;
  showUsername: boolean;
  showToken: boolean;
  autoParseUserIDFromCookie?: boolean;
};

const providerTemplates: Record<ProviderTemplate, ProviderTemplateConfig> = {
  new_api: {
    label: 'New API',
    kind: 'new_api',
    authType: 'mixed',
    balancePath: '/api/user/self',
    checkInPath: '/api/user/checkin',
    cookieLabel: 'Cookie',
    apiKeyLabel: '访问令牌（API Key）',
    usernameLabel: '用户 ID',
    usernameDescription: '部分 New API 站点签到需要用户 ID；通常可从站点后台或 Cookie 解析获得。',
    apiKeyDescription: '用于余额查询；部分站点同时支持签到。',
    cookieDescription: '推荐填写完整 Cookie。Cookie 可用于签到，部分站点也要求查询时携带。',
    showCookie: true,
    showAPIKey: true,
    showUsername: true,
    showToken: false,
    autoParseUserIDFromCookie: true,
  },
  anyrouter: {
    label: 'Anyrouter',
    kind: 'anyrouter',
    authType: 'mixed',
    balancePath: '/api/user/self',
    checkInPath: '/api/user/sign_in',
    apiKeyLabel: '访问令牌（API Key）',
    cookieLabel: 'Cookie',
    showCookie: true,
    showAPIKey: true,
    showUsername: false,
    showToken: false,
  },
  cubence: {
    label: 'Cubence',
    kind: 'cubence',
    authType: 'cookie',
    balancePath: '/api/v1/dashboard/overview',
    checkInPath: '',
    cookieLabel: 'Token Cookie',
    cookieDescription: '从浏览器复制完整 Cookie，至少应包含 `token=...`。',
    showCookie: true,
    showAPIKey: false,
    showUsername: false,
    showToken: false,
  },
  nekocode: {
    label: 'NekoCode',
    kind: 'nekocode',
    authType: 'cookie',
    balancePath: '/api/usage/summary',
    checkInPath: '',
    cookieLabel: 'Session Cookie',
    cookieDescription: '从浏览器复制完整 Cookie，至少应包含 `session=...`。',
    showCookie: true,
    showAPIKey: false,
    showUsername: false,
    showToken: false,
  },
  sub2api: {
    label: 'Sub2API',
    kind: 'sub2api',
    authType: 'mixed',
    balancePath: '/api/v1/auth/me?timezone=Asia/Shanghai',
    checkInPath: '/api/v1/user/checkin',
    apiKeyLabel: '访问令牌（API Key）',
    cookieLabel: 'Cookie',
    showCookie: true,
    showAPIKey: true,
    showUsername: false,
    showToken: false,
  },
  yescode: {
    label: 'YesCode',
    kind: 'yescode',
    authType: 'cookie',
    balancePath: '/api/v1/user/balance',
    checkInPath: '',
    cookieLabel: 'Auth Cookie',
    cookieDescription: '从浏览器复制完整 Cookie，建议包含 `yescode_auth` 和 `yescode_csrf`。',
    showCookie: true,
    showAPIKey: false,
    showUsername: false,
    showToken: false,
  },
  generic: {
    label: '通用站点',
    kind: 'generic',
    authType: 'mixed',
    balancePath: '/api/user/self',
    checkInPath: '',
    apiKeyLabel: 'API Key / Token',
    cookieLabel: 'Cookie',
    usernameLabel: '用户名 / 用户 ID',
    showCookie: true,
    showAPIKey: true,
    showUsername: true,
    showToken: false,
  },
};

const providerFormSchema = z.object({
  id: z.string().min(1, 'ID is required'),
  name: z.string().min(1, 'Name is required'),
  template: z.enum(['new_api', 'anyrouter', 'cubence', 'nekocode', 'sub2api', 'yescode', 'generic']),
  base_url: z.string().min(1, 'Base URL is required'),
  username: z.string().optional(),
  password: z.string().optional(),
  cookie: z.string().optional(),
  token: z.string().optional(),
  api_key: z.string().optional(),
  enabled: z.boolean(),
  auto_check_in: z.boolean(),
  remark: z.string().optional(),
});

type ProviderFormValues = z.infer<typeof providerFormSchema>;

const queryKey = ['public-benefit-providers'];

async function fetchPublicBenefitConfig() {
  return apiRequest<PublicBenefitHubConfig>('/admin/public-benefit/config', { requireAuth: true });
}

async function fetchPublicBenefitRuntime() {
  return apiRequest<PublicBenefitRuntimeState>('/admin/public-benefit/runtime', { requireAuth: true });
}

async function parseProviderIdentity(payload: { template: ProviderTemplate; base_url: string; cookie: string; api_key?: string }) {
  return apiRequest<ParseProviderIdentityResponse>('/admin/public-benefit/providers/parse-identity', {
    method: 'POST',
    requireAuth: true,
    body: payload,
  });
}

function usePublicBenefitConfig() {
  return useQuery({
    queryKey: [...queryKey, 'config'],
    queryFn: fetchPublicBenefitConfig,
  });
}

function usePublicBenefitRuntime() {
  return useQuery({
    queryKey: [...queryKey, 'runtime'],
    queryFn: fetchPublicBenefitRuntime,
    refetchInterval: 30_000,
  });
}

function templateFromProvider(provider: PublicBenefitProviderAccount): ProviderTemplate {
  const storedTemplate = typeof provider.extra?.provider_template === 'string' ? provider.extra.provider_template : '';
  if (storedTemplate === 'new_api' || storedTemplate === 'anyrouter' || storedTemplate === 'cubence' || storedTemplate === 'nekocode' || storedTemplate === 'sub2api' || storedTemplate === 'yescode' || storedTemplate === 'generic') {
    return storedTemplate;
  }

  switch (provider.kind) {
    case 'new_api':
    case 'one_api':
    case 'one_hub':
    case 'done_hub':
      return 'new_api';
    case 'anyrouter':
      return 'anyrouter';
    case 'cubence':
      return 'cubence';
    case 'nekocode':
      return 'nekocode';
    case 'sub2api':
      return 'sub2api';
    case 'yescode':
      return 'yescode';
    default:
      return 'generic';
  }
}

function defaultProviderValues(): ProviderFormValues {
  return {
    id: '',
    name: '',
    template: 'new_api',
    base_url: '',
    username: '',
    password: '',
    cookie: '',
    token: '',
    api_key: '',
    enabled: true,
    auto_check_in: true,
    remark: '',
  };
}

function providerToFormValue(provider: PublicBenefitProviderAccount): ProviderFormValues {
  return {
    id: provider.id,
    name: provider.name,
    template: templateFromProvider(provider),
    base_url: provider.base_url,
    username: provider.username || '',
    password: provider.password || '',
    cookie: provider.cookie || '',
    token: provider.token || '',
    api_key: provider.api_key || '',
    enabled: provider.enabled,
    auto_check_in: provider.auto_check_in,
    remark: provider.remark || '',
  };
}

function formValueToProvider(values: ProviderFormValues): PublicBenefitProviderAccount {
  const template = providerTemplates[values.template];
  const extra: Record<string, unknown> = {
    provider_template: values.template,
  };

  if (values.template === 'cubence') {
    extra.balance_paths = ['data.balance.total_balance_dollar'];
  }
  if (values.template === 'nekocode') {
    extra.balance_paths = ['data.balance', 'balance'];
    extra.usage_paths = ['data.subscription.daily_used_quota'];
  }
  if (values.template === 'yescode') {
    extra.balance_paths = ['total_balance', 'data.total_balance', 'pay_as_you_go_balance'];
    extra.usage_paths = ['weekly_spent_balance', 'data.weekly_spent_balance'];
  }

  return {
    id: values.id.trim(),
    name: values.name.trim(),
    kind: template.kind,
    base_url: normalizeBaseURL(values.base_url),
    auth_type: template.authType,
    username: values.username?.trim() || '',
    password: values.password?.trim() || '',
    cookie: values.cookie?.trim() || '',
    token: values.token?.trim() || '',
    api_key: values.api_key?.trim() || '',
    enabled: values.enabled,
    auto_check_in: values.auto_check_in,
    check_in_cron: values.auto_check_in ? '0 9 * * *' : '',
    balance_path: template.balancePath,
    check_in_path: values.auto_check_in ? template.checkInPath : '',
    remark: values.remark?.trim() || '',
    extra,
  };
}

function parseNewAPIUserID(cookie: string): string | null {
  try {
    let sessionValue = cookie.trim();
    if (!sessionValue) return null;

    if (sessionValue.includes('session=')) {
      const match = sessionValue.match(/session=([^;]+)/);
      if (match) {
        sessionValue = match[1];
      }
    }

    sessionValue = sessionValue.replace(/\s+/g, '');

    const padding = 4 - (sessionValue.length % 4);
    if (padding !== 4) {
      sessionValue += '='.repeat(padding);
    }
    const decoded = atob(sessionValue.replace(/-/g, '+').replace(/_/g, '/'));
    const parts = decoded.split('|');
    if (parts.length < 2) return null;

    let gobBase64 = parts[1];
    gobBase64 = gobBase64.replace(/\s+/g, '');
    const gobPadding = 4 - (gobBase64.length % 4);
    if (gobPadding !== 4) {
      gobBase64 += '='.repeat(gobPadding);
    }
    const gobData = atob(gobBase64.replace(/-/g, '+').replace(/_/g, '/'));

    const idPattern = '\x02id\x03int';
    const idIndex = gobData.indexOf(idPattern);
    if (idIndex === -1) return null;

    const valueStart = idIndex + 7 + 2;
    if (valueStart >= gobData.length || gobData.charCodeAt(valueStart) !== 0) {
      return null;
    }

    const marker = gobData.charCodeAt(valueStart + 1);
    if (marker < 0x80) return null;

    const length = 256 - marker;
    if (valueStart + 2 + length > gobData.length) return null;

    let value = 0;
    for (let i = 0; i < length; i += 1) {
      value = (value << 8) | gobData.charCodeAt(valueStart + 2 + i);
    }

    return String(value >> 1);
  } catch {
    return null;
  }
}

function normalizeBaseURL(value: string) {
  return value.trim().replace(/\/+$/, '');
}

function statusBadge(status?: string, fallback = 'unknown') {
  const normalized = (status || fallback).toLowerCase();
  if (normalized === 'ok' || normalized === 'healthy') return 'default';
  if (normalized === 'failed' || normalized === 'error') return 'destructive';
  return 'secondary';
}

function renderCheckInStatus(runtime?: PublicBenefitProviderRuntime, provider?: PublicBenefitProviderAccount) {
  if (!provider?.auto_check_in) {
    return '未启用';
  }
  if (runtime?.last_check_in_status) {
    return runtime.last_check_in_status;
  }
  if (runtime?.last_check_in_at) {
    return '未知';
  }
  return '未执行';
}

function maskValue(value?: string) {
  if (!value) return '-';
  if (value.length <= 10) return value;
  return `${value.slice(0, 4)}****${value.slice(-4)}`;
}

export default function PublicBenefitProvidersManagement() {
  const queryClient = useQueryClient();
  const configQuery = usePublicBenefitConfig();
  const runtimeQuery = usePublicBenefitRuntime();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingProviderID, setEditingProviderID] = useState<string | null>(null);

  const form = useForm<ProviderFormValues>({
    resolver: zodResolver(providerFormSchema),
    defaultValues: defaultProviderValues(),
  });

  const selectedTemplate = form.watch('template');
  const selectedTemplateConfig = providerTemplates[selectedTemplate];
  const cookieValue = form.watch('cookie');
  const apiKeyValue = form.watch('api_key');
  const baseURLValue = form.watch('base_url');

  const providers = configQuery.data?.providers || [];
  const runtimeByProviderID = useMemo(() => {
    const entries = runtimeQuery.data?.providers || [];
    return new Map(entries.map((item) => [item.provider_id, item]));
  }, [runtimeQuery.data?.providers]);

  useEffect(() => {
    if (!dialogOpen) {
      form.reset(defaultProviderValues());
      setEditingProviderID(null);
    }
  }, [dialogOpen, form]);

  useEffect(() => {
    if (!dialogOpen || !selectedTemplateConfig.autoParseUserIDFromCookie) {
      return;
    }

    const currentUserID = form.getValues('username')?.trim();
    if (currentUserID) {
      return;
    }

    const parsedUserID = parseNewAPIUserID(cookieValue || '');
    if (parsedUserID) {
      form.setValue('username', parsedUserID, {
        shouldDirty: true,
        shouldTouch: true,
        shouldValidate: true,
      });
      return;
    }

    let cancelled = false;
    void parseProviderIdentity({
      template: selectedTemplate,
      base_url: baseURLValue || '',
      cookie: cookieValue || '',
      api_key: apiKeyValue || '',
    })
      .then((result) => {
        const userID = result.user_id?.trim();
        if (!cancelled && userID && !form.getValues('username')?.trim()) {
          form.setValue('username', userID, {
            shouldDirty: true,
            shouldTouch: true,
            shouldValidate: true,
          });
        }
      })
      .catch(() => {});

    return () => {
      cancelled = true;
    };
  }, [apiKeyValue, baseURLValue, cookieValue, dialogOpen, form, selectedTemplate, selectedTemplateConfig.autoParseUserIDFromCookie]);

  const saveMutation = useMutation({
    mutationFn: async (nextProviders: PublicBenefitProviderAccount[]) => {
      const current = await fetchPublicBenefitConfig();
      return apiRequest('/admin/public-benefit/config', {
        method: 'PUT',
        requireAuth: true,
        body: {
          ...current,
          providers: nextProviders,
        },
      });
    },
    onSuccess: async () => {
      toast.success('供应商配置已保存');
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: [...queryKey, 'config'] }),
        queryClient.invalidateQueries({ queryKey: [...queryKey, 'runtime'] }),
      ]);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : '保存供应商配置失败');
    },
  });

  const syncMutation = useMutation({
    mutationFn: () => apiRequest('/admin/public-benefit/sync', { method: 'POST', requireAuth: true }),
    onSuccess: async () => {
      toast.success('已触发公益站同步');
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: [...queryKey, 'config'] }),
        queryClient.invalidateQueries({ queryKey: [...queryKey, 'runtime'] }),
      ]);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : '同步失败');
    },
  });

  const refreshProviderMutation = useMutation({
    mutationFn: (providerID: string) => apiRequest(`/admin/public-benefit/providers/${providerID}/refresh`, { method: 'POST', requireAuth: true }),
    onSuccess: async () => {
      toast.success('已刷新供应商余额');
      await queryClient.invalidateQueries({ queryKey: [...queryKey, 'runtime'] });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : '刷新供应商失败');
    },
  });

  const checkInProviderMutation = useMutation({
    mutationFn: (providerID: string) => apiRequest(`/admin/public-benefit/providers/${providerID}/checkin`, { method: 'POST', requireAuth: true }),
    onSuccess: async () => {
      toast.success('已执行供应商签到');
      await queryClient.invalidateQueries({ queryKey: [...queryKey, 'runtime'] });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : '供应商签到失败');
    },
  });

  const handleEdit = (provider: PublicBenefitProviderAccount) => {
    setEditingProviderID(provider.id);
    form.reset(providerToFormValue(provider));
    setDialogOpen(true);
  };

  const handleAdd = () => {
    setEditingProviderID(null);
    form.reset(defaultProviderValues());
    setDialogOpen(true);
  };

  const handleDelete = async (providerID: string) => {
    const nextProviders = providers.filter((item) => item.id !== providerID);
    await saveMutation.mutateAsync(nextProviders);
  };

  const onSubmit = form.handleSubmit(async (values) => {
    const nextProvider = formValueToProvider(values);
    const nextProviders = editingProviderID
      ? providers.map((item) => (item.id === editingProviderID ? nextProvider : item))
      : [...providers, nextProvider];

    await saveMutation.mutateAsync(nextProviders);
    setDialogOpen(false);
  });

  return (
    <>
      <Header fixed>
        <div className='flex flex-1 items-center justify-between gap-3'>
          <div>
            <h2 className='text-xl font-bold tracking-tight'>公益供应商</h2>
            <p className='text-sm text-muted-foreground'>管理公益站账号认证信息，用于余额查询和自动签到。</p>
          </div>
          <div className='flex gap-2'>
            <PermissionGuard requiredScope='write_settings'>
              <Button variant='outline' onClick={() => syncMutation.mutate()} disabled={syncMutation.isPending}>
                <IconRefresh className='mr-2 h-4 w-4' />
                全量同步
              </Button>
            </PermissionGuard>
            <PermissionGuard requiredScope='write_settings'>
              <Button onClick={handleAdd}>
                <IconPlus className='mr-2 h-4 w-4' />
                添加供应商
              </Button>
            </PermissionGuard>
          </div>
        </div>
      </Header>

      <Main fixed>
        <div className='grid gap-4 lg:grid-cols-4'>
          <Card>
            <CardHeader>
              <CardTitle>供应商总数</CardTitle>
              <CardDescription>当前配置的公益供应商数量</CardDescription>
            </CardHeader>
            <CardContent className='text-3xl font-semibold'>{providers.length}</CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>已启用</CardTitle>
              <CardDescription>当前可参与采集与签到的供应商</CardDescription>
            </CardHeader>
            <CardContent className='text-3xl font-semibold'>{providers.filter((item) => item.enabled).length}</CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>自动签到</CardTitle>
              <CardDescription>启用了自动签到计划的供应商</CardDescription>
            </CardHeader>
            <CardContent className='text-3xl font-semibold'>{providers.filter((item) => item.auto_check_in).length}</CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>异常数量</CardTitle>
              <CardDescription>最近一次同步或签到报错的供应商</CardDescription>
            </CardHeader>
            <CardContent className='text-3xl font-semibold'>
              {[...runtimeByProviderID.values()].filter((item) => item.last_error).length}
            </CardContent>
          </Card>
        </div>

        <div className='mt-4 rounded-2xl border bg-background'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>模板</TableHead>
                <TableHead>认证</TableHead>
                <TableHead>余额</TableHead>
                <TableHead>总用量</TableHead>
                <TableHead>签到状态</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className='text-right'>操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {providers.map((provider) => {
                const runtime = runtimeByProviderID.get(provider.id);
                const template = providerTemplates[templateFromProvider(provider)];
                return (
                  <TableRow key={provider.id}>
                    <TableCell>
                      <div className='font-medium'>{provider.name}</div>
                      <div className='text-xs text-muted-foreground'>{provider.base_url}</div>
                    </TableCell>
                    <TableCell>{template.label}</TableCell>
                    <TableCell>
                      <div className='text-sm'>{maskValue(provider.api_key || provider.token || provider.cookie)}</div>
                    </TableCell>
                    <TableCell>{runtime ? `${runtime.balance ?? 0} ${runtime.currency || ''}`.trim() : '-'}</TableCell>
                    <TableCell>{runtime?.total_usage ?? 0}</TableCell>
                    <TableCell>
                      <Badge variant={statusBadge(runtime?.last_check_in_status, provider.auto_check_in ? 'unknown' : 'disabled')}>
                        {renderCheckInStatus(runtime, provider)}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <div className='flex flex-col gap-1'>
                        <Badge variant={provider.enabled ? 'default' : 'secondary'}>{provider.enabled ? 'enabled' : 'disabled'}</Badge>
                        {runtime?.last_error ? (
                          <span className='max-w-64 truncate text-xs text-destructive' title={runtime.last_error}>
                            {runtime.last_error}
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className='text-right'>
                      <div className='flex justify-end gap-2'>
                        <PermissionGuard requiredScope='write_settings'>
                          <Button variant='outline' size='sm' onClick={() => refreshProviderMutation.mutate(provider.id)}>
                            <IconRefresh className='mr-1 h-4 w-4' />
                            刷新
                          </Button>
                        </PermissionGuard>
                        <PermissionGuard requiredScope='write_settings'>
                          <Button variant='outline' size='sm' onClick={() => checkInProviderMutation.mutate(provider.id)}>
                            <IconRosetteDiscountCheck className='mr-1 h-4 w-4' />
                            签到
                          </Button>
                        </PermissionGuard>
                        <PermissionGuard requiredScope='write_settings'>
                          <Button variant='outline' size='sm' onClick={() => handleEdit(provider)}>
                            <IconEdit className='mr-1 h-4 w-4' />
                            编辑
                          </Button>
                        </PermissionGuard>
                        <PermissionGuard requiredScope='write_settings'>
                          <Button variant='destructive' size='sm' onClick={() => handleDelete(provider.id)}>
                            <IconPlugConnectedX className='mr-1 h-4 w-4' />
                            删除
                          </Button>
                        </PermissionGuard>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
              {providers.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className='h-28 text-center text-muted-foreground'>
                    暂无供应商配置
                  </TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </div>
      </Main>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className='max-h-[90vh] overflow-auto sm:max-w-2xl'>
          <DialogHeader>
            <DialogTitle>{editingProviderID ? '用户认证' : '添加供应商'}</DialogTitle>
            <DialogDescription>配置提供商的用户认证信息，用于余额查询和签到操作。</DialogDescription>
          </DialogHeader>
          <Form {...form}>
            <form onSubmit={onSubmit} className='grid gap-4'>
              <div className='grid gap-4 md:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='id'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>ID</FormLabel>
                      <FormControl>
                        <Input {...field} disabled={Boolean(editingProviderID)} placeholder='muyuando' />
                      </FormControl>
                      <FormDescription>用于内部标识，建议使用英文或数字。</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='name'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>名称</FormLabel>
                      <FormControl>
                        <Input {...field} placeholder='木圆公益站' />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <FormField
                control={form.control}
                name='template'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>认证模板</FormLabel>
                    <Select value={field.value} onValueChange={field.onChange}>
                      <FormControl>
                        <SelectTrigger className='w-full'>
                          <SelectValue placeholder='选择供应商模板' />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectItem value='anyrouter'>Anyrouter</SelectItem>
                        <SelectItem value='cubence'>Cubence</SelectItem>
                        <SelectItem value='nekocode'>NekoCode</SelectItem>
                        <SelectItem value='new_api'>New API</SelectItem>
                        <SelectItem value='sub2api'>Sub2API</SelectItem>
                        <SelectItem value='yescode'>YesCode</SelectItem>
                        <SelectItem value='generic'>通用站点</SelectItem>
                      </SelectContent>
                    </Select>
                    <FormDescription>按站点类型自动套用余额查询路径和签到路径，减少手工填写。</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='base_url'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>站点地址</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder='https://muyuan.do' />
                    </FormControl>
                    <FormDescription>只填站点根地址，不要带 `/api/user/self` 这类路径。</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {selectedTemplateConfig.showCookie ? (
                <FormField
                  control={form.control}
                  name='cookie'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{selectedTemplateConfig.cookieLabel || 'Cookie'}</FormLabel>
                      <FormControl>
                        <Textarea {...field} rows={4} placeholder='session=xxxx; path=/; ...' />
                      </FormControl>
                      <FormDescription>{selectedTemplateConfig.cookieDescription || '从浏览器开发者工具复制完整 Cookie。'}</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ) : null}

              <div
                className={
                  selectedTemplateConfig.showAPIKey && selectedTemplateConfig.showUsername
                    ? 'grid gap-4 md:grid-cols-[minmax(0,3fr)_minmax(0,1fr)]'
                    : 'grid gap-4 md:grid-cols-2'
                }
              >
                {selectedTemplateConfig.showAPIKey ? (
                  <FormField
                    control={form.control}
                    name='api_key'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{selectedTemplateConfig.apiKeyLabel || 'API Key'}</FormLabel>
                        <FormControl>
                          <Input {...field} placeholder='sk-xxxx 或站点 API Key' />
                        </FormControl>
                        <FormDescription>{selectedTemplateConfig.apiKeyDescription || '用于余额查询和部分站点的签到。'}</FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                ) : null}

                {selectedTemplateConfig.showUsername ? (
                  <FormField
                    control={form.control}
                    name='username'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{selectedTemplateConfig.usernameLabel || '用户名'}</FormLabel>
                        <FormControl>
                          <Input {...field} placeholder='例如 259 或 zhiq' />
                        </FormControl>
                        <FormDescription>{selectedTemplateConfig.usernameDescription || '仅部分模板需要此字段。'}</FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                ) : null}

                {selectedTemplateConfig.showToken ? (
                  <FormField
                    control={form.control}
                    name='token'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>Token</FormLabel>
                        <FormControl>
                          <Input {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                ) : null}
              </div>

              <div className='grid gap-4 md:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='enabled'
                  render={({ field }) => (
                    <FormItem className='flex items-center justify-between rounded-lg border p-3'>
                      <div>
                        <FormLabel>启用供应商</FormLabel>
                        <FormDescription>关闭后不会参与余额采集和签到。</FormDescription>
                      </div>
                      <FormControl>
                        <Switch checked={field.value} onCheckedChange={field.onChange} />
                      </FormControl>
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='auto_check_in'
                  render={({ field }) => (
                    <FormItem className='flex items-center justify-between rounded-lg border p-3'>
                      <div>
                        <FormLabel>自动签到</FormLabel>
                        <FormDescription>按默认每日任务执行签到。</FormDescription>
                      </div>
                      <FormControl>
                        <Switch checked={field.value} onCheckedChange={field.onChange} />
                      </FormControl>
                    </FormItem>
                  )}
                />
              </div>

              <FormField
                control={form.control}
                name='remark'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>备注</FormLabel>
                    <FormControl>
                      <Textarea {...field} rows={2} placeholder='例如：Cookie 来自浏览器，用户 ID 为站点后台编号' />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <DialogFooter>
                <Button type='button' variant='outline' onClick={() => setDialogOpen(false)}>
                  取消
                </Button>
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
