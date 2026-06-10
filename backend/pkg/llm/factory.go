package llm

import "fmt"

// Factory builds a provider's Analyzer. Adapters self-register from init()
// (callers blank-import the adapter package); this keeps llm free of
// adapter imports and adapters free of each other.
type Factory func(apiKey, visionModel, textModel string) (Analyzer, error)

var providers = map[string]Factory{}

func Register(name string, f Factory) { providers[name] = f }

func New(provider, apiKey, visionModel, textModel string) (Analyzer, error) {
	f, ok := providers[provider]
	if !ok {
		return nil, fmt.Errorf("llm: unknown provider %q", provider)
	}
	return f(apiKey, visionModel, textModel)
}
