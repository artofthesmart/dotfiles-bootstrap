package catalog

import "context"

type Entry struct {
	ID         string
	OllamaName string
}

type Repository interface {
	List(context.Context) ([]Entry, error)
	Find(context.Context, string) (Entry, error)
}
