package providers

// Registry holds the set of payment providers active for this deployment,
// keyed by Provider.Name(). Business logic (services/, handlers/, main's job
// handlers) looks providers up by name rather than depending on a single
// hard-coded provider, so more than one payment rail (Stripe, BTCPay, ...)
// can be active at once (PLAN.md section 9).
type Registry struct {
	byName map[string]Provider
}

// NewRegistry builds a Registry from a set of providers. Nil providers are
// skipped, so callers can conditionally construct adapters and pass them all
// through without an extra filter step.
func NewRegistry(ps ...Provider) Registry {
	m := make(map[string]Provider, len(ps))
	for _, p := range ps {
		if p == nil {
			continue
		}
		m[p.Name()] = p
	}
	return Registry{byName: m}
}

// Get resolves a provider by name.
func (r Registry) Get(name string) (Provider, bool) {
	p, ok := r.byName[name]
	return p, ok
}

// Has reports whether a provider with the given name is registered.
func (r Registry) Has(name string) bool {
	_, ok := r.byName[name]
	return ok
}

// Empty reports whether no providers are configured, e.g. because payments
// are disabled entirely.
func (r Registry) Empty() bool {
	return len(r.byName) == 0
}

// Names returns the registered provider names, for building a checkout
// payment-method selector.
func (r Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for name := range r.byName {
		out = append(out, name)
	}
	return out
}
