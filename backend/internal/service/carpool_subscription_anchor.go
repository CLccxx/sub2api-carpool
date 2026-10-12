package service

import "context"

// SetWeeklyAnchorPolicy is configured on managed B only, before serving requests.
func (s *SubscriptionService) SetWeeklyAnchorPolicy(resolve func(context.Context, int64) (bool, error)) {
	s.weeklyAnchorPolicy = resolve
}

func (s *SubscriptionService) applyWeeklyAnchorPolicy(ctx context.Context, sub *UserSubscription) error {
	if sub == nil {
		return ErrSubscriptionNilInput
	}
	if s.weeklyAnchorPolicy == nil {
		return nil
	}
	aligned, err := s.weeklyAnchorPolicy(ctx, sub.GroupID)
	if err != nil {
		return err
	}
	sub.AccountAlignedWeekly = aligned
	return nil
}

func (s *SubscriptionService) applyWeeklyAnchorPolicies(ctx context.Context, subs []UserSubscription) error {
	if s.weeklyAnchorPolicy == nil {
		return nil
	}
	policies := make(map[int64]bool)
	for i := range subs {
		groupID := subs[i].GroupID
		aligned, ok := policies[groupID]
		if !ok {
			var err error
			aligned, err = s.weeklyAnchorPolicy(ctx, groupID)
			if err != nil {
				return err
			}
			policies[groupID] = aligned
		}
		subs[i].AccountAlignedWeekly = aligned
	}
	return nil
}
