package service

import (
	"context"
	"errors"
)

// InvalidateAuthCacheByKey 清除指定 API Key 的认证缓存
func (s *APIKeyService) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	if key == "" {
		return
	}
	cacheKey := s.authCacheKey(key)
	s.deleteAuthCache(ctx, cacheKey)
}

// InvalidateAuthCacheByUserID 清除用户相关的 API Key 认证缓存
func (s *APIKeyService) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	if userID <= 0 {
		return
	}
	keys, err := s.apiKeyRepo.ListKeysByUserID(ctx, userID)
	if err != nil {
		return
	}
	_ = s.deleteAuthCacheByKeys(ctx, keys)
}

// InvalidateAuthCacheByGroupID 清除分组相关的 API Key 认证缓存
func (s *APIKeyService) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	_ = s.invalidateAuthCacheByGroupID(ctx, groupID)
}

// Managed synchronization must not acknowledge a revision before every cache clears.
func (s *APIKeyService) invalidateAuthCacheByGroupID(ctx context.Context, groupID int64) error {
	if groupID <= 0 {
		return nil
	}
	keys, err := s.apiKeyRepo.ListKeysByGroupID(ctx, groupID)
	if err != nil {
		return err
	}
	return s.deleteAuthCacheByKeys(ctx, keys)
}

func (s *APIKeyService) deleteAuthCacheByKeys(ctx context.Context, keys []string) error {
	var result error
	for _, key := range keys {
		if key == "" {
			continue
		}
		result = errors.Join(result, s.deleteAuthCacheWithError(ctx, s.authCacheKey(key)))
	}
	return result
}
