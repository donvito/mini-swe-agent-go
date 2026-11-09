package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	mswea "github.com/donvito/mini-swe-agent-go"
)

// The same cost map that litellm uses. Override with $MSWEA_COST_MAP_URL.
const defaultCostMapURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

type modelPrice struct {
	InputCostPerToken       float64 `json:"input_cost_per_token"`
	OutputCostPerToken      float64 `json:"output_cost_per_token"`
	CacheReadInputTokenCost float64 `json:"cache_read_input_token_cost"`
}

var (
	costMapOnce sync.Once
	costMap     map[string]modelPrice
)

func parseCostMap(content []byte, into map[string]modelPrice) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return err
	}
	for name, entry := range raw {
		var price modelPrice
		if json.Unmarshal(entry, &price) == nil {
			into[name] = price
		}
	}
	return nil
}

// loadCostMap loads the litellm cost map, cached in the global config dir for a day.
func loadCostMap() map[string]modelPrice {
	costMapOnce.Do(func() {
		costMap = map[string]modelPrice{}
		cachePath := filepath.Join(mswea.GlobalConfigDir(), "model_prices_and_context_window.json")
		if info, err := os.Stat(cachePath); err != nil || time.Since(info.ModTime()) > 24*time.Hour {
			if err := downloadCostMap(cachePath); err != nil {
				slog.Warn("Could not download model cost map", "error", err)
			}
		}
		if content, err := os.ReadFile(cachePath); err == nil {
			if err := parseCostMap(content, costMap); err != nil {
				slog.Warn("Could not parse model cost map", "path", cachePath, "error", err)
			}
		}
	})
	return costMap
}

func downloadCostMap(path string) error {
	url := os.Getenv("MSWEA_COST_MAP_URL")
	if url == "" {
		url = defaultCostMapURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := parseCostMap(content, map[string]modelPrice{}); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func (m *OpenAIModel) lookupPrice() (modelPrice, bool) {
	if m.Config.InputCostPerToken > 0 || m.Config.OutputCostPerToken > 0 {
		return modelPrice{InputCostPerToken: m.Config.InputCostPerToken, OutputCostPerToken: m.Config.OutputCostPerToken}, true
	}
	prices := loadCostMap()
	if m.Config.LitellmModelRegistry != "" {
		if content, err := os.ReadFile(m.Config.LitellmModelRegistry); err == nil {
			registry := map[string]modelPrice{}
			if err := parseCostMap(content, registry); err != nil {
				slog.Warn("Could not parse model registry", "path", m.Config.LitellmModelRegistry, "error", err)
			}
			for _, name := range []string{m.Config.ModelName, m.apiModelName} {
				if price, ok := registry[name]; ok {
					return price, true
				}
			}
		}
	}
	for _, name := range []string{m.Config.ModelName, m.provider + "/" + m.apiModelName, m.apiModelName} {
		if price, ok := prices[name]; ok {
			return price, true
		}
	}
	return modelPrice{}, false
}

func (m *OpenAIModel) calculateCost(response map[string]any) (float64, error) {
	usage, _ := response["usage"].(map[string]any)
	cost, _ := usage["cost"].(float64)
	reason := "the API did not report a cost"
	if cost <= 0 {
		if price, ok := m.lookupPrice(); ok {
			promptTokens, _ := usage["prompt_tokens"].(float64)
			completionTokens, _ := usage["completion_tokens"].(float64)
			details, _ := usage["prompt_tokens_details"].(map[string]any)
			cachedTokens, _ := details["cached_tokens"].(float64)
			cacheReadCost := price.CacheReadInputTokenCost
			if cacheReadCost == 0 {
				cacheReadCost = price.InputCostPerToken
			}
			cost = (promptTokens-cachedTokens)*price.InputCostPerToken + cachedTokens*cacheReadCost + completionTokens*price.OutputCostPerToken
			reason = fmt.Sprintf("cost must be > 0.0, got %v", cost)
		} else {
			reason = "model not found in the cost map"
		}
	}
	if cost <= 0 {
		if m.Config.CostTracking == "ignore_errors" {
			return 0, nil
		}
		msg := fmt.Sprintf("Error calculating cost for model %s: %s. "+
			"You can ignore this issue from your config file with cost_tracking: 'ignore_errors' or "+
			"globally with export MSWEA_COST_TRACKING='ignore_errors'. "+
			"Alternatively, set model.input_cost_per_token and model.output_cost_per_token in your config.",
			m.Config.ModelName, reason)
		slog.Error(msg)
		return 0, fmt.Errorf("%s", msg)
	}
	return cost, nil
}
