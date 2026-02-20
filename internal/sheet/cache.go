package sheet

import (
	"context"
	"sync"
)

// OverviewCache provides thread-safe caching of ParseOverview results.
// Multiple test cases sharing the same spreadsheet file reuse the cached result.
type OverviewCache struct {
	mu    sync.RWMutex
	store map[string]*SpreadsheetOverview
}

func NewOverviewCache() *OverviewCache {
	return &OverviewCache{
		store: make(map[string]*SpreadsheetOverview),
	}
}

func (c *OverviewCache) GetOrParse(ctx context.Context, parser *ExcelizeParser, filePath string) (*SpreadsheetOverview, error) {
	c.mu.RLock()
	if ov, ok := c.store[filePath]; ok {
		c.mu.RUnlock()
		return ov, nil
	}
	c.mu.RUnlock()

	ov, err := parser.ParseOverview(ctx, filePath)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.store[filePath] = ov
	c.mu.Unlock()
	return ov, nil
}
