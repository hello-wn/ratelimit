package limiter

import (
	"bytes"
	"strconv"
	"sync"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/filter"
	"github.com/envoyproxy/ratelimit/src/utils"

	logger "github.com/sirupsen/logrus"
)

const (
	entryKeyRemoteAddr = "remote_address"
	entryKeyUserID     = "user_id"
	cacheKeyBlocked    = "_user_blocked"
)

type CacheKeyGenerator struct {
	prefix string
	// bytes.Buffer pool used to efficiently generate cache keys.
	bufferPool sync.Pool
}

func NewCacheKeyGenerator(prefix string) CacheKeyGenerator {
	return CacheKeyGenerator{
		prefix: prefix,
		bufferPool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}
}

type CacheKey struct {
	Key string
	// True if the key corresponds to a limit with a SECOND unit. False otherwise.
	PerSecond bool
}

func isPerSecondLimit(unit pb.RateLimitResponse_RateLimit_Unit) bool {
	return unit == pb.RateLimitResponse_RateLimit_SECOND
}

// Generate a cache key for a limit lookup.
// @param domain supplies the cache key domain.
// @param descriptor supplies the descriptor to generate the key for.
// @param limit supplies the rate limit to generate the key for (may be nil).
// @param now supplies the current unix time.
// @return CacheKey struct.
func (this *CacheKeyGenerator) GenerateCacheKey(
	domain string, descriptor *pb_struct.RateLimitDescriptor, limit *config.RateLimit, now int64, ipFilter filter.Filter, uidFilter filter.Filter) CacheKey {

	if limit == nil {
		return CacheKey{
			Key:       "",
			PerSecond: false,
		}
	}

	b := this.bufferPool.Get().(*bytes.Buffer)
	defer this.bufferPool.Put(b)
	b.Reset()

	b.WriteString(this.prefix)
	b.WriteString(domain)
	b.WriteByte('_')

	for _, entry := range descriptor.Entries {
		if domain == "edge_proxy_per_ip" {
			logger.Debugf("Checking entry key: %s , entry value: %s", entry.Key, entry.Value)

			if entry.Key == entryKeyRemoteAddr {
				switch action, reason := ipFilter.Match(entry.Value); action {
				case filter.FilterActionAllow:
					return CacheKey{
						Key:       "",
						PerSecond: false,
					}
				case filter.FilterActionDeny:
					return CacheKey{
						Key:       cacheKeyBlocked,
						PerSecond: false,
					}
				case filter.FilterActionError:
					logger.Warningf(reason)
				}
			}

			if entry.Key == entryKeyUserID {
				switch action, _ := uidFilter.Match(entry.Value); action {
				case filter.FilterActionAllow:
					return CacheKey{
						Key:       "",
						PerSecond: false,
					}
				case filter.FilterActionDeny:
					return CacheKey{
						Key:       cacheKeyBlocked,
						PerSecond: false,
					}
				}
			}
		}

		b.WriteString(entry.Key)
		b.WriteByte('_')
		b.WriteString(entry.Value)
		b.WriteByte('_')
	}

	divider := utils.UnitToDivider(limit.Limit.Unit)
	b.WriteString(strconv.FormatInt((now/divider)*divider, 10))

	return CacheKey{
		Key:       b.String(),
		PerSecond: isPerSecondLimit(limit.Limit.Unit),
	}
}
