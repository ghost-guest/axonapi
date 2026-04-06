import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import PublicBenefitOutboundManagement from '@/features/public-benefit-outbound';

function ProtectedPublicBenefitOutbound() {
  return (
    <RouteGuard requiredScopes={['read_settings']}>
      <PublicBenefitOutboundManagement />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/public-benefit-outbound/')({
  component: ProtectedPublicBenefitOutbound,
});
