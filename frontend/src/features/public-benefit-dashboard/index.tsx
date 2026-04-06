import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { IconArrowRight, IconHeartbeat, IconKey, IconPlugConnected, IconRoute2 } from '@tabler/icons-react';
import { Header } from '@/components/layout/header';
import { Main } from '@/components/layout/main';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { apiRequest } from '@/lib/api-client';

type PublicBenefitProviderRuntime = {
  provider_id: string;
  balance: number;
  currency?: string;
  total_usage: number;
  last_check_in_status?: string;
  last_error?: string;
};

type PublicBenefitUpstreamRuntime = {
  upstream_id: string;
  health_status?: string;
  available_models?: string[];
  total_requests: number;
  total_tokens: number;
  last_error?: string;
};

type PublicBenefitUsageSnapshot = {
  date: string;
  total_tokens: number;
  total_requests: number;
  used_upstreams?: string[];
};

type PublicBenefitDashboard = {
  provider_count: number;
  enabled_provider_count: number;
  upstream_count: number;
  enabled_upstream_count: number;
  healthy_upstream_count: number;
  total_balance: number;
  total_usage: number;
  total_requests: number;
  total_tokens: number;
  providers: PublicBenefitProviderRuntime[];
  upstreams: PublicBenefitUpstreamRuntime[];
  daily_usage: PublicBenefitUsageSnapshot[];
  outbound: {
    enabled: boolean;
    public_base_url?: string;
    session_affinity_enabled?: boolean;
    default_route_mode?: string;
  };
};

async function fetchDashboard() {
  return apiRequest<PublicBenefitDashboard>('/admin/public-benefit/dashboard', { requireAuth: true });
}

function healthBadge(status?: string) {
  const normalized = (status || 'unknown').toLowerCase();
  if (normalized === 'healthy' || normalized === 'ok') return 'default';
  if (normalized === 'error' || normalized === 'failed') return 'destructive';
  return 'secondary';
}

