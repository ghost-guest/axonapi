import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import PublicBenefitProvidersManagement from '@/features/public-benefit-providers';

function ProtectedPublicBenefitProviders() {
  return (
    <RouteGuard requiredScopes={['read_settings']}>
      <PublicBenefitProvidersManagement />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/public-benefit-providers/')({
  component: ProtectedPublicBenefitProviders,
});
