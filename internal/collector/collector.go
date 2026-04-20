package collector

import (
	"context"

	"infohub/internal/model"
)

type Collector interface {
	Name() string
	Collect(ctx context.Context) ([]model.DataItem, error)
}
