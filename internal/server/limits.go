package server

type Limits struct {
	MaxUpstreams int // 0 = unlimited (Pro)
	MaxUsers int // 0 = unlimited (Pro)
	PerRouteRateLimits bool
	IPAllowDeny bool
	LogExport bool
	MultipleAdminKeys bool
}

// DefaultLimits returns fully-unlocked limits for the standalone edition.
func DefaultLimits() Limits {
	return Limits{
		MaxUpstreams: 0,
		MaxUsers: 0,
		PerRouteRateLimits: true,
		IPAllowDeny: true,
		LogExport: true,
		MultipleAdminKeys: true,
}
}

// LimitReached returns true if the current count meets or exceeds the limit.
// A limit of 0 is treated as unlimited.
func LimitReached(limit, current int) bool {
	if limit == 0 {
		return false
	}
	return current >= limit
}
