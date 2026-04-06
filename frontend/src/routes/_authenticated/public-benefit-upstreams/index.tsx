import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import PublicBenefitUpstreamsManagement from '@/features/public-benefit-upstreams';

function ProtectedPublicBenefitUpstreams() {
  return (
    <RouteGuard requiredScopes={['read_settings']}>
      <PublicBenefitUpstreamsManagement />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/public-benefit-upstreams/')({
  component: ProtectedPublicBenefitUpstreams,
});
