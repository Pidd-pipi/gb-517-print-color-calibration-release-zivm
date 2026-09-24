
import { EntityPage } from '../components/EntityPage';
import { ENTITY_CONFIGS } from '../types/status';
import { useReleaseDecisionStore } from '../stores/release-decision';
import { ReleaseBoard } from '../components/run/ReleaseBoard';
import { roleAtLeast, useAuth } from '../hooks/useAuth';

export default function ReleaseDecisionPage() {
  const { session } = useAuth();
  const canReview = roleAtLeast(session?.role, 'reviewer');
  return <>
    <ReleaseBoard canReview={canReview} />
    <EntityPage config={ENTITY_CONFIGS[3]} useStore={useReleaseDecisionStore} />
  </>;
}
