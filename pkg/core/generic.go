package core

// Resolve returns the instance of the one module that delivers capability T.
// It is the type-safe form of (*Registry).Resolve, written core.Resolve[KV](reg).
func Resolve[T any](r *Registry) (T, error) {
	var zero T
	v, err := r.Resolve(token[T]())
	if err != nil {
		return zero, err
	}
	//nolint:errcheck // Resolve matched this very token[T]; a mismatch is a registry defect the panic reports, not a caller case.
	return v.(T), nil
}

// ResolveAll returns every constructed instance that delivers capability T, in module-name order.
// T must be an interface.
func ResolveAll[T any](r *Registry) []T {
	values := r.ResolveAll(token[T]())
	out := make([]T, 0, len(values))
	for _, v := range values {
		//nolint:errcheck // ResolveAll selected these instances by the same
		// token[T], so the assertion holds for the reason Resolve's does above.
		out = append(out, v.(T))
	}
	return out
}

// Resources returns every declared resource matching T, in module-name order.
// T designates a resource type, so any type is legal here.
func Resources[T any](r *Registry) []Resource[T] {
	found := r.Resources(token[T]())
	out := make([]Resource[T], 0, len(found))
	for _, res := range found {
		//nolint:errcheck // the assertion cannot fail: an interface T selects
		// only implementations of it and a concrete T only values of T itself,
		// so the assertion holds for the reason Resolve's does above.
		out = append(out, Resource[T]{Module: res.Module, Value: res.Value.(T)})
	}
	return out
}

func token[T any]() Token {
	var p *T
	return p
}
