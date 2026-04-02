package server

import "github.com/stockyard-dev/stockyard-gate/internal/license"

// Limits holds the feature limits for the current license tier.
// All int limits: 0 means unlimited (Pro tier only).
type Limits struct {
	MaxUpstreams int // 0 = unlimited (Pro)
	MaxUsers int // 0 = unlimited (Pro)
	PerRouteRateLimits bool
	IPAllowDeny bool
	LogExport bool
	MultipleAdminKeys bool
}

var freeLimits = Limits{
		MaxUpstreams: 1,
		MaxUsers: 5,
		PerRouteRateLimits: false,
		IPAllowDeny: false,
		LogExport: false,
		MultipleAdminKeys: false,
}

var proLimits = Limits{
		MaxUpstreams: 0,
		MaxUsers: 0,
		PerRouteRateLimits: true,
		IPAllowDeny: true,
		LogExport: true,
		MultipleAdminKeys: true,
}

// LimitsFor returns the appropriate Limits for the given license info.
// nil info = no key set = free tier.
func LimitsFor(info *license.Info) Limits {
	if info != nil && info.IsPro() {
		return proLimits
	}
	return freeLimits
}

// LimitReached returns true if the current count meets or exceeds the limit.
// A limit of 0 is treated as unlimited.
func LimitReached(limit, current int) bool {
	if limit == 0 {
		return false
	}
	return current >= limit
}
