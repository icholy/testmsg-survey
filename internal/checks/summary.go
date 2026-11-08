package checks

// Counts holds bare and contextual counts.
type Counts struct {
	Bare       int `json:"bare"`
	Contextual int `json:"contextual"`
}

func (c *Counts) add(bare bool) {
	if bare {
		c.Bare++
	} else {
		c.Contextual++
	}
}

// Summary is the aggregate result for one source tree.
type Summary struct {
	TestFiles       int                  `json:"test_files"`
	ParseErrors     int                  `json:"parse_errors"`
	TestParseErrors []string             `json:"test_parse_errors,omitempty"`
	Total           Counts               `json:"total"`
	ByMechanism     map[Mechanism]Counts `json:"by_mechanism"`
	ByFunc          map[string]Counts    `json:"by_func"`
	BySink          map[string]Counts    `json:"by_sink"`
	UnresolvedN     int                  `json:"unresolved_count"`

	Checks     []Check      `json:"checks,omitempty"`
	Unresolved []Unresolved `json:"unresolved,omitempty"`
}

// Summarize aggregates a result.
func Summarize(m *Module, res Result) Summary {
	s := Summary{
		TestFiles:       res.TestFiles,
		ParseErrors:     m.ParseErrors,
		TestParseErrors: m.TestParseErrors,
		ByMechanism:     map[Mechanism]Counts{},
		ByFunc:          map[string]Counts{},
		BySink:          map[string]Counts{},
		UnresolvedN:     len(res.Unresolved),
	}
	for _, c := range res.Checks {
		s.Total.add(c.Bare)
		m := s.ByMechanism[c.Mechanism]
		m.add(c.Bare)
		s.ByMechanism[c.Mechanism] = m
		f := s.ByFunc[c.Func]
		f.add(c.Bare)
		s.ByFunc[c.Func] = f
		sink := c.Sink
		if c.Mechanism == Helper {
			sink = "helper"
		}
		k := s.BySink[sink]
		k.add(c.Bare)
		s.BySink[sink] = k
	}
	return s
}
