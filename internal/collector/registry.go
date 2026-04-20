package collector

import "sort"

type Registry struct {
	collectors map[string]Collector
}

func NewRegistry() *Registry {
	return &Registry{collectors: make(map[string]Collector)}
}

func (r *Registry) Register(c Collector) {
	if c == nil {
		return
	}
	r.collectors[c.Name()] = c
}

func (r *Registry) Get(name string) (Collector, bool) {
	c, ok := r.collectors[name]
	return c, ok
}

func (r *Registry) All() []Collector {
	names := make([]string, 0, len(r.collectors))
	for name := range r.collectors {
		names = append(names, name)
	}
	sort.Strings(names)

	collectors := make([]Collector, 0, len(names))
	for _, name := range names {
		collectors = append(collectors, r.collectors[name])
	}
	return collectors
}
