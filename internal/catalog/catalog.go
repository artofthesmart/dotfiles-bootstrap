package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"

	catalogdata "github.com/artofthesmart/dotfiles-bootstrap/catalog"
)

var ErrNotFound = errors.New("catalog entry not found")

type Repository interface {
	List(context.Context) ([]Entry, error)
	Find(context.Context, string) (Entry, error)
}

type EmbeddedRepository struct {
	catalog Catalog
}

var (
	embeddedOnce sync.Once
	embedded     Catalog
	embeddedErr  error
)

func LoadEmbedded() (Catalog, error) {
	embeddedOnce.Do(func() {
		embedded, embeddedErr = Decode(catalogdata.ModelsJSON)
	})
	if embeddedErr != nil {
		return Catalog{}, embeddedErr
	}
	return cloneCatalog(embedded)
}

func MustLoadEmbedded() Catalog {
	value, err := LoadEmbedded()
	if err != nil {
		panic(err)
	}
	return value
}

func NewEmbeddedRepository() (*EmbeddedRepository, error) {
	value, err := LoadEmbedded()
	if err != nil {
		return nil, err
	}
	return &EmbeddedRepository{catalog: value}, nil
}

func NewRepository(data []byte) (*EmbeddedRepository, error) {
	value, err := Decode(data)
	if err != nil {
		return nil, err
	}
	return &EmbeddedRepository{catalog: value}, nil
}

func Decode(data []byte) (Catalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var value Catalog
	if err := decoder.Decode(&value); err != nil {
		return Catalog{}, fmt.Errorf("decode model catalog: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return Catalog{}, err
	}
	if err := Validate(value); err != nil {
		return Catalog{}, err
	}
	sort.Slice(value.Entries, func(i, j int) bool {
		return value.Entries[i].ID < value.Entries[j].ID
	})
	return value, nil
}

func (r *EmbeddedRepository) Catalog() Catalog {
	value, err := cloneCatalog(r.catalog)
	if err != nil {
		panic(err)
	}
	return value
}

func (r *EmbeddedRepository) List(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cloneEntries(r.catalog.Entries), nil
}

func (r *EmbeddedRepository) Find(ctx context.Context, id string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	index := sort.Search(len(r.catalog.Entries), func(i int) bool {
		return r.catalog.Entries[i].ID >= id
	})
	if index == len(r.catalog.Entries) || r.catalog.Entries[index].ID != id {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return cloneEntry(r.catalog.Entries[index]), nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("decode model catalog: trailing JSON value")
	}
	return fmt.Errorf("decode model catalog: %w", err)
}

func cloneCatalog(value Catalog) (Catalog, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return Catalog{}, err
	}
	var result Catalog
	if err := json.Unmarshal(data, &result); err != nil {
		return Catalog{}, err
	}
	return result, nil
}

func cloneEntries(values []Entry) []Entry {
	result := make([]Entry, len(values))
	for i := range values {
		result[i] = cloneEntry(values[i])
	}
	return result
}

func cloneEntry(value Entry) Entry {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var result Entry
	if err := json.Unmarshal(data, &result); err != nil {
		panic(err)
	}
	return result
}
