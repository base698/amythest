package source

import "context"

// Registry holds the configured sources. Registration order is merge order:
// within a Today section, earlier sources' items sort ahead on due-date ties.
type Registry struct {
	sources []Source
}

func NewRegistry(sources ...Source) *Registry {
	return &Registry{sources: sources}
}

func (r *Registry) All() []Source { return r.sources }

func (r *Registry) Get(name string) (Source, bool) {
	for _, s := range r.sources {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

// DueItems concatenates every source's items in registration order. A
// failing source contributes its error to errs (keyed by name) without
// discarding the others' results — a dead ticketing system must not blank
// the daily view.
func (r *Registry) DueItems(ctx context.Context, day string, includeDone bool) (items []Item, errs map[string]error) {
	for _, s := range r.sources {
		found, err := s.DueItems(ctx, day, includeDone)
		if err != nil {
			if errs == nil {
				errs = map[string]error{}
			}
			errs[s.Name()] = err
			continue
		}
		items = append(items, found...)
	}
	return items, errs
}
