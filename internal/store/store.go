package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"infohub/internal/config"
	"infohub/internal/model"
)

var ErrSourceNotFound = errors.New("source not found")

type Store interface {
	Save(source string, items []model.DataItem) error
	SaveFailure(source string, err error, fetchedAt time.Time) error
	GetBySource(source string) (model.SourceSnapshot, error)
	GetAll() (map[string]model.SourceSnapshot, error)
	Close() error
}

func New(cfg config.StoreConfig) (Store, error) {
	switch strings.ToLower(cfg.Type) {
	case "memory":
		return NewMemoryStore(), nil
	case "sqlite":
		return NewSQLiteStore(cfg.SQLitePath)
	default:
		return nil, fmt.Errorf("unsupported store type %q", cfg.Type)
	}
}
