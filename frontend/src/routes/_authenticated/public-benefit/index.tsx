import { createFileRoute } from '@tanstack/react-router';
import { RouteGuard } from '@/components/route-guard';
import PublicBenefitDashboardPage from '@/features/public-benefit-dashboard';

function ProtectedPublicBenefitDashboard() {
  return (
    <RouteGuard requiredScopes={['read_settings']}>
      <PublicBenefitDashboardPage />
    </RouteGuard>
  );
}

export const Route = createFileRoute('/_authenticated/public-benefit/')({
  component: ProtectedPublicBenefitDashboard,
});
