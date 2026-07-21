package collector

import (
	"math"
	"strings"
)

const tokensPerMillion = 1_000_000

type localModelUsage struct {
	Tokens        float64
	Input         float64
	Output        float64
	CacheRead     float64
	CacheCreation float64
	Reasoning     float64
	Messages      float64
}

type localTokenPrice struct {
	Name          string
	Input         float64
	Output        float64
	CacheRead     float64
	CacheCreation float64
	Reasoning     float64
}

type localCostSummary struct {
	TotalCost      float64
	PricedTokens   float64
	UnpricedTokens float64
	Models         map[string]any
}

func (b localUsageBucket) costSummary(source string) localCostSummary {
	summary := localCostSummary{Models: map[string]any{}}
	for modelName, usage := range b.ModelUsage {
		price, ok := localPriceForModel(source, modelName)
		if !ok {
			summary.UnpricedTokens += usage.Tokens
			summary.Models[modelName] = map[string]any{
				"tokens": usage.Tokens,
				"priced": false,
			}
			continue
		}

		cost := localUsageCost(usage, price)
		summary.TotalCost += cost
		summary.PricedTokens += usage.Tokens
		summary.Models[modelName] = map[string]any{
			"tokens":      usage.Tokens,
			"cost":        roundLocalCost(cost),
			"priced":      true,
			"price_model": price.Name,
		}
	}
	summary.TotalCost = roundLocalCost(summary.TotalCost)
	return summary
}

func localUsageCost(usage localModelUsage, price localTokenPrice) float64 {
	return (usage.Input*price.Input +
		usage.Output*price.Output +
		usage.CacheRead*price.CacheRead +
		usage.CacheCreation*price.CacheCreation +
		usage.Reasoning*price.Reasoning) / tokensPerMillion
}

func localPriceForModel(source, modelName string) (localTokenPrice, bool) {
	normalized := normalizeLocalModelName(modelName)
	if normalized == "" || normalized == "unknown" {
		return localTokenPrice{}, false
	}

	switch source {
	case localClaudeSource:
		return claudeLocalPrice(normalized)
	case localCodexSource:
		return codexLocalPrice(normalized)
	default:
		return localTokenPrice{}, false
	}
}

func claudeLocalPrice(model string) (localTokenPrice, bool) {
	switch {
	case strings.Contains(model, "fable-5"), strings.Contains(model, "mythos-5"):
		return localTokenPrice{Name: "claude-fable-5", Input: 10, Output: 50, CacheRead: 1, CacheCreation: 12.5}, true
	case strings.Contains(model, "opus-4.8"), strings.Contains(model, "opus-4-8"),
		strings.Contains(model, "opus-4.7"), strings.Contains(model, "opus-4-7"),
		strings.Contains(model, "opus-4.6"), strings.Contains(model, "opus-4-6"),
		strings.Contains(model, "opus-4.5"), strings.Contains(model, "opus-4-5"):
		return localTokenPrice{Name: "claude-opus-4.5+", Input: 5, Output: 25, CacheRead: 0.5, CacheCreation: 6.25}, true
	case strings.Contains(model, "opus-4.1"), strings.Contains(model, "opus-4-1"),
		strings.Contains(model, "opus-4"):
		return localTokenPrice{Name: "claude-opus-4", Input: 15, Output: 75, CacheRead: 1.5, CacheCreation: 18.75}, true
	case strings.Contains(model, "sonnet-4.6"), strings.Contains(model, "sonnet-4-6"),
		strings.Contains(model, "sonnet-4.5"), strings.Contains(model, "sonnet-4-5"),
		strings.Contains(model, "sonnet-4"):
		return localTokenPrice{Name: "claude-sonnet-4", Input: 3, Output: 15, CacheRead: 0.3, CacheCreation: 3.75}, true
	case strings.Contains(model, "haiku-4.5"), strings.Contains(model, "haiku-4-5"):
		return localTokenPrice{Name: "claude-haiku-4.5", Input: 1, Output: 5, CacheRead: 0.1, CacheCreation: 1.25}, true
	case strings.Contains(model, "haiku-3.5"), strings.Contains(model, "haiku-3-5"):
		return localTokenPrice{Name: "claude-haiku-3.5", Input: 0.8, Output: 4, CacheRead: 0.08, CacheCreation: 1}, true
	default:
		return localTokenPrice{}, false
	}
}

func codexLocalPrice(model string) (localTokenPrice, bool) {
	switch {
	case strings.Contains(model, "gpt-5.5-pro"):
		return openAILocalPrice("gpt-5.5-pro", 30, 0, 180), true
	case strings.Contains(model, "gpt-5.5"):
		return openAILocalPrice("gpt-5.5", 5, 0.5, 30), true
	case strings.Contains(model, "gpt-5.4-pro"):
		return openAILocalPrice("gpt-5.4-pro", 30, 0, 180), true
	case strings.Contains(model, "gpt-5.4-mini"):
		return openAILocalPrice("gpt-5.4-mini", 0.75, 0.075, 4.5), true
	case strings.Contains(model, "gpt-5.4-nano"):
		return openAILocalPrice("gpt-5.4-nano", 0.2, 0.02, 1.25), true
	case strings.Contains(model, "gpt-5.4"):
		return openAILocalPrice("gpt-5.4", 2.5, 0.25, 15), true
	case strings.HasPrefix(model, "gpt-") && strings.Contains(model, "codex"):
		return openAILocalPrice("gpt-5.3-codex", 1.75, 0.175, 14), true
	case strings.Contains(model, "chat-latest"):
		return openAILocalPrice("chat-latest", 5, 0.5, 30), true
	default:
		return localTokenPrice{}, false
	}
}

func openAILocalPrice(name string, input, cacheRead, output float64) localTokenPrice {
	return localTokenPrice{
		Name:      name,
		Input:     input,
		Output:    output,
		CacheRead: cacheRead,
		Reasoning: output,
	}
}

func normalizeLocalModelName(modelName string) string {
	return strings.ToLower(strings.TrimSpace(modelName))
}

func roundLocalCost(cost float64) float64 {
	return math.Round(cost*1_000_000) / 1_000_000
}