export default function PublicBenefitDashboardPage() {
  const dashboardQuery = useQuery({
    queryKey: ['public-benefit-dashboard'],
    queryFn: fetchDashboard,
    refetchInterval: 30_000,
  });

  const latestDailyUsage = useMemo(() => dashboardQuery.data?.daily_usage?.[0], [dashboardQuery.data?.daily_usage]);
  const riskyUpstreams = useMemo(() => (dashboardQuery.data?.upstreams || []).filter((item) => item.last_error || item.health_status !== 'healthy').slice(0, 5), [dashboardQuery.data?.upstreams]);
  const providerWarnings = useMemo(() => (dashboardQuery.data?.providers || []).filter((item) => item.last_error || item.last_check_in_status === 'failed').slice(0, 5), [dashboardQuery.data?.providers]);

  return (
    <>
      <Header fixed>
        <div className='flex flex-1 items-center justify-between gap-3'>
          <div>
            <h2 className='text-xl font-bold tracking-tight'>公益聚合总览</h2>
            <p className='text-sm text-muted-foreground'>查看供应商余额、上游健康、每日用量与统一出口运行状态。</p>
          </div>
          <div className='flex gap-2'>
            <Button asChild variant='outline'>
              <Link to='/public-benefit-providers'>
                供应商
                <IconArrowRight className='ml-2 h-4 w-4' />
              </Link>
            </Button>
            <Button asChild>
              <Link to='/public-benefit-outbound'>
                出口
                <IconArrowRight className='ml-2 h-4 w-4' />
              </Link>
            </Button>
          </div>
        </div>
      </Header>

      <Main fixed>
        <div className='grid gap-4 md:grid-cols-2 xl:grid-cols-4'>
          <Card>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'><IconKey className='h-5 w-5' />供应商</CardTitle>
              <CardDescription>已启用 / 总数</CardDescription>
            </CardHeader>
            <CardContent className='text-3xl font-semibold'>
              {dashboardQuery.data ? `${dashboardQuery.data.enabled_provider_count} / ${dashboardQuery.data.provider_count}` : '-'}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'><IconPlugConnected className='h-5 w-5' />聚合渠道</CardTitle>
              <CardDescription>当前参与聚合的渠道健康概览</CardDescription>
            </CardHeader>
            <CardContent className='text-3xl font-semibold'>
              {dashboardQuery.data ? `${dashboardQuery.data.healthy_upstream_count} / ${dashboardQuery.data.enabled_upstream_count}` : '-'}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'><IconRoute2 className='h-5 w-5' />总用量</CardTitle>
              <CardDescription>累计 requests / tokens</CardDescription>
            </CardHeader>
            <CardContent className='space-y-1'>
              <div className='text-3xl font-semibold'>{dashboardQuery.data?.total_requests ?? '-'}</div>
              <div className='text-sm text-muted-foreground'>{dashboardQuery.data?.total_tokens ?? '-'} tokens</div>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'><IconHeartbeat className='h-5 w-5' />统一出口</CardTitle>
              <CardDescription>路由与会话保持</CardDescription>
            </CardHeader>
            <CardContent className='space-y-2 text-sm'>
              <Badge variant={dashboardQuery.data?.outbound.enabled ? 'default' : 'secondary'}>
                {dashboardQuery.data?.outbound.enabled ? 'enabled' : 'disabled'}
              </Badge>
              <div className='text-muted-foreground'>route: {dashboardQuery.data?.outbound.default_route_mode || 'adaptive'}</div>
              <div className='text-muted-foreground'>session affinity: {dashboardQuery.data?.outbound.session_affinity_enabled ? 'on' : 'off'}</div>
            </CardContent>
          </Card>
        </div>

        <div className='mt-4 grid gap-4 lg:grid-cols-2'>
          <Card>
            <CardHeader>
              <CardTitle>每日用量</CardTitle>
              <CardDescription>最近一次汇总的公益聚合消耗。</CardDescription>
            </CardHeader>
            <CardContent className='space-y-2 text-sm'>
              <div>日期: {latestDailyUsage?.date || '-'}</div>
              <div>请求数: {latestDailyUsage?.total_requests ?? 0}</div>
              <div>Tokens: {latestDailyUsage?.total_tokens ?? 0}</div>
              <div className='truncate'>涉及上游: {latestDailyUsage?.used_upstreams?.join(', ') || '-'}</div>
              <div>总余额: {dashboardQuery.data?.total_balance ?? 0}</div>
              <div>总用量: {dashboardQuery.data?.total_usage ?? 0}</div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>供应商异常</CardTitle>
              <CardDescription>最近出现签到或余额同步问题的供应商。</CardDescription>
            </CardHeader>
            <CardContent className='space-y-3'>
              {providerWarnings.length === 0 ? <div className='text-sm text-muted-foreground'>暂无异常供应商。</div> : null}
              {providerWarnings.map((item) => (
                <div key={item.provider_id} className='rounded-lg border p-3 text-sm'>
                  <div className='flex items-center justify-between gap-3'>
                    <span className='font-medium'>{item.provider_id}</span>
                    <Badge variant={healthBadge(item.last_check_in_status)}>{item.last_check_in_status || 'unknown'}</Badge>
                  </div>
                  <div className='mt-1 text-muted-foreground'>balance: {item.balance} {item.currency || ''}</div>
                  {item.last_error ? <div className='mt-1 text-destructive'>{item.last_error}</div> : null}
                </div>
              ))}
            </CardContent>
          </Card>
        </div>

        <div className='mt-4'>
          <Card>
            <CardHeader>
              <CardTitle>聚合渠道健康</CardTitle>
              <CardDescription>这里展示当前参与自动轮换与无感切换的渠道运行状态。</CardDescription>
            </CardHeader>
            <CardContent className='grid gap-3 lg:grid-cols-2'>
              {(riskyUpstreams.length > 0 ? riskyUpstreams : (dashboardQuery.data?.upstreams || []).slice(0, 6)).map((item) => (
                <div key={item.upstream_id} className='rounded-lg border p-3 text-sm'>
                  <div className='flex items-center justify-between gap-3'>
                    <span className='font-medium'>{item.upstream_id}</span>
                    <Badge variant={healthBadge(item.health_status)}>{item.health_status || 'unknown'}</Badge>
                  </div>
                  <div className='mt-1 text-muted-foreground'>models: {item.available_models?.length || 0}</div>
                  <div className='text-muted-foreground'>requests: {item.total_requests} / tokens: {item.total_tokens}</div>
                  {item.last_error ? <div className='mt-1 text-destructive'>{item.last_error}</div> : null}
                </div>
              ))}
              {(dashboardQuery.data?.upstreams || []).length === 0 ? <div className='text-sm text-muted-foreground'>暂无上游数据。</div> : null}
            </CardContent>
          </Card>
        </div>
      </Main>
    </>
  );
}
